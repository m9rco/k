// Command server is the single-binary entry point for Game Asset Studio.
//
// It wires configuration, the SQLite store, and HTTP routing (including the
// embedded frontend), then serves until interrupted. Capability-specific routes
// (session, transport, generation, crop, download) are registered on the shared
// mux as those packages are implemented.
package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gameasset/internal/agent"
	"gameasset/internal/config"
	"gameasset/internal/cos"
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/download"
	"gameasset/internal/generation"
	"gameasset/internal/id"
	applog "gameasset/internal/log"
	"gameasset/internal/session"
	"gameasset/internal/store"
	"gameasset/internal/transport"
	"gameasset/internal/video"
	"gameasset/internal/vision"
	"gameasset/internal/websearch"
	"gameasset/internal/workspace"
	"gameasset/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func run() error {
	// Load .env for local development before reading config. Real environment
	// variables always take precedence; a missing file is not an error.
	if err := config.LoadDotenv(".env"); err != nil {
		return err
	}

	cfg, err := config.Load("")
	if err != nil {
		return err
	}

	// Initialise structured diagnostic logging as early as possible (right after
	// config) so every subsequent startup step logs through the facade. A missing
	// file destination falls back to stderr (historical behaviour).
	logCloser, err := applog.Init(applog.Options{
		File:         cfg.Log.File,
		Level:        cfg.Log.Level,
		MirrorStderr: cfg.Log.MirrorStderr,
	})
	if err != nil {
		return fmt.Errorf("init logging: %w", err)
	}
	defer logCloser.Close()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	mux := http.NewServeMux()

	// Health check.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Session management API.
	sessions := session.NewManager(st)
	sessions.RegisterRoutes(mux)

	// Real-time transport: WebSocket (conversation) + SSE (task progress).
	// The inbound handler is wired by the orchestration layer below.
	hub := transport.NewHub(nil)
	broker := transport.NewTaskBroker()
	transport.RegisterRoutes(mux, hub, broker)

	// Image cropping (platform presets + non-AI crop).
	cropSvc := crop.NewService(cfg.Channels, cfg.AssetDir, st, func() string { return id.New("crop") })
	cropSvc.RegisterRoutes(mux)

	// Image generation (primary + backup failover, async via SSE). Each backend's
	// concrete adapter is selected by its configured Provider key (openai/gemini).
	gen := generation.NewFailoverGenerator(
		generation.NewProvider(cfg.ImagePrimary),
		generation.NewProvider(cfg.ImageBackup),
	)
	genSvc := generation.NewService(gen, st, broker, cfg.AssetDir, id.New)
	// Record the primary image adapter's config so prompt assembly can detect
	// capabilities (e.g. transparent-background support) for size-note rewrites.
	genSvc.SetDefaultImageProvider(cfg.ImagePrimary)
	// Wire the crop service into generation so platform adaptation can use the
	// deterministic crop fast path (ratio-match) and the size catalog. Adaptation
	// is reached only through the conversation agent's adapt_to_platform tool (so
	// the LLM understands the image + intent before any repaint) — there is no
	// direct HTTP endpoint that would bypass the model.
	genSvc.SetCropper(cropSvc)
	// Mirror the orchestrator's per-turn adapt routing (gpt-image-2 →
	// gemini-3-pro-image) onto the generation service so RetryAsset can re-force it
	// for adapt products (gen_origin strips the provider override before persisting).
	if pc, ok := cfg.ResolveImageModel(config.SceneImage, "gpt-image-2"); ok {
		genSvc.SetAdaptImageProvider(&pc)
	} else if pc, ok := cfg.ResolveImageModel(config.SceneImage, "gemini-3-pro-image"); ok {
		genSvc.SetAdaptImageProvider(&pc)
	}

	// Outpaint convergence provider for extreme-ratio platform adaptation (e.g. a
	// 2:1 product toward a 4:1 banner): the product is padded to the target ratio
	// with transparent margins and this provider fills them by extending the
	// scene. Without an API key it stays unset and the outpaint path falls back to
	// transparent band padding, so adaptation still produces a valid product.
	if cfg.ImageOutpaint.APIKey != "" {
		genSvc.SetOutpainter(generation.NewProvider(cfg.ImageOutpaint))
		log.Printf("outpaint: provider=%s model=%s enabled for extreme-ratio adaptation", cfg.ImageOutpaint.Provider, cfg.ImageOutpaint.Model)
	} else {
		log.Printf("outpaint: not configured, extreme-ratio adaptation falls back to band padding")
	}

	// Image-to-video service (happyhorse). The provider fetches the source image
	// by public URL, so video requires a COS uploader to publish the local frame
	// first. Without COS configured the uploader stays nil, Service.Configured()
	// is false, the tool is left out of the whitelist, and the agent politely
	// reports "暂未配置" instead of attempting a call that cannot work.
	vidSvc := video.NewService(video.NewProvider(cfg.Video), st, broker, cfg.AssetDir, id.New)
	cosUploader, err := cos.New(cfg.COS)
	if err != nil {
		return fmt.Errorf("init cos uploader: %w", err)
	}
	if cosUploader != nil {
		vidSvc.SetUploader(cosUploader)
		log.Printf("video: COS uploader configured (bucket=%s), image-to-video enabled", cfg.COS.Bucket)
	} else {
		log.Printf("video: COS not configured, image-to-video disabled")
	}

	// Broadcast task_created over the WS conversation channel the instant a task
	// is created, so the workspace paints a placeholder immediately rather than
	// waiting for the agent turn to finish (deterministic, not callback-dependent).
	announcer := taskAnnouncer{hub: hub}
	genSvc.SetAnnouncer(announcer)
	vidSvc.SetAnnouncer(announcer)

	// Game-asset crawl service (pluggable source; degrades when unconfigured).
	crawlSvc := crawl.NewService(crawl.NewHTTPSource(cfg.CrawlEndpoint, cfg.CrawlAPIKey), st, broker, cfg.AssetDir, id.New)

	// Web search service (DDG text + Bing images, no API key required).
	webSearchSvc := websearch.NewService(websearch.DefaultSource(), st, broker, cfg.AssetDir, id.New)
	webSearchSvc.SetAnnouncer(announcer)

	// Asset workspace: list assets/tasks, upload source images, partial retry.
	wsSvc := workspace.NewService(st, cfg.AssetDir, func() string { return id.New("asset") },
		func(sessionID, taskID string) error {
			return genSvc.Retry(context.Background(), sessionID, taskID)
		},
		// cancelFn aborts an in-flight task. Dispatch by kind: video tasks go to
		// the video service, everything else to generation. Both share the same
		// store, so the one that owns the in-memory cancel also deletes the row.
		func(sessionID, taskID string) (int64, error) {
			rec, err := st.GetTask(sessionID, taskID)
			if err != nil {
				return 0, err
			}
			if rec != nil && rec.Kind == "video" {
				return vidSvc.Cancel(sessionID, taskID)
			}
			return genSvc.Cancel(sessionID, taskID)
		})
	wsSvc.RegisterRoutes(mux)
	// Asset-level retry: re-run the AI flow that produced a SUCCEEDED product as a
	// new asset (the original is left in place). RetryAsset re-forces the adapt
	// provider internally for adapt products, so override stays nil here.
	wsSvc.SetRetryAsset(func(sessionID, assetID string) (string, error) {
		return genSvc.RetryAsset(context.Background(), sessionID, assetID, nil)
	})

	// Download: single-asset attachment + server-side zip packaging.
	dlSvc := download.NewService(st)
	dlSvc.RegisterRoutes(mux)

	// Conversation orchestration: Eino ReAct agent over the whitelist of tools.
	orch := agent.NewOrchestrator(cfg, genSvc, cropSvc, vidSvc, crawlSvc, hub, st, id.New)
	// Track the last produced asset per session so follow-up turns default to
	// editing the latest output (sticky-last-output continuity).
	genSvc.SetAssetCallback(orch.SetLastProduced)
	vidSvc.SetAssetCallback(orch.SetLastProduced)
	// Text-to-image (wan/qwen): a second generation service wired with the
	// text-to-image provider. Only enabled when its API key is configured, so the
	// generate_image_from_text tool stays out of the whitelist otherwise.
	if cfg.TextToImage.APIKey != "" {
		t2iGen := generation.NewFailoverGenerator(generation.NewProvider(cfg.TextToImage), nil)
		t2iSvc := generation.NewService(t2iGen, st, broker, cfg.AssetDir, id.New)
		t2iSvc.SetAnnouncer(announcer)
		t2iSvc.SetAssetCallback(orch.SetLastProduced)
		orch.SetTextToImage(t2iSvc)
		log.Printf("text-to-image: provider=%s model=%s enabled", cfg.TextToImage.Provider, cfg.TextToImage.Model)
	} else {
		log.Printf("text-to-image: not configured, capability disabled")
	}
	orch.SetWebSearch(webSearchSvc)
	log.Printf("web-search: DDG text + Bing images enabled (no API key)")
	// Vision pre-stage for platform adaptation: publish refs to COS (md5-deduped)
	// then analyze with grok-4-fast to produce a theme report injected into the
	// AI-repaint prompt. Both capabilities are optional; nil disables gracefully.
	visionBase, visionKey := cfg.VisionCredential()
	visionAnalyzer := vision.New(visionBase, visionKey, "grok-4-fast")
	if visionAnalyzer != nil {
		orch.SetVisionAnalyzer(visionAnalyzer)
		log.Printf("vision: grok-4-fast analysis enabled (base=%s)", visionBase)
	} else {
		log.Printf("vision: yunwu/common credentials not configured, vision analysis disabled")
	}
	if cosUploader != nil {
		orch.SetRefPublisher(cosUploader)
	}
	// Upload-time vision prewarm: when both COS and vision are configured, each new
	// upload is analyzed in the background and cached by raw-content md5 — the same
	// key a later single-image adapt uses (internal/agent.visionThemeReport), so the
	// adapt hits the cache instead of re-analyzing. Best effort: an md5 already
	// cached is skipped; publish/analyze failures only log. Unconfigured → nil → the
	// workspace skips prewarming entirely.
	if cosUploader != nil && visionAnalyzer != nil {
		wsSvc.SetPrewarm(func(sessionID, assetID, path, mime string) {
			go func() {
				data, err := os.ReadFile(path)
				if err != nil {
					log.Printf("prewarm: read %s failed: %v", assetID, err)
					return
				}
				key := fmt.Sprintf("%x", md5.Sum(data))
				if cached, err := st.GetVisionReport(key); err == nil && cached != "" {
					return // already analyzed (same content uploaded before)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				url, err := cosUploader.UploadIfAbsent(ctx, data, mime, st)
				if err != nil {
					log.Printf("prewarm: publish %s failed: %v", assetID, err)
					return
				}
				report, err := visionAnalyzer.Analyze(ctx, []string{url}, nil)
				if err != nil {
					log.Printf("prewarm: analyze %s failed: %v", assetID, err)
					return
				}
				if err := st.InsertVisionReport(key, report); err != nil {
					log.Printf("prewarm: cache %s failed: %v", assetID, err)
				}
			}()
		})
		log.Printf("upload prewarm: enabled (publish → analyze → cache by md5)")
	}
	hub.SetHandler(func(ctx context.Context, sessionID string, msg transport.Inbound) {
		switch msg.Type {
		case "user_message":
			text := msg.Text
			// Asset numbering map: lets the model resolve "图N" the user typed and
			// reply with matching labels. Built from the client-provided display
			// order (authoritative for drag-reorders) joined with stored kinds.
			if numbering := buildNumbering(st, sessionID, msg.AssetOrder, msg.Refs, msg.Ref, orch.LastProduced(sessionID)); numbering != "" {
				text = numbering + " " + text
			} else if len(msg.Refs) > 0 {
				// Fallback (no display order supplied): surface up to 16 reference ids
				// (matches generation.MaxReferenceImages).
				refs := msg.Refs
				if len(refs) > 16 {
					refs = refs[:16]
				}
				text = "[reference assets: " + strings.Join(refs, ", ") + "] " + text
			} else if msg.Ref != "" {
				text = "[asset " + msg.Ref + "] " + text
			}
			// Platform-adaptation size ids picked in the size selector: surfaced to
			// the agent as a hidden hint (never shown in the user's bubble) so the
			// model calls adapt_to_platform with the exact ids — keeping raw ids and
			// tool names out of the conversation UI.
			if len(msg.SizeIDs) > 0 {
				text = "[adapt sizes: " + strings.Join(msg.SizeIDs, ", ") + "] " + text
			}
			// Lossless compression defaults to on; an explicit false disables it.
			lossless := msg.Lossless == nil || *msg.Lossless
			runTurn(ctx, orch, hub, sessionID, text, lossless)
		case "capsule_select":
			// A reply to a clarify capsule: prefer the (possibly edited) free text,
			// else fall back to the chosen option value(s). Feed it back into the
			// agent as the next user turn so the conversation continues seamlessly.
			text := strings.TrimSpace(msg.Text)
			if text == "" && len(msg.Selection) > 0 {
				text = strings.Join(msg.Selection, ", ")
			}
			if text == "" {
				return
			}
			if msg.Ref != "" {
				text = "[asset " + msg.Ref + "] " + text
			}
			lossless := msg.Lossless == nil || *msg.Lossless
			runTurn(ctx, orch, hub, sessionID, text, lossless)
		case "cancel_turn":
			// Interrupt the in-flight turn for this session. The next inbound
			// user_message (sent right after by the client) will then start a new
			// turn once the cancelled one releases the per-session turn lock.
			orch.CancelTurn(sessionID)
		}
	})

	// Context-window state for the UI panel.
	mux.HandleFunc("GET /api/session/{id}/window", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, orch.State(r.PathValue("id")))
	})

	// Context cleanup: reset the conversation window (workspace assets untouched).
	mux.HandleFunc("POST /api/session/{id}/context/clear", func(w http.ResponseWriter, r *http.Request) {
		orch.ResetContext(r.PathValue("id"))
		writeJSON(w, map[string]string{"status": "cleared"})
	})

	// Prompt optimization: rewrite a colloquial input into a structured
	// image-generation prompt. One-shot, tool-free, does not touch the session
	// window — the result is returned for the user to confirm before sending.
	mux.HandleFunc("POST /api/session/{id}/prompt/optimize", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		optimized, err := orch.OptimizePrompt(r.Context(), req.Text)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]string{"optimized": optimized})
	})

	// Per-session model selection: list the available catalog (grouped by scene)
	// plus the session's current choices, and switch a scene's model. Switching the
	// chat model triggers a brief self-introduction by the new model.
	mux.HandleFunc("GET /api/session/{id}/models", func(w http.ResponseWriter, r *http.Request) {
		catalog, selected, defaults, err := orch.AvailableModels(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"catalog": catalog, "selected": selected, "defaults": defaults})
	})
	mux.HandleFunc("POST /api/session/{id}/models", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scene string `json:"scene"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := orch.SwitchModel(r.PathValue("id"), config.ModelScene(req.Scene), req.Model); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// Embedded frontend (serves index.html and static assets).
	webFS, err := web.FS()
	if err != nil {
		return err
	}
	mux.Handle("/", spaHandler(webFS))

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on %s (db=%s, assets=%s)", cfg.Addr, cfg.DBPath, cfg.AssetDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("listen error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// writeJSON encodes v as a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// runTurn dispatches an agent turn asynchronously so the connection's readPump
// stays free to read a cancel_turn while a turn is in flight (otherwise an
// interrupt could never be delivered). The orchestrator's per-session turn lock
// keeps concurrently-dispatched turns ordered.
func runTurn(ctx context.Context, orch *agent.Orchestrator, hub *transport.Hub, sessionID, text string, lossless bool) {
	// One trace per turn (per inbound user message); session_id rides along so a
	// single turn is pulled by trace_id and a whole session by session_id. The
	// trace logger is stashed in ctx and survives the async generation boundary
	// (context.WithoutCancel keeps ctx values), so long-task logs link back here.
	ctx = applog.WithTrace(ctx, id.New("trace"), sessionID)
	applog.From(ctx).Info().Str("event", "turn.start").Int("text_len", len(text)).Bool("lossless", lossless).Msg("turn accepted")
	go func() {
		if _, err := orch.Handle(ctx, sessionID, text, lossless); err != nil {
			applog.From(ctx).Error().Str("event", "turn.error").Err(err).Msg("turn failed")
			hub.Send(sessionID, transport.Event{
				Type: transport.EventError,
				Data: map[string]string{"message": err.Error()},
			})
		}
	}()
}

// buildNumbering joins the client-supplied display order with stored asset kinds
// into the "图N → asset_id" context prefix (see agent.BuildAssetNumbering). The
// selected ids (refs / single ref) are annotated so the model knows which the
// user picked; when nothing is selected, lastProduced (the session's most recent
// output) is annotated as "[上次产物: 图N]" so the model defaults to editing it.
// Returns "" when there is no display order to number.
func buildNumbering(st *store.Store, sessionID string, order, refs []string, ref, lastProduced string) string {
	if len(order) == 0 {
		return ""
	}
	kinds := map[string]string{}
	if assets, err := st.ListAssets(sessionID); err == nil {
		for _, a := range assets {
			kinds[a.ID] = a.Kind
		}
	}
	refList := make([]agent.AssetRef, 0, len(order))
	for _, id := range order {
		refList = append(refList, agent.AssetRef{ID: id, Kind: kinds[id]})
	}
	selected := refs
	if len(selected) == 0 && ref != "" {
		selected = []string{ref}
	}
	return agent.BuildAssetNumbering(refList, selected, lastProduced)
}

// taskAnnouncer adapts the WS hub to the generation/video TaskAnnouncer hook:
// it broadcasts a task_created event to the session so the frontend can paint a
// placeholder and subscribe to SSE progress the instant a task is created.
type taskAnnouncer struct{ hub *transport.Hub }

func (a taskAnnouncer) AnnounceTask(sessionID, taskID, kind string, count int) {
	log.Printf("announce: task_created session=%s task=%s kind=%s count=%d conns=%d", sessionID, taskID, kind, count, a.hub.ConnCount(sessionID))
	a.hub.Send(sessionID, transport.Event{
		Type:      transport.EventTaskCreated,
		SessionID: sessionID,
		TaskID:    taskID,
		Data:      map[string]any{"task_id": taskID, "kind": kind, "count": count},
	})
}

// spaHandler serves static files from fsys, falling back to index.html for
// unknown paths so the single-page frontend can handle client-side routing.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			// If the requested file does not exist, fall back to index.html.
			if _, err := fs.Stat(fsys, path[1:]); err != nil && os.IsNotExist(err) {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

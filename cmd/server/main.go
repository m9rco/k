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
	"gameasset/internal/composite"
	"gameasset/internal/config"
	"gameasset/internal/copywriting"
	"gameasset/internal/cos"
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/download"
	"gameasset/internal/generation"
	"gameasset/internal/id"
	"gameasset/internal/layering"
	applog "gameasset/internal/log"
	"gameasset/internal/session"
	"gameasset/internal/store"
	"gameasset/internal/textoverlay"
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

	// Composite persist endpoint: the free-compositing canvas (拼接画布) flattens
	// layers in the browser and posts the PNG here to land it in the workspace.
	// Deterministic storage only — no AI, like crop.
	compositeSvc := composite.NewService(cfg.AssetDir, st, func() string { return id.New("composite") })
	compositeSvc.RegisterRoutes(mux)

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

	// extract_layer (抠图) needs a transparency-capable adapter (Gemini) to cut a
	// subject onto a real alpha background; gpt-image-2 cannot. Wire a Gemini image
	// model as the forced cutout provider. When none is configured the intent is
	// refused at run time (no opaque fallback that fakes a background).
	if pc, ok := cfg.ResolveImageModel(config.SceneImage, "gemini-3-pro-image"); ok {
		genSvc.SetCutoutImageProvider(&pc)
		log.Printf("cutout(extract_layer): provider=%s model=%s enabled", pc.Provider, pc.Model)
	} else if pc, ok := cfg.ResolveImageModel(config.SceneImage, "gemini-2.5-flash-image"); ok {
		genSvc.SetCutoutImageProvider(&pc)
		log.Printf("cutout(extract_layer): provider=%s model=%s enabled", pc.Provider, pc.Model)
	} else {
		log.Printf("cutout(extract_layer): no transparency-capable (Gemini) image model configured, 抠图 disabled")
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

	// Platform-adaptation quality gate: after an AI-repaint product converges, a
	// vision judge (doubao-seed-1-6-vision-250815) scores compliance/subject/appeal;
	// a failing verdict regenerates once with the judge's hints fed back to the
	// image model. No API key => the gate is disabled and every product passes.
	if qc := vision.NewQualityChecker(cfg.Quality.BaseURL, cfg.Quality.APIKey, cfg.Quality.Model, cfg.QualityThreshold, cfg.KeyElementsFidelityMin); qc != nil {
		genSvc.SetQualityChecker(qualityCheckerAdapter{qc})
		genSvc.SetMaxRetry(cfg.QualityMaxRetry)
		log.Printf("quality-gate: %s enabled (threshold=%d key_elements_min=%d max_retry=%d)", cfg.Quality.Model, cfg.QualityThreshold, cfg.KeyElementsFidelityMin, cfg.QualityMaxRetry)
	} else {
		log.Printf("quality-gate: not configured, adapt products pass without review")
	}

	// Pixel pre-filter: fast algorithmic blur+border check before the AI judge.
	if pc := vision.NewPixelChecker(cfg.PixelBlurThreshold, cfg.PixelBorderMaxRatio); pc != nil {
		genSvc.SetPixelChecker(pixelCheckerAdapter{pc})
		log.Printf("pixel-filter: blur_threshold=%d border_max_ratio=%.2f", cfg.PixelBlurThreshold, cfg.PixelBorderMaxRatio)
	}

	// Subject locator: for extreme-ratio banners/strips (≥3:1) the cover crop
	// discards up to half of one axis, so a center crop can decapitate an
	// off-center subject. When configured (reusing the quality-gate vision
	// credentials/model — it already understands subject placement) the converge
	// step runs one vision call to anchor the crop on the detected subject; no
	// key, low confidence, or any error falls back to the center crop.
	if sd := vision.NewSubjectDetector(cfg.Quality.BaseURL, cfg.Quality.APIKey, cfg.Quality.Model); sd != nil {
		genSvc.SetSubjectDetector(subjectDetectorAdapter{sd})
		log.Printf("subject-locator: %s enabled for extreme-ratio crop anchoring", cfg.Quality.Model)
	} else {
		log.Printf("subject-locator: not configured, extreme-ratio crops stay center-anchored")
	}

	// Layer split (图层精修): right-click a workspace image → detect its foreground
	// subjects, cut each onto a transparent layer (extract_layer, Gemini) and inpaint
	// a clean base background (fill_background), then return the layers for the
	// fixed-size compositing canvas. The subject splitter reuses the marketing-vision
	// model (cfg.Vision) — a Gemini inline model that already reads these images;
	// falls back to the quality-gate vision model when Vision lacks credentials.
	layerDetBase, layerDetKey, layerDetModel := cfg.Vision.BaseURL, cfg.Vision.APIKey, cfg.Vision.Model
	if vb, vk := cfg.VisionCredential(); strings.TrimSpace(layerDetKey) == "" {
		layerDetBase, layerDetKey = vb, vk
	}
	if strings.TrimSpace(layerDetKey) == "" {
		layerDetBase, layerDetKey, layerDetModel = cfg.Quality.BaseURL, cfg.Quality.APIKey, cfg.Quality.Model
	}
	if layerDet := vision.NewSubjectDetector(layerDetBase, layerDetKey, layerDetModel); layerDet != nil {
		layeringSvc := layering.NewService(layerDet, genSvc, st)
		layeringSvc.RegisterRoutes(mux)
		log.Printf("layer-split: %s enabled (subject detection → cutout layers + inpaint background)", layerDetModel)
	} else {
		log.Printf("layer-split: not configured (no vision model), 图层精修 disabled")
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

	// Video prompt enricher: uses a fast LLM to expand the user's short motion
	// description into a richer prompt before calling the video provider.
	// Falls back gracefully when the chat credential is not configured.
	if enricher := video.NewLLMEnricher(cfg.ChatPrimary.BaseURL, cfg.ChatPrimary.APIKey, cfg.VideoPromptLLMModel); enricher != nil {
		vidSvc.SetPromptEnricher(enricher)
		log.Printf("video-enricher: %s enabled", cfg.VideoPromptLLMModel)
	}

	// Video source quality check: scores the source image before video generation
	// to collect hints that improve the prompt. Reuses the quality-gate credential.
	if qc := vision.NewQualityChecker(cfg.Quality.BaseURL, cfg.Quality.APIKey, cfg.Quality.Model, cfg.QualityThreshold, cfg.KeyElementsFidelityMin); qc != nil {
		vidSvc.SetVideoQualityChecker(videoQCAdapter{qc})
		log.Printf("video-quality: source image quality check enabled")
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
	// Marketing copy (generate_copy): drafts structured copy via the configured
	// chat model. Enabled whenever the primary chat credential is present (same
	// credential the conversation model uses), so it follows chat availability.
	copyMC := cfg.ChatPrimary
	if cfg.UseTestModel {
		copyMC = cfg.ChatTest
	}
	if copyMC.APIKey != "" {
		copySvc := copywriting.NewService(agent.NewChatCompleter(copyMC))
		orch.SetCopywriter(copySvc)
		log.Printf("copywriting: generate_copy enabled (model=%s)", copyMC.Model)
	} else {
		log.Printf("copywriting: chat credential absent, generate_copy disabled")
	}
	// Text overlay (overlay_text): deterministic font-rendered CTA/badge/headline
	// compositing. Always enabled (the Go Bold fallback font is embedded); the
	// CJK-capable primary font is the OFL Noto Sans SC vendored in the repo at
	// configs/fonts/ (overridable via $OVERLAY_FONT), enabling Chinese text. No
	// system-font probing — the vendored file is the single source of truth so
	// behaviour is identical on every machine. Without it, CJK overlays fail
	// loudly (no tofu) while Latin/ASCII overlays still work.
	overlayFontPath := os.Getenv("OVERLAY_FONT_PATH")
	if overlayFontPath == "" {
		overlayFontPath = "configs/fonts/NotoSansSC-Regular.otf"
	}
	overlayFonts, ferr := textoverlay.LoadFonts(overlayFontPath)
	if ferr != nil {
		log.Printf("text-overlay: font load failed, overlay_text disabled: %v", ferr)
	} else {
		overlaySvc := textoverlay.NewService(cfg.AssetDir, st, overlayFonts, id.New)
		orch.SetOverlay(overlaySvc)
		log.Printf("text-overlay: overlay_text enabled (cjk_font=%v)", overlaySvc.HasCJK())
	}
	// Vision pre-stage for platform adaptation: analyze the reference group to
	// produce a theme report injected into the AI-repaint prompt. The default
	// provider is gemini (gemini-flash-latest over the native inline API — no COS
	// upload needed); VISION_PROVIDER=openai selects the legacy image_url path
	// (which still needs COS). Both are optional; nil disables gracefully.
	var visionAnalyzer vision.Analyzer
	if strings.EqualFold(cfg.Vision.Provider, "openai") {
		base := cfg.Vision.BaseURL
		key := cfg.Vision.APIKey
		if base == "" || key == "" {
			b2, k2 := cfg.VisionCredential()
			if base == "" {
				base = b2
			}
			if key == "" {
				key = k2
			}
		}
		visionAnalyzer = vision.NewOpenAI(base, key, cfg.Vision.Model)
		if visionAnalyzer != nil {
			log.Printf("vision: openai-compatible %s analysis enabled (base=%s, needs COS)", cfg.Vision.Model, base)
		}
	} else {
		visionAnalyzer = vision.NewGemini(cfg.Vision.BaseURL, cfg.Vision.APIKey, cfg.Vision.Model)
		if visionAnalyzer != nil {
			log.Printf("vision: gemini inline %s analysis enabled (no COS required)", cfg.Vision.Model)
		}
	}
	if visionAnalyzer != nil && visionAnalyzer.Configured() {
		orch.SetVisionAnalyzer(visionAnalyzer)
	} else {
		visionAnalyzer = nil
		log.Printf("vision: credentials not configured, vision analysis disabled")
	}
	if cosUploader != nil {
		orch.SetRefPublisher(cosUploader)
	}
	// Upload-time vision prewarm: each new upload is analyzed in the background and
	// cached by raw-content md5 — the same key a later single-image adapt uses
	// (internal/agent.visionThemeReport), so the adapt hits the cache instead of
	// re-analyzing. The gemini inline analyzer needs no COS; the openai analyzer
	// publishes first. Best effort: an md5 already cached is skipped; failures only
	// log. Disabled when vision is unconfigured, or when the openai path lacks COS.
	prewarmInline := visionAnalyzer != nil && !visionAnalyzer.NeedsPublicURL()
	if visionAnalyzer != nil && (prewarmInline || cosUploader != nil) {
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
				var images []vision.Image
				if visionAnalyzer.NeedsPublicURL() {
					url, err := cosUploader.UploadIfAbsent(ctx, data, mime, st)
					if err != nil {
						log.Printf("prewarm: publish %s failed: %v", assetID, err)
						return
					}
					images = []vision.Image{{URL: url}}
				} else {
					images = []vision.Image{{Data: data, Mime: mime}}
				}
				report, err := visionAnalyzer.Analyze(ctx, images, nil)
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
	// Region description. Three modes share one closure:
	//   - POINT mode (px,py ≥ 0): the vision model inspects the FULL image + the
	//     click point and returns the clicked object's box + feature description.
	//     No crop, no COS for the box itself (gemini inline path); the OpenAI path
	//     still publishes the full image to a public URL.
	//   - POLYGON mode (len(poly) ≥ 3): mask the lassoed shape to transparent
	//     outside, crop its bbox, and describe that cutout.
	//   - RECT mode (px,py < 0, no poly): crop of [x,y,w,h] + describe the crop.
	// Wired only when vision is configured; the openai (NeedsPublicURL) path also
	// needs COS to publish the image.
	if visionAnalyzer != nil && (!visionAnalyzer.NeedsPublicURL() || cosUploader != nil) {
		wsSvc.SetDescribeRegion(func(sessionID, assetID string, x, y, bw, bh, px, py float64, poly [][2]float64) (string, float64, float64, float64, float64, error) {
			asset, err := st.GetAsset(sessionID, assetID)
			if err != nil {
				return "", 0, 0, 0, 0, err
			}
			if asset == nil {
				return "", 0, 0, 0, 0, fmt.Errorf("asset not found")
			}
			data, err := os.ReadFile(asset.Path)
			if err != nil {
				return "", 0, 0, 0, 0, fmt.Errorf("read asset: %w", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			if px >= 0 && py >= 0 {
				// POINT mode: send the FULL image + click point; the model returns the
				// object's box and description in one call.
				var img vision.Image
				if visionAnalyzer.NeedsPublicURL() {
					url, err := cosUploader.UploadIfAbsent(ctx, data, asset.Mime, st)
					if err != nil {
						return "", 0, 0, 0, 0, fmt.Errorf("publish image: %w", err)
					}
					img = vision.Image{URL: url}
				} else {
					img = vision.Image{Data: data, Mime: asset.Mime}
				}
				res, err := visionAnalyzer.LocateAndDescribe(ctx, img, px, py)
				if err != nil {
					return "", 0, 0, 0, 0, err
				}
				return res.Description, res.Box.X, res.Box.Y, res.Box.W, res.Box.H, nil
			}

			// POLYGON mode: mask the lasso shape, crop its bbox, describe the cutout.
			if len(poly) >= 3 {
				pts := make([]crop.Point, len(poly))
				var pminX, pminY = 1.0, 1.0
				var pmaxX, pmaxY = 0.0, 0.0
				for i, p := range poly {
					pts[i] = crop.Point{X: p[0], Y: p[1]}
					if p[0] < pminX {
						pminX = p[0]
					}
					if p[1] < pminY {
						pminY = p[1]
					}
					if p[0] > pmaxX {
						pmaxX = p[0]
					}
					if p[1] > pmaxY {
						pmaxY = p[1]
					}
				}
				region, err := crop.RegionPolygonBytes(data, pts)
				if err != nil {
					return "", 0, 0, 0, 0, err
				}
				var img vision.Image
				if visionAnalyzer.NeedsPublicURL() {
					url, err := cosUploader.UploadIfAbsent(ctx, region.Data, region.Mime, st)
					if err != nil {
						return "", 0, 0, 0, 0, fmt.Errorf("publish region: %w", err)
					}
					img = vision.Image{URL: url}
				} else {
					img = vision.Image{Data: region.Data, Mime: region.Mime}
				}
				desc, err := visionAnalyzer.DescribeRegion(ctx, img)
				if err != nil {
					return "", 0, 0, 0, 0, err
				}
				// Return the polygon's bounding box so the frontend can echo it.
				return desc, pminX, pminY, pmaxX - pminX, pmaxY - pminY, nil
			}

			// RECT mode: crop the box and describe the crop.
			region, err := crop.RegionBytes(data, x, y, bw, bh)
			if err != nil {
				return "", 0, 0, 0, 0, err
			}
			var img vision.Image
			if visionAnalyzer.NeedsPublicURL() {
				url, err := cosUploader.UploadIfAbsent(ctx, region.Data, region.Mime, st)
				if err != nil {
					return "", 0, 0, 0, 0, fmt.Errorf("publish region: %w", err)
				}
				img = vision.Image{URL: url}
			} else {
				img = vision.Image{Data: region.Data, Mime: region.Mime}
			}
			desc, err := visionAnalyzer.DescribeRegion(ctx, img)
			if err != nil {
				return "", 0, 0, 0, 0, err
			}
			// Echo the input rect as the box.
			return desc, x, y, bw, bh, nil
		})
		log.Printf("region description: enabled (point+polygon+rect)")
	}
	// On-demand read-only marketing analysis for the stamp-mode reference panel.
	// Same gate as describe-region (gemini inline needs no COS; openai needs it).
	// Keyed by vision.CacheKey — the SAME key the adapt pre-stage uses — so a group
	// analyzed here is reused by adaptation and vice versa (and the upload prewarm's
	// single-image reports are hit directly). Cache hit returns even when vision is
	// momentarily down; a miss analyzes live and writes back.
	if visionAnalyzer != nil && (!visionAnalyzer.NeedsPublicURL() || cosUploader != nil) {
		// visionGroupKey loads an ordered reference group's bytes (skipping any
		// unreadable one, mirroring agent.visionThemeReport) and derives its shared
		// cache key via vision.CacheKey. Returns the per-image bytes/mime so callers
		// that need to analyze don't re-read. err when nothing is readable.
		type refImg struct {
			data []byte
			mime string
			md5  string
		}
		visionGroupKey := func(sessionID string, assetIDs []string) (string, []refImg, error) {
			var imgs []refImg
			for _, id := range assetIDs {
				asset, err := st.GetAsset(sessionID, id)
				if err != nil || asset == nil {
					continue
				}
				data, err := os.ReadFile(asset.Path)
				if err != nil {
					continue
				}
				imgs = append(imgs, refImg{data: data, mime: asset.Mime, md5: fmt.Sprintf("%x", md5.Sum(data))})
			}
			if len(imgs) == 0 {
				return "", nil, fmt.Errorf("no readable references")
			}
			md5s := make([]string, len(imgs))
			for i, im := range imgs {
				md5s[i] = im.md5
			}
			return vision.CacheKey(md5s), imgs, nil
		}

		wsSvc.SetVisionReport(func(sessionID string, assetIDs []string, force bool) (string, error) {
			key, imgs, err := visionGroupKey(sessionID, assetIDs)
			if err != nil {
				return "", err
			}

			// Cache first (unless force) — requires only the store, so a hit returns
			// even when vision is currently unconfigured/down (same as adapt). The
			// "重新分析" affordance passes force=true to bypass and re-run.
			if !force {
				if cached, err := st.GetVisionReport(key); err == nil && cached != "" {
					return cached, nil
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			images := make([]vision.Image, 0, len(imgs))
			if visionAnalyzer.NeedsPublicURL() {
				for _, im := range imgs {
					url, err := cosUploader.UploadIfAbsent(ctx, im.data, im.mime, st)
					if err != nil {
						continue // skip refs that fail to publish, like the adapt path
					}
					images = append(images, vision.Image{URL: url})
				}
			} else {
				for _, im := range imgs {
					images = append(images, vision.Image{Data: im.data, Mime: im.mime})
				}
			}
			if len(images) == 0 {
				return "", fmt.Errorf("no analyzable references")
			}
			// onChunk=nil: this is a one-shot read-only report, not a streamed block.
			report, err := visionAnalyzer.Analyze(ctx, images, nil)
			if err != nil {
				return "", err
			}
			if err := st.InsertVisionReport(key, report); err != nil {
				log.Printf("vision-report: cache write failed for key %s: %v", key, err)
			}
			return report, nil
		})

		// Write-back of an edited report: persist under the SAME group key so the
		// adapt flow and later views reuse the edit (parity with the chat-flow edit
		// path that writes the edited summary into vision_reports).
		wsSvc.SetSaveVisionReport(func(sessionID string, assetIDs []string, report string) error {
			key, _, err := visionGroupKey(sessionID, assetIDs)
			if err != nil {
				return err
			}
			return st.InsertVisionReport(key, report)
		})
		log.Printf("on-demand vision report: enabled (stamp-mode reference analysis)")
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
		case "summary_confirm":
			orch.DeliverSummaryConfirm(sessionID, msg.CacheKey, msg.Summary, msg.Edited)
		case "summary_editing":
			// User entered edit mode: cancel the backend safety timeout so the gate
			// waits indefinitely for an explicit confirm rather than auto-proceeding.
			orch.DeliverSummaryEditing(sessionID, msg.CacheKey)
		case "summary_reanalyze":
			// User requested fresh grok analysis on the same reference group.
			orch.DeliverSummaryReanalyze(sessionID, msg.CacheKey)
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

// qualityCheckerAdapter bridges vision.QualityChecker to the generation package's
// local QualityChecker interface (which uses generation.QualityVerdict), keeping
// the generation package free of a vision import.
type qualityCheckerAdapter struct{ qc *vision.QualityChecker }

func (a qualityCheckerAdapter) Configured() bool { return a.qc.Configured() }

func (a qualityCheckerAdapter) Check(ctx context.Context, img []byte, mime, themeReport, specLabel string) (generation.QualityVerdict, error) {
	v, err := a.qc.Check(ctx, img, mime, themeReport, specLabel)
	return generation.QualityVerdict{
		Pass:                v.Pass,
		Total:               v.Total,
		Compliant:           v.Compliant,
		Reasons:             v.Reasons,
		Hints:               v.Hints,
		FaultSource:         v.FaultSource,
		KeyElementsFidelity: v.DimScores.KeyElementsFidelity,
	}, err
}

// pixelCheckerAdapter bridges vision.PixelChecker to generation.PixelChecker.
type pixelCheckerAdapter struct{ pc *vision.PixelChecker }

func (a pixelCheckerAdapter) Check(img []byte, mime string) (generation.PixelVerdict, error) {
	v, err := a.pc.Check(img, mime)
	return generation.PixelVerdict{Pass: v.Pass, Reasons: v.Reasons, Hints: v.Hints}, err
}

// subjectDetectorAdapter bridges vision.SubjectDetector to the generation
// package's local SubjectDetector interface (which uses generation.SubjectBox),
// keeping the generation package free of a vision import.
type subjectDetectorAdapter struct{ d *vision.SubjectDetector }

func (a subjectDetectorAdapter) Configured() bool { return a.d.Configured() }

func (a subjectDetectorAdapter) Detect(ctx context.Context, img []byte, mime string) (generation.SubjectBox, error) {
	b, err := a.d.Detect(ctx, img, mime)
	return generation.SubjectBox{CenterX: b.CenterX, CenterY: b.CenterY, Confidence: b.Confidence}, err
}

// videoQCAdapter bridges vision.QualityChecker to video.VideoQualityChecker,
// running a quality check on the video task's source image as a proxy signal.
type videoQCAdapter struct{ qc *vision.QualityChecker }

func (a videoQCAdapter) Configured() bool { return a.qc.Configured() }

func (a videoQCAdapter) CheckVideoSource(ctx context.Context, srcImg []byte, mime, motion string) (video.VideoQualitySignal, error) {
	v, err := a.qc.Check(ctx, srcImg, mime, "", "video source: "+motion)
	return video.VideoQualitySignal{
		SubjectScore: v.DimScores.SubjectConsistency,
		AppealScore:  v.DimScores.AdAppeal,
		Hints:        v.Hints,
	}, err
}

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
			// If the requested file does not exist, fall back to index.html so
			// client-side routing works.
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

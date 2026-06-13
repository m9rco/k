// Command server is the single-binary entry point for Game Asset Studio.
//
// It wires configuration, the SQLite store, and HTTP routing (including the
// embedded frontend), then serves until interrupted. Capability-specific routes
// (session, transport, generation, crop, download) are registered on the shared
// mux as those packages are implemented.
package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/download"
	"gameasset/internal/generation"
	"gameasset/internal/id"
	"gameasset/internal/session"
	"gameasset/internal/store"
	"gameasset/internal/transport"
	"gameasset/internal/video"
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

	// Image generation (gpt-image-1 primary + backup failover, async via SSE).
	gen := generation.NewFailoverGenerator(
		generation.NewHTTPProvider(cfg.ImagePrimary),
		generation.NewHTTPProvider(cfg.ImageBackup),
	)
	genSvc := generation.NewService(gen, st, broker, cfg.AssetDir, id.New)

	// Image-to-video service (happyhorse R2V; degrades when unconfigured).
	vidSvc := video.NewService(video.NewHTTPProvider(cfg.Video), st, broker, cfg.AssetDir, id.New)

	// Game-asset crawl service (pluggable source; degrades when unconfigured).
	crawlSvc := crawl.NewService(crawl.NewHTTPSource(cfg.CrawlEndpoint, cfg.CrawlAPIKey), st, broker, cfg.AssetDir, id.New)

	// Asset workspace: list assets/tasks, upload source images, partial retry.
	wsSvc := workspace.NewService(st, cfg.AssetDir, func() string { return id.New("asset") },
		func(sessionID, taskID string) error {
			return genSvc.Retry(context.Background(), sessionID, taskID)
		})
	wsSvc.RegisterRoutes(mux)

	// Download: single-asset attachment + server-side zip packaging.
	dlSvc := download.NewService(st)
	dlSvc.RegisterRoutes(mux)

	// Conversation orchestration: Eino ReAct agent over the whitelist of tools.
	orch := agent.NewOrchestrator(cfg, genSvc, cropSvc, vidSvc, crawlSvc, hub)
	hub.SetHandler(func(ctx context.Context, sessionID string, msg transport.Inbound) {
		switch msg.Type {
		case "user_message":
			text := msg.Text
			if len(msg.Refs) > 0 {
				// Multi-reference flow: surface up to 6 reference asset ids so the
				// agent can pass them as reference_asset_ids for generation.
				refs := msg.Refs
				if len(refs) > 6 {
					refs = refs[:6]
				}
				text = "[reference assets: " + strings.Join(refs, ", ") + "] " + text
			} else if msg.Ref != "" {
				// Re-adjust flow: the acted-on asset id is surfaced to the agent.
				text = "[asset " + msg.Ref + "] " + text
			}
			// Lossless compression defaults to on; an explicit false disables it.
			lossless := msg.Lossless == nil || *msg.Lossless
			if _, err := orch.Handle(ctx, sessionID, text, lossless); err != nil {
				hub.Send(sessionID, transport.Event{
					Type: transport.EventError,
					Data: map[string]string{"message": err.Error()},
				})
			}
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

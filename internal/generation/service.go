package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "image/gif"  // register decoders for dimension probing
	_ "image/jpeg" // .
	_ "image/png"  // .

	"gameasset/internal/config"
	"gameasset/internal/crop"
	"gameasset/internal/imageopt"
	applog "gameasset/internal/log"
	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// generator abstracts the failover generator for testability.
type generator interface {
	Generate(ctx context.Context, req Request) (Output, error)
}

// Service runs image-generation tasks asynchronously, publishing progress over
// SSE and persisting products as assets. Color adaptation (palette extraction)
// and prompt assembly happen here so every product is harmonized and every
// user input is sanitized.
type Service struct {
	gen      generator
	store    *store.Store
	broker   *transport.TaskBroker
	assetDir string
	now      func() time.Time
	newID    func(prefix string) string

	// announce, when set, broadcasts a task_created event over the conversation
	// channel the instant a task is created, so the workspace shows an immediate
	// placeholder without waiting for the agent turn to finish. Optional (nil in
	// tests / when no hub is wired).
	announce TaskAnnouncer
	// onAsset, when set, is called with (sessionID, assetID) when a task
	// completes successfully. Used by the orchestrator to track the last produced
	// asset so follow-up turns default to editing it.
	onAsset func(sessionID, assetID string)
	// cropper backs platform adaptation: catalog lookup (route + prompt slots) and
	// the deterministic crop fast path. Wired via SetCropper; nil disables adapt.
	cropper Cropper

	// defaultImageProvider is the primary image adapter's config, used only for
	// capability detection (e.g. transparent-background support) when assembling
	// the prompt and no per-task override is set. Wired via SetDefaultImageProvider;
	// the zero value (empty Provider) is treated as the OpenAI-compatible adapter.
	defaultImageProvider config.ImageProviderConfig

	// outpainter, when set, is the image provider used for the outpaint
	// convergence step: extreme-ratio adaptations (e.g. a 2:1 product toward a
	// 4:1 banner) are padded to the target ratio with transparent margins and
	// handed to this provider to fill the margins by extending the scene. Wired
	// via SetOutpainter; nil makes the outpaint path fall back to ModeContain
	// (band-padded), so adaptation still produces a valid product without it.
	outpainter Provider

	// params caches each task's request so a failed product can be retried
	// without the caller re-supplying inputs (short-term in-memory store, D7).
	mu     sync.Mutex
	params map[string]GenerateParams
	// cancels holds the cancel func for each in-flight task's run context, so a
	// user-initiated cancel can abort the underlying provider HTTP request and
	// stop the pipeline before it persists an orphan product.
	cancels map[string]context.CancelFunc
}

// TaskAnnouncer broadcasts a task-created notice to a session's live clients.
// kind is one of "generate" / "video" / "search" so the frontend can pick a
// placeholder; count is how many product slots to pre-render (1 for single-output
// tasks, N for a search batch that downloads N images).
type TaskAnnouncer interface {
	AnnounceTask(sessionID, taskID, kind string, count int)
}

// SetAnnouncer installs the task-created broadcaster (wired by main once the hub
// exists, avoiding an init cycle). Safe to leave unset.
func (s *Service) SetAnnouncer(a TaskAnnouncer) { s.announce = a }

// SetAssetCallback installs a callback invoked with (sessionID, assetID) when a
// generation task completes successfully. Used by the orchestrator to track the
// last-produced asset for context continuity. Safe to leave unset.
func (s *Service) SetAssetCallback(fn func(sessionID, assetID string)) { s.onAsset = fn }

// SetDefaultImageProvider records the primary image adapter's config for prompt
// capability detection (e.g. whether the adapter can produce a transparent
// background). Optional; the zero value behaves like the OpenAI-compatible
// adapter (no transparency).
func (s *Service) SetDefaultImageProvider(cfg config.ImageProviderConfig) {
	s.defaultImageProvider = cfg
}

// SetOutpainter installs the image provider used for the outpaint convergence
// step (extreme-ratio platform adaptation). Optional: leaving it unset makes the
// outpaint path fall back to ModeContain (transparent band padding), so
// adaptation degrades gracefully rather than failing.
func (s *Service) SetOutpainter(p Provider) { s.outpainter = p }

// providerSupportsTransparency reports whether an image adapter can produce a
// real transparent background. gpt-image-2 (the OpenAI-compatible default) cannot;
// Gemini image models can. Used to decide whether a 透明底 size note is injected
// verbatim or rewritten to a clean-cutout phrasing (design D4).
func providerSupportsTransparency(cfg config.ImageProviderConfig) bool {
	return strings.ToLower(strings.TrimSpace(cfg.Provider)) == "gemini"
}

// providerKind returns the adapter selection key for logging (openai/gemini/
// dashscope). Empty config reports "openai" (the default adapter). Mirrors the
// switch in NewProvider so trace logs name the same adapter that runs.
func providerKind(cfg config.ImageProviderConfig) string {
	switch k := strings.ToLower(strings.TrimSpace(cfg.Provider)); k {
	case "gemini", "dashscope":
		return k
	default:
		return "openai"
	}
}

// NewService constructs a generation service.
func NewService(gen generator, st *store.Store, broker *transport.TaskBroker, assetDir string, newID func(string) string) *Service {
	return &Service{
		gen:      gen,
		store:    st,
		broker:   broker,
		assetDir: assetDir,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
		params:   make(map[string]GenerateParams),
		cancels:  make(map[string]context.CancelFunc),
	}
}

// MaxReferenceImages bounds how many reference images one generation accepts.
// gpt-image-2 accepts a multi-image array in one edit call; 16 matches the
// product cap (anchor + up to 15 auxiliaries). Excess is truncated (design D2).
const MaxReferenceImages = 16

// GenerateParams describes one generation request initiated by the agent.
type GenerateParams struct {
	SessionID string
	Slots     Slots
	// SourceAssetID, when set, is the existing asset to edit (二次调整 / 换背景).
	// Its bytes become the generation source and its palette drives harmony.
	// Treated as the primary reference when ReferenceAssetIDs is empty.
	SourceAssetID string
	// ReferenceAssetIDs lists reference assets to reuse composition/style from,
	// up to MaxReferenceImages (excess is truncated). The first id is the primary
	// reference (drives palette, size inheritance, and parent linkage); the rest
	// are additional references. When empty, SourceAssetID is used as the sole
	// reference (backward compatible).
	ReferenceAssetIDs []string
	// Lossless enables program-side PNG lossless optimization of the product
	// before persistence. Defaults to true at the call sites.
	Lossless bool
	// Width/Height are explicit target dimensions for source-less generation
	// (text-to-image). Ignored when a source image drives size inheritance. 0
	// means let the provider decide.
	Width  int
	Height int
	// ProviderOverride, when set, makes this task use a specific provider/model
	// (the caller's per-session selection) instead of the Service default. The
	// task fixes its provider at Start, so switching models mid-flight does not
	// affect an in-progress task.
	ProviderOverride *config.ImageProviderConfig
	// --- platform adaptation (Slots.Kind == EditAdaptPlatform) ---
	// AdaptChannelID / AdaptSizeID record the target placement so the product's
	// Meta carries the same channel/size attribution as a pure crop (packaging +
	// dedup parity). AdaptWidth / AdaptHeight are the exact target platform size:
	// the provider output is converged (contain) down to them after generation,
	// same范式 as icon. All zero/empty for non-adaptation tasks.
	AdaptChannelID string
	AdaptSizeID    string
	AdaptSizeName  string
	AdaptWidth     int
	AdaptHeight    int
	// AdaptConvergeMode optionally pins the post-generation convergence mode for
	// adaptation ("contain"/"cover"). Empty lets run() auto-pick by the
	// aspect-ratio difference between the provider output and the target size.
	AdaptConvergeMode string
}

// primaryAndExtras resolves the effective reference list: the primary asset id
// (drives palette/size/parent) plus any additional reference ids, capped at
// MaxReferenceImages. Returns ("", nil) when there is no reference at all.
func (p GenerateParams) primaryAndExtras() (string, []string) {
	refs := p.ReferenceAssetIDs
	if len(refs) == 0 && p.SourceAssetID != "" {
		refs = []string{p.SourceAssetID}
	}
	if len(refs) > MaxReferenceImages {
		refs = refs[:MaxReferenceImages]
	}
	if len(refs) == 0 {
		return "", nil
	}
	return refs[0], refs[1:]
}

// Start creates a task, kicks off async generation, and returns the task id
// immediately. Progress is published to the broker; the produced asset id is
// attached to the terminal task_done event.
func (s *Service) Start(ctx context.Context, p GenerateParams) (string, error) {
	taskID := s.newID("task")
	now := s.now()
	rec := store.TaskRecord{
		ID:        taskID,
		SessionID: p.SessionID,
		Kind:      "generate",
		Status:    "queued",
		Intent:    string(p.Slots.Kind),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.InsertTask(rec); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.params[taskID] = p
	s.mu.Unlock()
	// Announce over the conversation channel first so the workspace can paint a
	// placeholder immediately, then publish the queued event on the SSE stream.
	if s.announce != nil {
		s.announce.AnnounceTask(p.SessionID, taskID, "generate", 1)
	}
	s.broker.Publish(taskID, transport.EventTaskQueued, p.SessionID, map[string]string{"intent": string(p.Slots.Kind)})

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancels[taskID] = cancel
	s.mu.Unlock()
	go s.run(runCtx, taskID, p)
	return taskID, nil
}

// Retry re-runs a previously failed task in place, reusing its cached request
// parameters. Only failed tasks owned by the session can be retried; succeeded
// products are untouched (partial-retry requirement). The task id is preserved
// so the workspace placeholder updates rather than spawning a new card.
func (s *Service) Retry(ctx context.Context, sessionID, taskID string) error {
	rec, err := s.store.GetTask(sessionID, taskID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("task %q not found", taskID)
	}
	if rec.Status != "failed" {
		return fmt.Errorf("task %q is not in a failed state (status=%s)", taskID, rec.Status)
	}
	s.mu.Lock()
	p, ok := s.params[taskID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("no cached parameters for task %q; cannot retry", taskID)
	}

	rec.Status = "queued"
	rec.Progress = 0
	rec.Error = ""
	rec.UpdatedAt = s.now()
	if err := s.store.UpdateTask(*rec); err != nil {
		return err
	}
	// Clear the prior attempt's SSE history + terminal flag so the re-run streams
	// on a fresh task stream. Otherwise the stream stays terminal (from the old
	// task_failed) and the retry's live events never reach the client — the UI
	// would freeze on the old failure even though the re-run actually executes.
	s.broker.Reset(taskID)
	s.broker.Publish(taskID, transport.EventTaskQueued, sessionID, map[string]string{"intent": string(p.Slots.Kind), "retry": "true"})

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancels[taskID] = cancel
	s.mu.Unlock()
	go s.run(runCtx, taskID, p)
	return nil
}

// genOrigin marshals the params that produced an asset so the same flow can be
// re-run on retry. ProviderOverride is dropped: it may carry credentials and is
// re-resolved at retry time by the caller (the adapt path re-forces gpt-image-2).
// Returns "" on marshal failure — the product is simply treated as non-retryable.
func genOrigin(p GenerateParams) string {
	p.ProviderOverride = nil
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// RetryAsset re-runs the AI generation flow that produced a successful product,
// reusing its persisted gen_origin parameters. The result is a NEW asset (the
// original is left untouched) — this is distinct from Retry, which re-runs a
// FAILED task in place. Only session-owned assets carrying a gen_origin (i.e. AI
// products; uploads and deterministic crops have none) can be retried. override,
// when non-nil, re-applies the request-scoped provider routing (e.g. adapt's
// gpt-image-2) that was stripped from gen_origin before persistence.
func (s *Service) RetryAsset(ctx context.Context, sessionID, assetID string, override *config.ImageProviderConfig) (string, error) {
	asset, err := s.store.GetAsset(sessionID, assetID)
	if err != nil {
		return "", err
	}
	if asset == nil {
		return "", fmt.Errorf("asset %q not found in session", assetID)
	}
	if asset.GenOrigin == "" {
		return "", fmt.Errorf("asset %q is not retryable (no generation origin)", assetID)
	}
	var p GenerateParams
	if err := json.Unmarshal([]byte(asset.GenOrigin), &p); err != nil {
		return "", fmt.Errorf("decode gen_origin for %q: %w", assetID, err)
	}
	p.SessionID = sessionID // never trust a persisted session id; scope to caller
	p.ProviderOverride = override
	return s.Start(ctx, p)
}

// Cancel aborts an in-flight task: it fires the run context's cancel (which
// interrupts the provider HTTP request and stops the pipeline before it can
// persist an orphan product) and deletes the task record. Returns the number of
// task rows removed (0 if the task was unknown or already terminal). Session
// scoping is enforced via the store delete.
func (s *Service) Cancel(sessionID, taskID string) (int64, error) {
	s.mu.Lock()
	cancel := s.cancels[taskID]
	delete(s.cancels, taskID)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.store.DeleteTask(sessionID, taskID)
}

// run executes the generation pipeline for a task.
func (s *Service) run(ctx context.Context, taskID string, p GenerateParams) {
	// Drop the cancel entry once the pipeline ends (success, failure, or abort)
	// so the map doesn't leak and a stale cancel can't fire on a reused id.
	defer func() {
		s.mu.Lock()
		delete(s.cancels, taskID)
		s.mu.Unlock()
	}()
	s.setStatus(taskID, p.SessionID, "running", transport.EventTaskRunning, 10, "", "")

	// Resolve the primary reference (drives palette/size/parent) plus extras.
	primaryID, extraIDs := p.primaryAndExtras()

	// Load primary bytes + palette for color adaptation and size inheritance.
	var srcBytes []byte
	var srcMime string
	var palette []PaletteColor
	var srcW, srcH int
	if primaryID != "" {
		asset, err := s.store.GetAsset(p.SessionID, primaryID)
		if err != nil || asset == nil {
			s.fail(taskID, p.SessionID, fmt.Sprintf("source asset not found: %v", err))
			return
		}
		srcBytes, err = os.ReadFile(asset.Path)
		if err != nil {
			s.fail(taskID, p.SessionID, fmt.Sprintf("read source: %v", err))
			return
		}
		srcMime = asset.Mime
		srcW, srcH = asset.Width, asset.Height
		if pal, err := ExtractPaletteFromBytes(srcBytes, 5); err == nil {
			palette = pal
		}
	}

	// Load any additional reference images (best-effort: a missing/unreadable
	// extra reference is skipped rather than failing the whole generation).
	var extraImages [][]byte
	for _, id := range extraIDs {
		asset, err := s.store.GetAsset(p.SessionID, id)
		if err != nil || asset == nil {
			continue
		}
		b, err := os.ReadFile(asset.Path)
		if err != nil {
			continue
		}
		extraImages = append(extraImages, b)
	}
	s.progress(taskID, p.SessionID, 30)

	// Harness inputs (design D2/D3/D4), set on the slots just before templating so
	// BuildPrompt can add the anchor-role clause for multi-image edits and rewrite
	// capability-bound size notes (e.g. 透明底) for adapters that can't honor them.
	refCount := len(extraImages)
	if primaryID != "" {
		refCount++
	}
	p.Slots.RefCount = refCount
	effectiveCfg := s.defaultImageProvider
	if p.ProviderOverride != nil {
		effectiveCfg = *p.ProviderOverride
	}
	p.Slots.ProviderSupportsTransparency = providerSupportsTransparency(effectiveCfg)

	prompt, err := BuildPrompt(p.Slots, palette)
	if err != nil {
		s.fail(taskID, p.SessionID, err.Error())
		return
	}

	// Surface the harness decisions (design D1/D2/D4) so the new behavior is
	// visible in the trace, not just the prompt length: how many references and
	// their anchor/auxiliary split, whether a 透明底 note was rewritten because the
	// adapter can't honor it, and (for adaptation) the target→generation size
	// mapping that keeps gpt-image-2 on the right proportions.
	transparencyRewritten := !p.Slots.ProviderSupportsTransparency &&
		(strings.Contains(p.Slots.SizeNote, "透明底") || strings.Contains(p.Slots.SizeNote, "透明背景"))
	harnessLog := applog.From(ctx).Info().Str("event", "gen.harness").Str("task", taskID).
		Str("kind", string(p.Slots.Kind)).
		Int("ref_count", refCount).
		Bool("multi_image_anchor", refCount >= 2).
		Str("provider", providerKind(effectiveCfg)).
		Bool("supports_transparency", p.Slots.ProviderSupportsTransparency).
		Bool("transparency_rewritten", transparencyRewritten)
	if p.Slots.Kind == EditAdaptPlatform && p.Slots.TargetWidth > 0 && p.Slots.TargetHeight > 0 {
		harnessLog = harnessLog.
			Str("target_size", fmt.Sprintf("%dx%d", p.Slots.TargetWidth, p.Slots.TargetHeight)).
			Str("gen_size", resolveGptImage2Size(p.Slots.TargetWidth, p.Slots.TargetHeight))
	}
	harnessLog.Msg("assembled low-divergence prompt")
	s.progress(taskID, p.SessionID, 45)

	// Desired output dimensions: 普通二次调整继承源图原尺寸；generate_icon 则使用目标
	// icon 尺寸（provider 端会 snap 到支持的尺寸 enum，最终再由 crop 收敛到精确尺寸）。
	wantW, wantH := srcW, srcH
	iconW, iconH := 0, 0
	if p.Slots.Kind == EditIcon {
		iconW, iconH = p.Slots.IconWidth, p.Slots.IconHeight
		if iconW <= 0 {
			iconW = DefaultIconSize
		}
		if iconH <= 0 {
			iconH = DefaultIconSize
		}
		wantW, wantH = iconW, iconH
	}
	// 平台适配：目标是平台尺寸（provider 会 snap 到支持的 enum，产物之后再由 crop
	// 收敛到精确平台尺寸，同 icon 范式）。请求里携带目标平台宽高引导构图比例。
	adaptW, adaptH := 0, 0
	if p.Slots.Kind == EditAdaptPlatform {
		adaptW, adaptH = p.Slots.TargetWidth, p.Slots.TargetHeight
		if adaptW > 0 && adaptH > 0 {
			wantW, wantH = adaptW, adaptH
		}
		// Honest "no silent caps": a target above gpt-image-2's 2K experimental
		// boundary is generated at the clamped ~2K budget and upsampled to exact
		// during convergence (design D1/D5). Surface it so the size choice isn't
		// invisible.
		if adaptW*adaptH > gptImage2ExperimentalPixels {
			applog.From(ctx).Info().Str("event", "gen.adapt_above_2k").Str("task", taskID).
				Int("target_w", adaptW).Int("target_h", adaptH).
				Msg("adapt target exceeds gpt-image-2 2K boundary; generating at clamped budget then upsampling to exact")
		}
	}
	// Source-less generation (text-to-image) has no source dimensions to inherit;
	// use the explicit target size from the request when provided.
	if wantW == 0 && wantH == 0 {
		wantW, wantH = p.Width, p.Height
	}

	genStart := time.Now()
	// Log the full prompt sent to the image model so interactions are auditable.
	// Also log the resolved generation size: for gpt-image-2 this is the legal
	// enum snapped from the target dims (e.g. 200×200 icon → 1728×1728 gen size,
	// then downsampled in the convergence step — verifying "small ≠ blurry").
	applog.From(ctx).Info().
		Str("event", "gen.provider_call").
		Str("task", taskID).
		Str("kind", string(p.Slots.Kind)).
		Int("gen_w", wantW).Int("gen_h", wantH).
		Int("refs", len(extraImages)).
		Str("prompt", prompt).
		Msg("calling provider.Generate")
	// Pick the generator: a per-session model override fixes a specific provider
	// for this task; otherwise use the Service default. Resolved here at run time
	// so the choice is fixed for the task's lifetime.
	gen := s.gen
	if p.ProviderOverride != nil {
		gen = NewProvider(*p.ProviderOverride)
	}
	out, err := gen.Generate(ctx, Request{
		Prompt:          prompt,
		SourceImage:     srcBytes,
		SourceMime:      srcMime,
		ReferenceImages: extraImages,
		Width:           wantW,
		Height:          wantH,
	})
	if err != nil {
		log.Printf("gen.run: task=%s provider.Generate FAILED after %s: %v", taskID, time.Since(genStart), err)
		applog.From(ctx).Error().Str("event", "gen.provider_failed").Str("task", taskID).Int64("duration_ms", time.Since(genStart).Milliseconds()).Err(err).Msg("provider.Generate failed")
		s.fail(taskID, p.SessionID, err.Error())
		return
	}
	log.Printf("gen.run: task=%s provider.Generate OK in %s (%d bytes)", taskID, time.Since(genStart), len(out.Data))
	applog.From(ctx).Info().Str("event", "gen.provider_ok").Str("task", taskID).Int64("duration_ms", time.Since(genStart).Milliseconds()).Int("bytes", len(out.Data)).Msg("provider.Generate ok")

	// If the task was cancelled while the provider was working, drop the result
	// instead of persisting it — otherwise a cancelled task leaves an orphan
	// asset that resurfaces on the next workspace refresh.
	if ctx.Err() != nil {
		log.Printf("gen.run: task=%s cancelled, discarding product", taskID)
		return
	}
	s.progress(taskID, p.SessionID, 80)

	// generate_icon: provider 会把尺寸 snap 到支持的 enum，产物尺寸往往大于目标
	// icon 尺寸。用 contain 收敛到精确尺寸——保留完整主体、不裁切，多余区域留白
	// （透明），保证最终落库即是请求的 icon 尺寸。
	if p.Slots.Kind == EditIcon && iconW > 0 && iconH > 0 {
		genW, genH := decodeDimensions(out.Data)
		if conv, err := crop.CropBytesWithOptions(out.Data, iconW, iconH, crop.Options{Mode: crop.ModeScale}); err != nil {
			log.Printf("gen.run: task=%s icon converge to %dx%d FAILED: %v (keeping provider output)", taskID, iconW, iconH, err)
		} else {
			out.Data = conv.Data
			applog.From(ctx).Info().Str("event", "gen.converge").Str("task", taskID).
				Str("kind", "icon").
				Int("gen_w", genW).Int("gen_h", genH).
				Int("dst_w", iconW).Int("dst_h", iconH).
				Str("mode", "scale").
				Msg("icon converged to target size")
		}
	}

	// adapt_platform: same convergence范式 as icon — the provider snaps to its
	// supported size enum, so converge the product down to the exact target
	// platform size. Mode is picked per size by convergeMode: scale (clean rescale)
	// when the provider output's aspect ratio is close to the target, outpaint
	// (AI extends the scene) when it diverges far enough that scaling would distort
	// and padding would leave large empty bands (e.g. an extreme banner). A size
	// may pin the mode via its catalog convergeMode.
	if p.Slots.Kind == EditAdaptPlatform && adaptW > 0 && adaptH > 0 {
		genW, genH := decodeDimensions(out.Data)
		mode := convergeMode(p.AdaptConvergeMode, genW, genH, adaptW, adaptH)
		if mode == crop.ModeOutpaint {
			// Outpaint: pad to the target ratio with transparent margins, let the
			// outpainter fill them, then scale to exact. Falls back to ModeContain
			// (band padding) when no outpainter is wired or the fill fails — never
			// crops the subject out.
			if data, err := s.outpaintConverge(ctx, taskID, out.Data, genW, genH, adaptW, adaptH); err != nil {
				log.Printf("gen.run: task=%s adapt outpaint to %dx%d FAILED: %v (falling back to contain)", taskID, adaptW, adaptH, err)
				if conv, cerr := crop.CropBytesWithOptions(out.Data, adaptW, adaptH, crop.Options{Mode: crop.ModeContain}); cerr == nil {
					out.Data = conv.Data
				}
			} else {
				out.Data = data
			}
		} else if conv, err := crop.CropBytesWithOptions(out.Data, adaptW, adaptH, crop.Options{Mode: mode}); err != nil {
			log.Printf("gen.run: task=%s adapt converge to %dx%d (%s) FAILED: %v (keeping provider output)", taskID, adaptW, adaptH, mode, err)
		} else {
			out.Data = conv.Data
			applog.From(ctx).Info().Str("event", "gen.converge").Str("task", taskID).
				Str("kind", "adapt").
				Int("gen_w", genW).Int("gen_h", genH).
				Int("dst_w", adaptW).Int("dst_h", adaptH).
				Str("mode", string(mode)).
				Msg("adapt converged to target size")
		}
	}

	// Persist the product.
	assetID := s.newID("asset")
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("mkdir: %v", err))
		return
	}
	path := filepath.Join(s.assetDir, assetID+".png")
	// Lossless PNG optimization before persistence (pixels unchanged).
	outData := imageopt.Optimize(out.Data, p.Lossless)
	if err := os.WriteFile(path, outData, 0o644); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("write: %v", err))
		return
	}
	// Record the produced image's actual dimensions so二次调整产物尺寸可追溯。
	outW, outH := decodeDimensions(outData)
	now := s.now()
	// Platform-adaptation products carry channel/size attribution in Meta, in the
	// same shape as crop products (crop.CropMeta), so packaging organizes both by
	// 渠道/尺寸 without distinguishing the path, and session-level dedup can match
	// (sourceAssetId, sizeId). Via=ai marks the repaint path.
	meta := ""
	if p.Slots.Kind == EditAdaptPlatform && p.AdaptSizeID != "" {
		b, _ := json.Marshal(crop.CropMeta{
			ChannelID:     p.AdaptChannelID,
			SizeID:        p.AdaptSizeID,
			SizeName:      p.AdaptSizeName,
			SourceAssetID: primaryID,
			Via:           crop.ViaAI,
		})
		meta = string(b)
	}
	if err := s.store.InsertAsset(store.AssetRecord{
		ID:        assetID,
		SessionID: p.SessionID,
		Kind:      "generated",
		Path:      path,
		Mime:      out.Mime,
		Width:     outW,
		Height:    outH,
		Provider:  out.Provider,
		ParentID:  primaryID,
		Meta:      meta,
		GenOrigin: genOrigin(p),
		CreatedAt: now,
	}); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("persist: %v", err))
		return
	}

	// Terminal success: attach the produced asset id.
	t := store.TaskRecord{ID: taskID, SessionID: p.SessionID, Status: "done", Progress: 100, AssetID: assetID, UpdatedAt: now}
	_ = s.store.UpdateTask(t)
	s.broker.Publish(taskID, transport.EventTaskDone, p.SessionID, map[string]string{
		"assetId":  assetID,
		"provider": out.Provider,
	})
	if s.onAsset != nil {
		s.onAsset(p.SessionID, assetID)
	}
	log.Printf("gen.run: task=%s DONE asset=%s published task_done", taskID, assetID)
	applog.From(ctx).Info().Str("event", "gen.done").Str("task", taskID).Str("asset", assetID).Msg("generation done, published task_done")
}

func (s *Service) setStatus(taskID, sessionID, status string, ev transport.EventType, progress int, assetID, errMsg string) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: status, Progress: progress, AssetID: assetID, Error: errMsg, UpdatedAt: now})
	s.broker.Publish(taskID, ev, sessionID, map[string]any{"progress": progress})
}

func (s *Service) progress(taskID, sessionID string, pct int) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: "running", Progress: pct, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskProgress, sessionID, map[string]int{"progress": pct})
}

func (s *Service) fail(taskID, sessionID, msg string) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: "failed", Error: msg, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskFailed, sessionID, map[string]string{"error": msg})
}

// outpaintConverge converges an extreme-ratio adaptation product to the exact
// target size by handing the UNPADDED high-res master to the outpainter, which
// extends the scene outward to the new ratio, and cover-cropping that render to
// dstW×dstH. The outpainter's output is used directly: a prior version composited
// the master back over the center to keep brand pixels pristine, but the
// mechanical seam between the locked center and the AI-filled margins (style /
// perspective mismatch, "double image") looked worse than a single coherent
// render. So the anti-drift load now lives entirely in buildOutpaintPrompt, which
// frames the job as a preserve-everything outpaint rather than a regeneration.
//
// We do NOT pad with transparent margins first: the outpainter is a prompt-driven
// editing model, not a mask inpainter, and treats a transparent region as a
// black/empty band to keep rather than a fill mask (the observed "black sides"
// bug). The caller falls back to band padding when no outpainter is wired — never
// a sliced subject or a distorted stretch.
func (s *Service) outpaintConverge(ctx context.Context, taskID string, data []byte, genW, genH, dstW, dstH int) ([]byte, error) {
	if s.outpainter == nil {
		return nil, fmt.Errorf("no outpainter wired")
	}
	start := time.Now()
	out, err := s.outpainter.Generate(ctx, Request{
		Prompt:      buildOutpaintPrompt(genW, genH, dstW, dstH),
		SourceImage: data,
		SourceMime:  "image/png",
		Width:       dstW,
		Height:      dstH,
	})
	if err != nil {
		return nil, fmt.Errorf("outpainter generate: %w", err)
	}
	fillW, fillH := decodeDimensions(out.Data)
	// Cover-crop the outpainter render to the exact target (it snaps to its own
	// size enum, so it rarely lands on dstW×dstH precisely). Center anchor keeps
	// the preserved subject — which the prompt requires to stay centered — in frame.
	conv, cerr := crop.CropBytesWithOptions(out.Data, dstW, dstH, crop.Options{Mode: crop.ModeCover})
	if cerr != nil {
		return nil, fmt.Errorf("cover-crop outpaint render to %dx%d: %w", dstW, dstH, cerr)
	}
	applog.From(ctx).Info().Str("event", "gen.converge").Str("task", taskID).
		Str("kind", "adapt").
		Int("gen_w", genW).Int("gen_h", genH).
		Int("fill_w", fillW).Int("fill_h", fillH).
		Int("dst_w", dstW).Int("dst_h", dstH).
		Str("mode", "outpaint_direct").
		Str("outpainter", s.outpainter.Name()).
		Int64("fill_ms", time.Since(start).Milliseconds()).
		Msg("adapt converged to target size via outpaint")
	return conv.Data, nil
}

// decodeDimensions reads an image's pixel dimensions; returns (0,0) if the
// bytes cannot be decoded, leaving width/height unset rather than failing.
func decodeDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

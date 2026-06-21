package agent

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gameasset/internal/config"
	"gameasset/internal/copywriting"
	"gameasset/internal/cos"
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	applog "gameasset/internal/log"
	"gameasset/internal/store"
	"gameasset/internal/textoverlay"
	"gameasset/internal/video"
	"gameasset/internal/vision"
	"gameasset/internal/websearch"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ToolDeps carries the concrete services that back the agent tools. The agent
// layer keeps a thin adapter over these so the framework stays isolated
// (design D1: thin wrapper around Eino).
type ToolDeps struct {
	Generation *generation.Service
	// TextToImage is a generation.Service wired with the text-to-image provider
	// (wan/qwen). It may be nil when text-to-image is not configured, in which
	// case the generate_image_from_text tool is left out of the whitelist.
	TextToImage *generation.Service
	Crop        *crop.Service
	Video       *video.Service
	Crawl       *crawl.Service
	// WebSearch backs the web_search and search_images tools. Always available
	// (no API key needed); nil only in tests that don't exercise search.
	WebSearch *websearch.Service
	// Copywriting backs the generate_copy tool (structured marketing copy). Nil
	// leaves generate_copy out of the whitelist (the agent declines copy
	// requests), so wiring it is opt-in like text-to-image.
	Copywriting *copywriting.Service
	// Overlay backs the overlay_text tool (deterministic text/LOGO compositing).
	// Nil/unconfigured leaves overlay_text out of the whitelist.
	Overlay *textoverlay.Service
	// Store is used by await_result polling to read task completion status.
	Store *store.Store
	// SessionID scopes every tool call to the caller's session so produced
	// tasks and assets stay isolated per session.
	SessionID string
	// Lossless toggles program-side PNG lossless optimization of image products
	// (default true; set per request from the frontend compression switch).
	Lossless bool
	// ImageOverride / TextToImageOverride / VideoOverride carry the session's
	// per-scene model selection. Nil means "use the service's default provider".
	// They are resolved per turn from the usermodel manager.
	ImageOverride       *config.ImageProviderConfig
	TextToImageOverride *config.ImageProviderConfig
	VideoOverride       *config.ImageProviderConfig
	// AdaptModelOverride, when non-nil, is used by adapt_to_platform's AI
	// repaint path instead of ImageOverride. Request-scoped: only this one
	// tool uses it; edit_image and other image tools keep using ImageOverride.
	// Nil falls back to ImageOverride (or service default). Injected by the
	// orchestrator to route AI platform adaptation to gpt-image-2 (falling back
	// to gemini-3-pro-image when gpt-image-2 is unavailable).
	AdaptModelOverride *config.ImageProviderConfig
	// Clarify, when set, is invoked by the clarify_intent tool to surface a
	// structured clarifying question (capsule) to the user. Injected by the
	// orchestrator so tools.go stays free of the transport layer (design D1).
	Clarify CapsuleEmitter
	// RefPublisher, when non-nil, uploads source images to COS (md5-deduped) so
	// the vision analyzer can read them by public URL. Nil disables the
	// publish → analyze pre-stage (adapt falls back to no theme report).
	RefPublisher *cos.Uploader
	// VisionAnalyzer, when non-nil and Configured, runs marketing analysis to
	// produce a theme report injected into the AI-repaint prompt. The default
	// (gemini) reads images inline (no COS); the openai variant needs published
	// URLs. Nil/unconfigured disables analysis (graceful).
	VisionAnalyzer vision.Analyzer
	// Notify, when set, pushes a chat-visible stage message: done=false for
	// streaming chunks (analysis report), done=true to finalize a message.
	// Injected by the orchestrator so tools.go stays transport-free.
	Notify func(text string, done bool)
	// NotifyAnalysis sends vision analysis chunks to the collapsible analysis
	// panel (distinct from regular assistant bubbles). Nil falls back to Notify.
	NotifyAnalysis func(text string, done bool)
	// AwaitSummaryConfirm gates adapt_to_platform between producing a live analysis
	// report and starting AI repaint: it emits a "summary_confirm" signal to the
	// frontend (so the editable analysis panel starts its 3s countdown) keyed by
	// cacheKey, then blocks until the user confirms/edits, the countdown expires,
	// a server safety timeout elapses, or the user triggers a reanalysis. The
	// reanalyze func (when non-nil) is called when the gate receives a
	// "summary_reanalyze" signal — it streams fresh grok output and returns the
	// new report; the gate updates its current report and re-arms the frontend.
	// Returns the final summary and whether the user edited it. Nil disables the
	// gate (adaptation proceeds immediately with the original report).
	AwaitSummaryConfirm func(ctx context.Context, cacheKey, original string, reanalyze func(context.Context) (string, error)) (final string, edited bool)
	// dedup guards against the model emitting the SAME async-task tool call twice
	// in one turn (parallel tool_calls), which would otherwise start two
	// duplicate tasks and concatenate two identical acknowledgments into one
	// bubble. Scoped per turn (one ToolDeps is built per Handle), nil-safe.
	dedup *turnCallGuard
}

// turnCallGuard records which (tool, args) signatures have already executed in
// the current turn so a duplicate call can be short-circuited. Concurrency-safe
// because the ReAct framework may dispatch parallel tool calls on separate
// goroutines.
type turnCallGuard struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newTurnCallGuard() *turnCallGuard {
	return &turnCallGuard{seen: make(map[string]struct{})}
}

// firstSeen atomically records sig and reports whether this is its first
// occurrence this turn. A nil guard always reports true (no dedup).
func (g *turnCallGuard) firstSeen(sig string) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.seen[sig]; ok {
		return false
	}
	g.seen[sig] = struct{}{}
	return true
}

// statusDuplicate marks a result produced by a duplicate same-turn tool call
// that was suppressed (no task started). The friendly marshalers map it to an
// empty acknowledgment so no second bubble appears.
const statusDuplicate = "duplicate"

// statusClarified marks a result produced when an edit tool could not proceed
// because a required description was missing, so it surfaced a clarify capsule to
// the user instead of starting a doomed task. Like statusDuplicate it maps to an
// empty acknowledgment (the capsule itself is the user-facing output).
const statusClarified = "clarified"

// argSig builds a stable signature for a tool's args struct so two identical
// same-turn calls collapse to one. It marshals to JSON, then drops fields that
// don't change WHAT the call produces (only how its result is delivered), so a
// model that emits the same generation twice — differing only in such a hint —
// is still deduped. Today that means await_result: it toggles wait-and-return
// vs fire-and-forget for chaining, but both start the exact same task, so two
// calls differing only there are a duplicate, not a distinct request. Calls that
// differ in any SEMANTIC arg get distinct signatures and both run (e.g. two
// different motions / intents in one turn — the intended batch case).
func argSig(a any) string {
	b, err := json.Marshal(a)
	if err != nil {
		return fmt.Sprintf("%#v", a)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return string(b) // non-object args: sign the raw JSON
	}
	delete(m, "await_result")     // delivery hint, not part of the produced artifact
	canon, err := json.Marshal(m) // map keys marshal in sorted order: deterministic
	if err != nil {
		return string(b)
	}
	return string(canon)
}

// ClarifyOption is one selectable answer to a clarify_intent question. Label is
// shown on the chip; Value is what gets fed back to the agent on click;
// EditableHint pre-fills an editable input the user can rewrite before sending.
type ClarifyOption struct {
	Label        string `json:"label" jsonschema:"description=Short label shown on the option chip"`
	Value        string `json:"value" jsonschema:"description=The value fed back to the agent when the user picks this option"`
	EditableHint string `json:"editable_hint,omitempty" jsonschema:"description=Optional text pre-filled into an editable input so the user can rewrite this option before sending"`
}

// CapsuleEmitter surfaces a structured clarifying question to the user.
type CapsuleEmitter func(question string, options []ClarifyOption)

// awaitTask polls the store until taskID is done/failed (up to 120 s). Returns
// the final TaskRecord so callers can extract asset_id.
func (d ToolDeps) awaitTask(ctx context.Context, taskID string) (*store.TaskRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			rec, err := d.Store.GetTask(d.SessionID, taskID)
			if err != nil {
				return nil, err
			}
			if rec == nil {
				continue
			}
			if rec.Status == "done" || rec.Status == "failed" {
				return rec, nil
			}
		}
	}
}

// asyncMarshal returns a MarshalOutput that either:
//   - Serialises the result as JSON when the result's json "asset_id" field is
//     non-empty (await_result=true path — model needs to read the asset id).
//   - Returns a short Chinese instruction string otherwise (standalone path —
//     the model should just acknowledge to the user and stop).
//
// This replaces the old ToolReturnDirectly mechanism for generation tools, so
// they can participate in multi-task chains when await_result=true while still
// behaving sensibly for standalone calls.
func asyncMarshal(standaloneFriendly string) utils.MarshalOutput {
	return func(_ context.Context, v any) (string, error) {
		b, _ := json.Marshal(v)
		var probe struct {
			AssetID string `json:"asset_id"`
			Status  string `json:"status"`
		}
		if json.Unmarshal(b, &probe) == nil {
			if probe.Status == statusDuplicate || probe.Status == statusClarified {
				return "", nil // duplicate / clarify-capsule: no second bubble
			}
			if probe.AssetID != "" {
				return string(b), nil // await path: give full JSON back to model
			}
		}
		return standaloneFriendly, nil
	}
}

// --- change_character / change_background / change_text -------------------

type editArgs struct {
	// Intent is one of change_character, add_character, change_background, change_text.
	Intent string `json:"intent" jsonschema:"description=The edit intent. change_character REPLACES the existing main character with a new one; add_character ADDS a new character while KEEPING the existing subject(s) (use this when the user says 增加/添加/多加一个角色/在旁边加一个人); change_background; change_text,enum=change_character,enum=add_character,enum=change_background,enum=change_text"`
	// SourceAssetID is the existing asset to edit. Empty for a fresh generation.
	SourceAssetID string `json:"source_asset_id,omitempty" jsonschema:"description=The base image to EDIT ON TOP OF (被编辑底图). Set this when the user says '把X放进图Z' or '在图Z基础上修改' — Z is the source. Leave EMPTY when generating a brand-new image purely from references."`
	// ReferenceAssetIDs lists up to 16 reference assets to reuse composition/style
	// from. The first is the primary (anchor) reference. Takes precedence over
	// source_asset_id when provided.
	ReferenceAssetIDs []string `json:"reference_asset_ids,omitempty" jsonschema:"description=IDs of up to 16 assets used as REFERENCES (参照物 for style/character/composition). First is the anchor (primary). For '根据图X图Y生成新图' put X and Y here and leave source_asset_id empty; for '把图X放进图Z' put X here and Z in source_asset_id."`
	// CharacterDesc/BackgroundDesc/TextContent carry the per-intent payload.
	CharacterDesc  string `json:"character_desc,omitempty" jsonschema:"description=Description of the character (the NEW one to replace with for change_character, or the one to ADD for add_character)"`
	BackgroundDesc string `json:"background_desc,omitempty" jsonschema:"description=Description of the new background (for change_background)"`
	TextContent    string `json:"text_content,omitempty" jsonschema:"description=The new copy/text to render (for change_text)"`
	// RegionDesc carries a description of the specific region/subject the user
	// selected in the preview (produced by the region-description stage). When set,
	// the edit is scoped to that subject and the rest of the frame is preserved.
	RegionDesc string `json:"region_desc,omitempty" jsonschema:"description=Optional. Description of the SPECIFIC region/subject the user selected to edit (e.g. '画面左侧的红甲战士'). When present the edit is scoped to that subject and the rest of the image is kept unchanged. Leave empty for whole-image edits."`
	// ReuseComposition preserves the source/reference image composition.
	ReuseComposition bool `json:"reuse_composition,omitempty" jsonschema:"description=Reuse the reference image composition and base elements"`
	// AwaitResult when true blocks until the task finishes and returns asset_id,
	// enabling multi-task chaining (e.g. edit_image → image_to_video in one turn).
	AwaitResult bool `json:"await_result,omitempty" jsonschema:"description=Set true when you need the produced asset_id immediately to pass to the next tool (multi-task chain). The tool waits up to 120s for completion."`
}

type editResult struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Note    string `json:"note,omitempty"`
	AssetID string `json:"asset_id,omitempty"` // populated when await_result=true and done
}

// editMissingDesc reports whether an edit call lacks the description its intent
// requires, and if so returns a clarify question + concrete editable options to
// surface to the user. This keeps a missing description from reaching the
// generation pipeline (which would fail with "description required") and turns it
// into a friendly capsule the user can answer in one click/edit. Returns
// (false, "", nil) when the description is present.
func editMissingDesc(a editArgs) (bool, string, []ClarifyOption) {
	switch generation.EditKind(a.Intent) {
	case generation.EditCharacter, generation.EditCharacterAdd:
		if strings.TrimSpace(a.CharacterDesc) == "" {
			verb := "替换成的"
			if generation.EditKind(a.Intent) == generation.EditCharacterAdd {
				verb = "新增的"
			}
			return true, "好的，要" + verb + "角色长什么样？给我点描述，或选一个再改：", []ClarifyOption{
				{Label: "动漫少女", Value: "动漫风格少女，明亮大眼，精致五官", EditableHint: "动漫风格少女，……"},
				{Label: "写实男性", Value: "写实风格男性，硬朗轮廓，自然光影", EditableHint: "写实风格男性，……"},
				{Label: "Q版角色", Value: "Q版卡通角色，大头比例，可爱风格", EditableHint: "Q版卡通角色，……"},
			}
		}
	case generation.EditBackground:
		if strings.TrimSpace(a.BackgroundDesc) == "" {
			return true, "好的，背景想换成什么风格？给我点描述，或选一个再改：", []ClarifyOption{
				{Label: "中国风", Value: "中国风，水墨意境，亭台楼阁，远山云雾，淡雅色调", EditableHint: "中国风，……"},
				{Label: "赛博朋克", Value: "赛博朋克，霓虹灯，未来都市夜景，雨夜街道", EditableHint: "赛博朋克，……"},
				{Label: "简约纯色", Value: "简约纯色背景，柔和渐变，干净留白", EditableHint: "简约……色背景"},
				{Label: "自然风光", Value: "自然风光，蓝天白云，开阔草地，明亮通透", EditableHint: "自然风光，……"},
			}
		}
	case generation.EditText:
		if strings.TrimSpace(a.TextContent) == "" {
			return true, "好的，文案要换成什么内容？告诉我要显示的文字：", []ClarifyOption{
				{Label: "输入文案", Value: "把文案换成：", EditableHint: "把文案换成：……"},
			}
		}
	}
	return false, "", nil
}

func (d ToolDeps) newEditTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"edit_image",
		"换背景/换角色/增加角色/换文案：对已有图片进行编辑（换背景、替换或新增角色/主体、替换文案），自动做颜色适配。"+
			"触发词：换背景/改背景/换角色/换人物/增加角色/添加角色/多加一个人/换文案/改文案/替换/调整图片/二次修改。"+
			"换角色与增加角色不同：intent=change_character 是把原角色替换掉，intent=add_character 是保留原角色再新增一个。"+
			"Use source_asset_id to adjust an already-generated image. "+
			"Returns a task id; progress streams over SSE and the result lands in the workspace. "+
			"Set await_result=true only when you must chain this result into the next tool call.",
		func(ctx context.Context, a editArgs) (editResult, error) {
			if !d.dedup.firstSeen("edit_image|" + argSig(a)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "edit_image").Str("intent", a.Intent).Str("source", a.SourceAssetID).Msg("duplicate same-turn call suppressed")
				return editResult{Status: statusDuplicate}, nil
			}
			kind := generation.EditKind(a.Intent)
			switch kind {
			case generation.EditCharacter, generation.EditCharacterAdd, generation.EditBackground, generation.EditText:
			default:
				return editResult{}, fmt.Errorf("unsupported intent %q", a.Intent)
			}
			// Validate the required per-intent description BEFORE starting an async
			// task. A missing description makes the generation pipeline fail later
			// with "description required" — and because edit_image is
			// ToolReturnDirectly, returning a Go error here would abort the turn with
			// an empty reply (the user just sees errors, retry reuses the same empty
			// args). Instead surface a clarify capsule so the user can pick/type the
			// missing detail, and return a benign result (no doomed task, no crash).
			if missing, question, opts := editMissingDesc(a); missing {
				if d.Clarify != nil {
					d.Clarify(question, opts)
				}
				applog.From(ctx).Warn().Str("event", "tool.missing_param").Str("tool", "edit_image").Str("intent", a.Intent).Msg("missing description, surfaced clarify capsule")
				return editResult{Status: statusClarified}, nil
			}
			taskID, err := d.Generation.Start(ctx, generation.GenerateParams{
				SessionID:         d.SessionID,
				SourceAssetID:     a.SourceAssetID,
				ReferenceAssetIDs: a.ReferenceAssetIDs,
				Lossless:          d.Lossless,
				ProviderOverride:  d.ImageOverride,
				Slots: generation.Slots{
					Kind:             kind,
					CharacterDesc:    a.CharacterDesc,
					BackgroundDesc:   a.BackgroundDesc,
					TextContent:      a.TextContent,
					RegionDesc:       a.RegionDesc,
					ReuseComposition: a.ReuseComposition,
				},
			})
			if err != nil {
				return editResult{}, err
			}
			if a.AwaitResult && d.Store != nil {
				rec, err := d.awaitTask(ctx, taskID)
				if err != nil {
					return editResult{}, fmt.Errorf("await edit_image: %w", err)
				}
				if rec.Status == "failed" {
					return editResult{}, fmt.Errorf("edit_image failed: %s", rec.Error)
				}
				return editResult{TaskID: taskID, Status: "done", AssetID: rec.AssetID}, nil
			}
			return editResult{TaskID: taskID, Status: "queued"}, nil
		},
		utils.WithMarshalOutput(asyncMarshal("好的，正在按你的要求处理这张图，产物会很快出现在左侧工作区。")),
	)
}

// --- generate_icon ----------------------------------------------------------

type iconArgs struct {
	// SourceAssetID is the existing image the icon is derived from. Required.
	SourceAssetID string `json:"source_asset_id" jsonschema:"description=ID of the workspace image to derive an icon FROM (从这张图提炼图标)。"`
	// Desc is an optional style hint for the icon (e.g. 扁平描边 / 圆角拟物).
	Desc string `json:"desc,omitempty" jsonschema:"description=Optional style hint for the icon, e.g. 扁平 / 描边 / 圆角拟物。Leave empty to let the model choose."`
	// Width/Height set the target icon size. Both default to 150 when omitted.
	Width  int `json:"width,omitempty" jsonschema:"description=Target icon width in px (default 150 when omitted)。"`
	Height int `json:"height,omitempty" jsonschema:"description=Target icon height in px (default 150 when omitted)。"`
}

func (d ToolDeps) newIconTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"generate_icon",
		"生成图标/生成 icon：从工作区图片的主体生成独立 app/游戏图标。"+
			"触发词：生成icon/做图标/生成图标/app icon/应用图标。"+
			"This calls the image-generation model (not a pure crop), then converges the product to the "+
			"exact requested icon size (default 150x150). Returns a task id; progress streams over SSE and "+
			"the icon lands in the workspace.",
		func(ctx context.Context, a iconArgs) (editResult, error) {
			if a.SourceAssetID == "" {
				return editResult{}, fmt.Errorf("generate_icon requires source_asset_id")
			}
			if !d.dedup.firstSeen("generate_icon|" + argSig(a)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "generate_icon").Str("source", a.SourceAssetID).Msg("duplicate same-turn call suppressed")
				return editResult{Status: statusDuplicate}, nil
			}
			taskID, err := d.Generation.Start(ctx, generation.GenerateParams{
				SessionID:        d.SessionID,
				SourceAssetID:    a.SourceAssetID,
				Lossless:         d.Lossless,
				ProviderOverride: d.ImageOverride,
				Slots: generation.Slots{
					Kind:       generation.EditIcon,
					IconDesc:   a.Desc,
					IconWidth:  a.Width,
					IconHeight: a.Height,
				},
			})
			if err != nil {
				return editResult{}, err
			}
			return editResult{TaskID: taskID, Status: "queued"}, nil
		},
		utils.WithMarshalOutput(friendlyMarshal("好的，正在为这张图生成图标，完成后会出现在左侧工作区。")),
	)
}

// --- generate_image_from_text ----------------------------------------------

type textToImageArgs struct {
	// Desc is the scene/content description for the brand-new image.
	Desc string `json:"desc" jsonschema:"description=纯文本画面描述：要生成的图片内容/风格/主体。无需任何源图。"`
	// Width/Height are optional target dimensions (provider snaps to its enum).
	Width  int `json:"width,omitempty" jsonschema:"description=Optional target width in px (0 = provider default)。"`
	Height int `json:"height,omitempty" jsonschema:"description=Optional target height in px (0 = provider default)。"`
	// AwaitResult blocks until done and returns asset_id for multi-task chaining.
	AwaitResult bool `json:"await_result,omitempty" jsonschema:"description=Set true to wait for completion and get asset_id (for chaining into the next tool)."`
}

func (d ToolDeps) newTextToImageTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"generate_image_from_text",
		"文生图：根据纯文字描述生成一张全新图片，不需要任何底图或参照图。"+
			"触发词：画一张/生成一张/来一张/创作一张/生成图片……且没有提供任何图片。"+
			"Returns a task id; progress streams over SSE and the result lands in the workspace. "+
			"Set await_result=true only when chaining into the next tool.",
		func(ctx context.Context, a textToImageArgs) (editResult, error) {
			if !d.dedup.firstSeen("generate_image_from_text|" + argSig(a)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "generate_image_from_text").Msg("duplicate same-turn call suppressed")
				return editResult{Status: statusDuplicate}, nil
			}
			taskID, err := d.TextToImage.Start(ctx, generation.GenerateParams{
				SessionID:        d.SessionID,
				Lossless:         d.Lossless,
				ProviderOverride: d.TextToImageOverride,
				Slots: generation.Slots{
					Kind:            generation.EditTextToImage,
					TextToImageDesc: a.Desc,
				},
				Width:  a.Width,
				Height: a.Height,
			})
			if err != nil {
				return editResult{}, err
			}
			if a.AwaitResult && d.Store != nil {
				rec, err := d.awaitTask(ctx, taskID)
				if err != nil {
					return editResult{}, fmt.Errorf("await generate_image_from_text: %w", err)
				}
				if rec.Status == "failed" {
					return editResult{}, fmt.Errorf("generate_image_from_text failed: %s", rec.Error)
				}
				return editResult{TaskID: taskID, Status: "done", AssetID: rec.AssetID}, nil
			}
			return editResult{TaskID: taskID, Status: "queued"}, nil
		},
		utils.WithMarshalOutput(asyncMarshal("好的，正在按你的描述生成图片，产物会很快出现在左侧工作区。")),
	)
}

// --- crop_to_sizes ---------------------------------------------------------

type cropArgs struct {
	SourceAssetID string   `json:"source_asset_id" jsonschema:"description=ID of the workspace asset to crop"`
	SizeIDs       []string `json:"size_ids" jsonschema:"description=Unique size ids to produce (e.g. taptap.icon.512). List valid ids via list_platform_sizes."`
	// Mode selects the crop strategy. cover (default) fills then center-crops;
	// contain fits the whole image with padding (no cropping); anchor crops
	// toward a nine-grid position. rect (manual region) is not exposed here.
	Mode string `json:"mode,omitempty" jsonschema:"description=Crop strategy: cover (default fill+center-crop) | contain (fit whole image with padding no crop) | anchor (crop toward a position),enum=cover,enum=contain,enum=anchor"`
	// Anchor names the crop position when mode=anchor.
	Anchor string `json:"anchor,omitempty" jsonschema:"description=Crop position when mode=anchor: one of top-left,top,top-right,left,center,right,bottom-left,bottom,bottom-right"`
}

type cropResultItem struct {
	AssetID string `json:"asset_id"`
	SizeID  string `json:"size_id"`
	Channel string `json:"channel"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

func (d ToolDeps) newCropTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"crop_to_sizes",
		"切尺寸/按平台裁剪：将工作区图片裁剪/缩放到一个或多个平台广告位尺寸，纯图像处理，不经过 AI。"+
			"触发词：切尺寸/裁剪/按平台/按渠道/广告位/输出尺寸/各种尺寸。"+
			"Supports crop modes: cover (default), contain (fit whole image, pad, no crop), "+
			"anchor (crop toward a nine-grid position). Each produced size becomes a new workspace asset. "+
			"Unknown ids and non-producible sizes are rejected.",
		func(_ context.Context, a cropArgs) ([]cropResultItem, error) {
			opts := crop.Options{Mode: crop.Mode(a.Mode), Anchor: crop.Anchor(a.Anchor)}
			results, err := d.Crop.CropToSizes(d.SessionID, a.SourceAssetID, a.SizeIDs, d.Lossless, opts)
			if err != nil {
				return nil, err
			}
			out := make([]cropResultItem, 0, len(results))
			for _, r := range results {
				out = append(out, cropResultItem{AssetID: r.AssetID, SizeID: r.SizeID, Channel: r.ChannelID, Width: r.Width, Height: r.Height})
			}
			return out, nil
		},
	)
}

// --- list_platform_sizes ---------------------------------------------------

type sizeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Orientation string `json:"orientation"`
	Format      string `json:"format,omitempty"`
	MaxKB       int    `json:"max_kb,omitempty"`
	Note        string `json:"note,omitempty"`
	Producible  bool   `json:"producible"`
}

type assetTypeGroup struct {
	Type  string     `json:"type"`
	Name  string     `json:"name"`
	Sizes []sizeInfo `json:"sizes"`
}

type channelGroup struct {
	Channel    string           `json:"channel"`
	Name       string           `json:"name"`
	Group      string           `json:"group"`
	AssetTypes []assetTypeGroup `json:"asset_types"`
}

type listSizesArgs struct {
	Channel string `json:"channel" jsonschema:"description=Optional channel id to filter by (e.g. taptap). Omit to list every channel."`
}

func (d ToolDeps) newListSizesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"list_platform_sizes",
		"List available crop size presets as channel → asset type → size, each size carrying a unique id and optional "+
			"constraint hints (format/max_kb/note). Pass an optional channel id to list just that channel. "+
			"Use before crop_to_sizes to pick valid size ids.",
		func(_ context.Context, a listSizesArgs) ([]channelGroup, error) {
			groups := make([]channelGroup, 0)
			for _, ch := range d.Crop.Channels() {
				if a.Channel != "" && ch.ID != a.Channel {
					continue
				}
				cg := channelGroup{Channel: ch.ID, Name: ch.Name, Group: ch.Group}
				for _, at := range ch.AssetTypes {
					atg := assetTypeGroup{Type: at.Type, Name: at.Name}
					for _, s := range at.Sizes {
						atg.Sizes = append(atg.Sizes, sizeInfo{
							ID: s.ID, Name: s.Name, Width: s.Width, Height: s.Height,
							Orientation: s.Orientation, Format: s.Format, MaxKB: s.MaxKB,
							Note: s.Note, Producible: s.Producible,
						})
					}
					cg.AssetTypes = append(cg.AssetTypes, atg)
				}
				groups = append(groups, cg)
			}
			return groups, nil
		},
	)
}

// --- adapt_to_platform -------------------------------------------------------

type adaptArgs struct {
	SourceAssetID     string   `json:"source_asset_id,omitempty" jsonschema:"description=ID of a single workspace image to adapt. For a multi-image reference group use reference_asset_ids instead."`
	ReferenceAssetIDs []string `json:"reference_asset_ids,omitempty" jsonschema:"description=Ordered reference group (up to 16) to adapt as ONE bundle. First id is the anchor (内容/主体真相源), the rest are auxiliary style/element references. Each target size yields exactly one image referencing the whole group (product count = size count). Use this for the [reference assets: ...] multi-select case."`
	SizeIDs           []string `json:"size_ids" jsonschema:"description=Unique platform size ids to adapt to (e.g. taptap.banner.1120x280). Use list_platform_sizes first."`
}

type adaptOutcomeItem struct {
	SizeID  string `json:"size_id"`
	Via     string `json:"via"`
	AssetID string `json:"asset_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
}

// adaptResult wraps the per-size outcomes plus a status so asyncMarshal can
// suppress a duplicate same-turn call (like the other async tools).
type adaptResult struct {
	Status   string             `json:"status,omitempty"`
	Outcomes []adaptOutcomeItem `json:"outcomes,omitempty"`
}

// adaptProvider returns the image provider config for adapt_to_platform's AI
// repaint path: AdaptModelOverride when set, else ImageOverride (session
// selection). Nil means the generation service uses its default provider.
func adaptProvider(d ToolDeps) *config.ImageProviderConfig {
	if d.AdaptModelOverride != nil {
		return d.AdaptModelOverride
	}
	return d.ImageOverride
}

// refImg is one reference image's bytes + metadata, loaded for the vision
// pre-stage. md5 keys the report cache (and de-dups COS uploads on the openai path).
type refImg struct {
	data []byte
	mime string
	md5  string
}

// buildVisionImages turns loaded references into analyzer inputs. When needsURL
// is true (openai image_url path) each image is published to COS (md5-deduped)
// and carried as a URL; references that fail to publish are skipped. When false
// (gemini inline path) the raw bytes are carried inline with no COS dependency.
func buildVisionImages(ctx context.Context, d ToolDeps, imgs []refImg, needsURL bool) ([]vision.Image, error) {
	out := make([]vision.Image, 0, len(imgs))
	if !needsURL {
		for _, im := range imgs {
			out = append(out, vision.Image{Data: im.data, Mime: im.mime})
		}
		return out, nil
	}
	if d.RefPublisher == nil {
		return nil, fmt.Errorf("ref publisher not configured")
	}
	for _, im := range imgs {
		url, err := d.RefPublisher.UploadIfAbsent(ctx, im.data, im.mime, d.Store)
		if err != nil {
			applog.From(ctx).Warn().Str("event", "adapt.publish_failed").Err(err).Msg("ref publish failed; skipping that reference")
			continue
		}
		out = append(out, vision.Image{URL: url})
	}
	return out, nil
}

// visionThemeReport runs the (publish →) analyze pre-stage for platform adaptation
// and returns the marketing theme report to anchor the AI repaint. It analyzes the
// WHOLE ordered reference group (anchor first), so multi-image adaptations feed
// every reference to the vision model. Returns "" — and the caller proceeds with the
// standard harness — whenever vision is unconfigured, no reference can be
// read, or any step fails. Stage progress is surfaced to chat: upload/error via
// d.Notify, analysis chunks via d.NotifyAnalysis (collapsible panel), falling back
// to d.Notify when NotifyAnalysis is nil.
//
// Cache key: a single image keys on its raw-content md5 (so an upload-time prewarm
// and a later single-image adapt share the same cached report); a group of 2+ keys
// on a composite fingerprint of the ordered per-image md5s.
func visionThemeReport(ctx context.Context, d ToolDeps, refIDs []string) string {
	if d.Store == nil {
		return ""
	}
	// Load each reference's bytes in order; skip any that can't be read rather
	// than failing the whole pre-stage (a missing auxiliary ref must not block).
	var imgs []refImg
	for _, id := range refIDs {
		if id == "" {
			continue
		}
		asset, err := d.Store.GetAsset(d.SessionID, id)
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
		return ""
	}

	notifyAnalysis := d.NotifyAnalysis
	if notifyAnalysis == nil {
		notifyAnalysis = d.Notify
	}

	// Group cache key: raw md5 for a single image (aligns with upload prewarm so a
	// pre-warmed single-image report is reused here too); composite of ordered
	// per-image md5s for a reference group of 2+.
	cacheKey := imgs[0].md5
	if len(imgs) > 1 {
		parts := make([]string, len(imgs))
		for i, im := range imgs {
			parts[i] = im.md5
		}
		cacheKey = fmt.Sprintf("%x", md5.Sum([]byte("group:"+strings.Join(parts, ","))))
	}
	// reanalyzeFn is passed to the gate so the user can re-run analysis on the same
	// reference group without re-uploading. The default (gemini) analyzer reads
	// images inline (no COS); the openai analyzer needs published URLs (md5-deduped
	// UploadIfAbsent). Built before the cache check so it works for both paths.
	var reanalyzeFn func(context.Context) (string, error)
	if d.VisionAnalyzer != nil && d.VisionAnalyzer.Configured() {
		inlineOK := !d.VisionAnalyzer.NeedsPublicURL()
		if inlineOK || d.RefPublisher != nil {
			capturedImgs := imgs
			notifyAn := notifyAnalysis
			reanalyzeFn = func(ctx context.Context) (string, error) {
				images, err := buildVisionImages(ctx, d, capturedImgs, d.VisionAnalyzer.NeedsPublicURL())
				if err != nil || len(images) == 0 {
					return "", fmt.Errorf("no analyzable references for reanalysis")
				}
				return d.VisionAnalyzer.Analyze(ctx, images, func(chunk string) {
					if notifyAn != nil {
						notifyAn(chunk, false)
					}
				})
			}
		}
	}

	// Check cache first — this path requires only the store, not COS/vision. A
	// report written by the upload prewarm (or a previous adapt) is returned even
	// when COS/vision are currently unconfigured (e.g. credentials rotated). A cache
	// hit still opens the editable confirmation window (the user may want to tweak a
	// previously-analyzed/edited summary before this adaptation); an edit there is
	// written back to the same key, so the next reuse picks up the latest version.
	if cached, err := d.Store.GetVisionReport(cacheKey); err == nil && cached != "" {
		applog.From(ctx).Info().Str("event", "adapt.analysis_cache_hit").Str("key", cacheKey).Int("refs", len(imgs)).Msg("vision report cache hit")
		if notifyAnalysis != nil {
			notifyAnalysis(cached, true)
		}
		return gateSummaryConfirm(ctx, d, cacheKey, cached, reanalyzeFn)
	}

	// No cached report — need live analysis. The analyzer must be configured; the
	// inline (gemini) path needs no COS, the openai path needs a publisher.
	if d.VisionAnalyzer == nil || !d.VisionAnalyzer.Configured() {
		return ""
	}
	needsURL := d.VisionAnalyzer.NeedsPublicURL()
	if needsURL && d.RefPublisher == nil {
		return ""
	}

	if d.Notify != nil {
		d.Notify("⏫ 正在分析参考图的宣发要素，请稍候…\n\n", false)
	}
	// Build analyzer inputs: inline bytes for the gemini path, or published URLs
	// (md5-deduped) for the openai path.
	images, err := buildVisionImages(ctx, d, imgs, needsURL)
	if err != nil || len(images) == 0 {
		if d.Notify != nil {
			d.Notify("（参考图发布暂不可用，按默认适配）", true)
		}
		return ""
	}
	report, err := d.VisionAnalyzer.Analyze(ctx, images, func(chunk string) {
		if notifyAnalysis != nil {
			notifyAnalysis(chunk, false)
		}
	})
	if err != nil {
		applog.From(ctx).Warn().Str("event", "adapt.analysis_failed").Err(err).Msg("vision analysis failed; skipping theme report")
		if d.Notify != nil {
			d.Notify("（主题分析暂不可用，按默认适配）", true)
		}
		return ""
	}
	if notifyAnalysis != nil {
		notifyAnalysis("", true)
	}
	if err := d.Store.InsertVisionReport(cacheKey, report); err != nil {
		applog.From(ctx).Warn().Str("event", "adapt.analysis_cache_miss").Err(err).Msg("failed to cache vision report")
	}
	applog.From(ctx).Info().Str("event", "adapt.analysis_ok").Str("key", cacheKey).Int("refs", len(imgs)).Int("report_len", len(report)).Msg("vision theme report produced and cached")

	// Gate before AI repaint: give the user a chance to edit the summary in the
	// frontend's confirmation window (shared with the cache-hit path).
	return gateSummaryConfirm(ctx, d, cacheKey, report, reanalyzeFn)
}

// gateSummaryConfirm runs the editable-summary confirmation gate shared by both
// the live-analysis and cache-hit paths. It blocks adapt_to_platform between
// having a theme report and starting AI repaint so the user can edit the summary
// in the frontend's confirmation window. reanalyze (when non-nil) is passed
// through so the gate can re-run grok if the user clicks "重新分析". It returns
// the final theme text to anchor the adaptation. When the user edits the
// summary, the edited text is written back to cacheKey's vision_reports entry.
// When no gate hook is injected (tests / transport-less), returns report unchanged.
func gateSummaryConfirm(ctx context.Context, d ToolDeps, cacheKey, report string, reanalyze func(context.Context) (string, error)) string {
	if d.AwaitSummaryConfirm == nil {
		return report
	}
	final, edited := d.AwaitSummaryConfirm(ctx, cacheKey, report, reanalyze)
	final = strings.TrimSpace(final)
	if final == "" {
		final = report
		edited = false
	}
	if edited && final != report {
		// User edited the summary: overwrite the cached report so this image
		// group's later adaptations/edits reuse the edited version.
		if err := d.Store.InsertVisionReport(cacheKey, final); err != nil {
			applog.From(ctx).Warn().Str("event", "adapt.analysis_edit_cache_miss").Err(err).Msg("failed to write back edited vision report")
		} else {
			applog.From(ctx).Info().Str("event", "adapt.analysis_edited").Str("key", cacheKey).Int("report_len", len(final)).Msg("edited vision report cached (overwrote prior report)")
		}
	}
	return final
}

func (d ToolDeps) newAdaptTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"adapt_to_platform",
		"平台适配：把工作区图片适配到一个或多个平台广告位尺寸，在保留主体与核心宣发意图的前提下重新组织构图。"+
			"触发词：切尺寸/裁剪/适配尺寸/按某平台/各平台/广告位/按渠道出图/宣发图适配。"+
			"智能路由：宽高比一致时走确定性裁剪快路径（免费即时）；横竖翻转或比例差异大时调用图生图模型补全画面而非裁切主体。"+
			"同一会话内相同源图+尺寸只请求一次（后续复用产物）。"+
			"Use list_platform_sizes first to get valid size_ids. "+
			"Fast-path sizes return asset_id immediately; AI-repaint sizes return task_id (progress over SSE).",
		func(ctx context.Context, a adaptArgs) (adaptResult, error) {
			// Build the ordered reference group: explicit reference_asset_ids take
			// precedence (anchor first); fall back to source_asset_id for the
			// single-image call. The whole group is analyzed and fed to the model.
			refs := a.ReferenceAssetIDs
			if len(refs) == 0 && a.SourceAssetID != "" {
				refs = []string{a.SourceAssetID}
			} else if a.SourceAssetID != "" && (len(refs) == 0 || refs[0] != a.SourceAssetID) {
				// An explicit source that isn't already the anchor leads the group.
				refs = append([]string{a.SourceAssetID}, refs...)
			}
			if len(refs) == 0 {
				return adaptResult{}, fmt.Errorf("adapt requires a source or reference asset")
			}
			dedupKey := "adapt_to_platform|" + strings.Join(refs, ",") + "|" + strings.Join(a.SizeIDs, ",")
			if !d.dedup.firstSeen(dedupKey) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "adapt_to_platform").Str("anchor", refs[0]).Msg("duplicate same-turn call suppressed")
				return adaptResult{Status: statusDuplicate}, nil
			}
			// Vision pre-stage (proposal add-vision-guided-adaptation, extended by
			// strengthen-reference-driven-adaptation): publish the WHOLE reference
			// group to COS (md5-deduped) → analyze its marketing elements → inject
			// the resulting theme report into the AI-repaint prompt so each adapted
			// size stays anchored to the analyzed subject/intent. Gracefully skipped
			// (themeReport stays "") when COS/vision are unconfigured or any step
			// fails — adaptation still runs with the standard harness.
			themeReport := visionThemeReport(ctx, d, refs)
			outcomes, err := d.Generation.AdaptToPlatform(ctx, d.SessionID, refs, a.SizeIDs, d.Lossless, adaptProvider(d), themeReport)
			if err != nil {
				return adaptResult{}, err
			}
			items := make([]adaptOutcomeItem, 0, len(outcomes))
			for _, o := range outcomes {
				items = append(items, adaptOutcomeItem{SizeID: o.SizeID, Via: o.Via, AssetID: o.AssetID, TaskID: o.TaskID})
			}
			return adaptResult{Outcomes: items}, nil
		},
		utils.WithMarshalOutput(asyncMarshal("好的，正在为你适配各平台尺寸，产物会陆续出现在左侧工作区。")),
	)
}

// --- image_to_video --------------------------------------------------------

type videoArgs struct {
	SourceAssetID string `json:"source_asset_id" jsonschema:"description=ID of the workspace image asset to animate into a video"`
	Motion        string `json:"motion" jsonschema:"description=Describe the desired motion (e.g. 让角色走起来 / 镜头缓慢推进)"`
	// AwaitResult blocks until done and returns asset_id for multi-task chaining.
	AwaitResult bool `json:"await_result,omitempty" jsonschema:"description=Set true to wait for completion and get asset_id (for chaining into the next tool)."`
}

type videoResult struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	AssetID string `json:"asset_id,omitempty"`
}

func (d ToolDeps) newVideoTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"image_to_video",
		"生视频/让图片动起来：基于一张工作区图片和动作描述生成短视频。"+
			"触发词：生视频/生成视频/让图动起来/动起来/加动作/制作视频/图转视频。"+
			"Returns a task id; progress streams over SSE and the video lands in the workspace. "+
			"Set await_result=true only when chaining this result into the next tool.",
		func(ctx context.Context, a videoArgs) (videoResult, error) {
			if d.Video == nil || !d.Video.Configured() {
				return videoResult{}, fmt.Errorf("图生视频暂未配置，暂不可用")
			}
			// Suppress a duplicate same-turn call (model sometimes emits two
			// parallel image_to_video calls), which would start two tasks and
			// concatenate two identical acks into one bubble.
			if !d.dedup.firstSeen(fmt.Sprintf("image_to_video|%s|%s", a.SourceAssetID, a.Motion)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "image_to_video").Str("source", a.SourceAssetID).Msg("duplicate same-turn call suppressed")
				return videoResult{Status: statusDuplicate}, nil
			}
			taskID, err := d.Video.Start(ctx, video.Params{
				SessionID:        d.SessionID,
				SourceAssetID:    a.SourceAssetID,
				Motion:           a.Motion,
				ProviderOverride: d.VideoOverride,
			})
			if err != nil {
				return videoResult{}, err
			}
			if a.AwaitResult && d.Store != nil {
				rec, err := d.awaitTask(ctx, taskID)
				if err != nil {
					return videoResult{}, fmt.Errorf("await image_to_video: %w", err)
				}
				if rec.Status == "failed" {
					return videoResult{}, fmt.Errorf("image_to_video failed: %s", rec.Error)
				}
				return videoResult{TaskID: taskID, Status: "done", AssetID: rec.AssetID}, nil
			}
			return videoResult{TaskID: taskID, Status: "queued"}, nil
		},
		utils.WithMarshalOutput(asyncMarshal("好的，正在把这张图生成视频，完成后会出现在左侧工作区。")),
	)
}

// --- web_search (T2) --------------------------------------------------------

type webSearchArgs struct {
	Query string `json:"query" jsonschema:"description=搜索关键词，用于联网查找相关信息"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=返回条数（默认5，最多10）"`
}

type webSearchResult struct {
	Results []webSearchItem `json:"results"`
	Total   int             `json:"total"`
}

type webSearchItem struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func (d ToolDeps) newWebSearchTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"web_search",
		"联网搜索：通过 DuckDuckGo 搜索互联网内容，返回摘要与链接列表。"+
			"触发词：搜索/查一下/查找/网上找/搜一下/帮我查/了解一下。"+
			"Use when the user asks to look up information online.",
		func(ctx context.Context, a webSearchArgs) (webSearchResult, error) {
			if d.WebSearch == nil {
				return webSearchResult{}, fmt.Errorf("联网搜索暂不可用")
			}
			limit := a.Limit
			if limit <= 0 {
				limit = 5
			}
			if limit > 10 {
				limit = 10
			}
			results, err := d.WebSearch.SearchText(ctx, a.Query, limit)
			if err != nil {
				return webSearchResult{}, fmt.Errorf("web search: %w", err)
			}
			items := make([]webSearchItem, 0, len(results))
			for _, r := range results {
				items = append(items, webSearchItem{Title: r.Title, URL: r.URL, Snippet: r.Snippet})
			}
			return webSearchResult{Results: items, Total: len(items)}, nil
		},
	)
}

// --- search_images (T3) -----------------------------------------------------

type searchImagesArgs struct {
	Query       string `json:"query" jsonschema:"description=中文图片搜索关键词（搜狗使用），如「王者荣耀 海报」"`
	QueryEN     string `json:"query_en,omitempty" jsonschema:"description=英文图片搜索关键词（Bing 使用），由你翻译 query 得到，如「Honor of Kings poster」。品牌/专有名词用其通用英文名。强烈建议同时提供以扩大优质结果池。"`
	Limit       int    `json:"limit,omitempty" jsonschema:"description=根据用户语义推断：「找一张/找1张」=1，「找几张/找两三张」=3，「找一些/多找点」=6，表达模糊或未提数量=6，最多12"`
	AwaitResult bool   `json:"await_result,omitempty" jsonschema:"description=Set true to wait for download completion and get task result (for chaining into the next tool)."`
}

type searchImagesResult struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	AssetID string `json:"asset_id,omitempty"` // first downloaded asset when await_result=true
}

func (d ToolDeps) newSearchImagesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"search_images",
		"搜索图片：双源并行（搜狗中文词 + Bing 英文词）搜索高质量图片，自动去重筛选下载到工作区。"+
			"触发词：搜索图片/找一张/搜一张/查找图片/帮我找图/图片搜索/下载图片/配图。"+
			"请同时提供 query（中文）和 query_en（你翻译的英文）以获得最佳结果。"+
			"Returns a task id; progress streams over SSE and images land in the workspace. "+
			"Set await_result=true only when chaining the first downloaded image into the next tool.",
		func(ctx context.Context, a searchImagesArgs) (searchImagesResult, error) {
			if d.WebSearch == nil {
				return searchImagesResult{}, fmt.Errorf("图片搜索暂不可用")
			}
			taskID, err := d.WebSearch.StartImageSearch(ctx, websearch.ImageSearchParams{
				SessionID: d.SessionID,
				Query:     a.Query,
				QueryEN:   a.QueryEN,
				Limit:     a.Limit,
			})
			if err != nil {
				return searchImagesResult{}, err
			}
			if a.AwaitResult && d.Store != nil {
				rec, err := d.awaitTask(ctx, taskID)
				if err != nil {
					return searchImagesResult{}, fmt.Errorf("await search_images: %w", err)
				}
				if rec.Status == "failed" {
					return searchImagesResult{}, fmt.Errorf("search_images failed: %s", rec.Error)
				}
				return searchImagesResult{TaskID: taskID, Status: "done", AssetID: rec.AssetID}, nil
			}
			return searchImagesResult{TaskID: taskID, Status: "queued"}, nil
		},
		// Standalone path returns an EMPTY string: search_images is ToolReturnDirectly,
		// so a non-empty canned phrase here would be appended to the model's own
		// in-turn confirmation, surfacing the same sentence twice. The empty result
		// honors the system-prompt contract ("工具返回空内容表示任务已提交") and leaves
		// the single model-streamed confirmation as the only acknowledgment. The
		// await_result path still returns full JSON so the asset_id can be chained.
		utils.WithMarshalOutput(asyncMarshal("")),
	)
}

// --- crawl_game_assets (kept for reference, NOT registered in Tools()) ------

type crawlArgs struct {
	Game  string `json:"game" jsonschema:"description=Game name to crawl marketing/asset image previews for"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Max number of previews to fetch (default 8, max 20)"`
}

type crawlResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (d ToolDeps) newCrawlTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"crawl_game_assets",
		"Crawl image-asset previews for a game by name (legacy, use search_images instead).",
		func(ctx context.Context, a crawlArgs) (crawlResult, error) {
			if d.Crawl == nil || !d.Crawl.Configured() {
				return crawlResult{}, fmt.Errorf("物料爬取暂未配置，暂不可用")
			}
			taskID, err := d.Crawl.Start(ctx, crawl.Params{
				SessionID: d.SessionID,
				Game:      a.Game,
				Limit:     a.Limit,
			})
			if err != nil {
				return crawlResult{}, err
			}
			return crawlResult{TaskID: taskID, Status: "queued", Note: "Crawl started; watch task progress."}, nil
		},
		utils.WithMarshalOutput(friendlyMarshal("好的，正在抓取该游戏的素材预览，结果会出现在左侧工作区。")),
	)
}

// --- clarify_intent ---------------------------------------------------------

type clarifyOptionArg struct {
	Label        string `json:"label" jsonschema:"description=Short label shown on the option chip"`
	Value        string `json:"value" jsonschema:"description=Value fed back to the agent when the user picks this option"`
	EditableHint string `json:"editable_hint,omitempty" jsonschema:"description=Optional text pre-filled into an editable input so the user can rewrite before sending"`
}

type clarifyArgs struct {
	Question string             `json:"question" jsonschema:"description=A short question asking the user for the missing information"`
	Options  []clarifyOptionArg `json:"options" jsonschema:"description=2 to 4 concrete options the user can pick or edit"`
}

type clarifyResult struct {
	Status string `json:"status"`
}

func (d ToolDeps) newClarifyTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"clarify_intent",
		"Ask the user a single structured clarifying question when the request hits a "+
			"supported capability but is missing a KEY parameter required to safely call an "+
			"execution tool (which image, what to change it to, target platform). "+
			"Do NOT clarify when the answer can be reasonably inferred from context. "+
			"Provide 2-4 concrete options. Do NOT call execution tools in the same turn.",
		func(_ context.Context, a clarifyArgs) (clarifyResult, error) {
			if d.Clarify != nil {
				opts := make([]ClarifyOption, 0, len(a.Options))
				for _, o := range a.Options {
					opts = append(opts, ClarifyOption{Label: o.Label, Value: o.Value, EditableHint: o.EditableHint})
				}
				d.Clarify(a.Question, opts)
			}
			return clarifyResult{Status: "asked"}, nil
		},
		utils.WithMarshalOutput(friendlyMarshal("")),
	)
}

// friendlyMarshal returns a MarshalOutput that emits a fixed user-facing Chinese
// sentence regardless of the raw result struct. Used for ToolReturnDirectly async
// tools so the chat shows a clean confirmation instead of raw {task_id,...} JSON.
// A duplicate-status result (suppressed same-turn re-call) yields an empty string
// so no second confirmation bubble appears.
func friendlyMarshal(msg string) utils.MarshalOutput {
	return func(_ context.Context, v any) (string, error) {
		b, _ := json.Marshal(v)
		var probe struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(b, &probe) == nil && probe.Status == statusDuplicate {
			return "", nil
		}
		return msg, nil
	}
}

// Tools builds the full whitelist of agent tools for this session.
func (d ToolDeps) Tools() ([]tool.BaseTool, error) {
	edit, err := d.newEditTool()
	if err != nil {
		return nil, fmt.Errorf("edit tool: %w", err)
	}
	cropTool, err := d.newCropTool()
	if err != nil {
		return nil, fmt.Errorf("crop tool: %w", err)
	}
	listSizes, err := d.newListSizesTool()
	if err != nil {
		return nil, fmt.Errorf("list sizes tool: %w", err)
	}
	clarify, err := d.newClarifyTool()
	if err != nil {
		return nil, fmt.Errorf("clarify tool: %w", err)
	}
	// icon, err := d.newIconTool()
	// if err != nil {
	// 	return nil, fmt.Errorf("icon tool: %w", err)
	// }
	tools := []tool.BaseTool{edit, cropTool, listSizes, clarify}
	adaptTool, err := d.newAdaptTool()
	if err != nil {
		return nil, fmt.Errorf("adapt tool: %w", err)
	}
	tools = append(tools, adaptTool)
	if d.TextToImage != nil {
		t2i, err := d.newTextToImageTool()
		if err != nil {
			return nil, fmt.Errorf("text-to-image tool: %w", err)
		}
		tools = append(tools, t2i)
	}
	if d.Video != nil && d.Video.Configured() {
		vid, err := d.newVideoTool()
		if err != nil {
			return nil, fmt.Errorf("video tool: %w", err)
		}
		tools = append(tools, vid)
	}
	// web_search is always available (no API key needed).
	if d.WebSearch != nil {
		wsearch, err := d.newWebSearchTool()
		if err != nil {
			return nil, fmt.Errorf("web search tool: %w", err)
		}
		wsImg, err := d.newSearchImagesTool()
		if err != nil {
			return nil, fmt.Errorf("search images tool: %w", err)
		}
		tools = append(tools, wsearch, wsImg)
	}
	// generate_copy is opt-in: only whitelisted when a copywriting service is wired.
	if d.Copywriting != nil && d.Copywriting.Configured() {
		copyTool, err := d.newCopywritingTool()
		if err != nil {
			return nil, fmt.Errorf("copywriting tool: %w", err)
		}
		tools = append(tools, copyTool)
	}
	// overlay_text is opt-in: only whitelisted when the overlay service is configured.
	if d.Overlay != nil && d.Overlay.Configured() {
		overlayTool, err := d.newOverlayTool()
		if err != nil {
			return nil, fmt.Errorf("overlay tool: %w", err)
		}
		tools = append(tools, overlayTool)
	}
	// generate_variants reuses the generation pipeline; whitelisted whenever
	// generation is wired (it always is, mirroring edit_image's availability).
	if d.Generation != nil {
		variantsTool, err := d.newVariantsTool()
		if err != nil {
			return nil, fmt.Errorf("variants tool: %w", err)
		}
		tools = append(tools, variantsTool)
	}
	// crawl_game_assets removed from whitelist (replaced by search_images).
	return tools, nil
}

// AsyncTaskTools are tools whose results go directly to the user (not back to
// the model), preventing the model from generating fabricated result descriptions
// before async tasks actually complete.
func AsyncTaskTools() map[string]struct{} {
	return map[string]struct{}{
		"edit_image":               {},
		"generate_icon":            {},
		"generate_image_from_text": {},
		"adapt_to_platform":        {},
		"image_to_video":           {},
		"search_images":            {},
		"generate_variants":        {},
		// clarify_intent ends the turn; result must not feed back to model.
		"clarify_intent": {},
	}
}

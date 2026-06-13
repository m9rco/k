package agent

import (
	"context"
	"fmt"
	"log"

	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	"gameasset/internal/video"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ToolDeps carries the concrete services that back the agent tools. The agent
// layer keeps a thin adapter over these so the framework stays isolated
// (design D1: thin wrapper around Eino).
type ToolDeps struct {
	Generation *generation.Service
	Crop       *crop.Service
	Video      *video.Service
	Crawl      *crawl.Service
	// SessionID scopes every tool call to the caller's session so produced
	// tasks and assets stay isolated per session.
	SessionID string
	// Lossless toggles program-side PNG lossless optimization of image products
	// (default true; set per request from the frontend compression switch).
	Lossless bool
	// Clarify, when set, is invoked by the clarify_intent tool to surface a
	// structured clarifying question (capsule) to the user. Injected by the
	// orchestrator so tools.go stays free of the transport layer (design D1).
	Clarify CapsuleEmitter
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

// --- change_character / change_background / change_text -------------------

type editArgs struct {
	// Intent is one of change_character, change_background, change_text.
	Intent string `json:"intent" jsonschema:"description=The edit intent: change_character, change_background, or change_text,enum=change_character,enum=change_background,enum=change_text"`
	// SourceAssetID is the existing asset to edit. Empty for a fresh generation.
	SourceAssetID string `json:"source_asset_id,omitempty" jsonschema:"description=The base image to EDIT ON TOP OF (被编辑底图). Set this when the user says '把X放进图Z' or '在图Z基础上修改' — Z is the source. Leave EMPTY when generating a brand-new image purely from references."`
	// ReferenceAssetIDs lists up to 6 reference assets to reuse composition/style
	// from. The first is the primary reference. Takes precedence over
	// source_asset_id when provided.
	ReferenceAssetIDs []string `json:"reference_asset_ids,omitempty" jsonschema:"description=IDs of up to 6 assets used as REFERENCES (参照物 for style/character/composition). First is primary. For '根据图X图Y生成新图' put X and Y here and leave source_asset_id empty; for '把图X放进图Z' put X here and Z in source_asset_id."`
	// CharacterDesc/BackgroundDesc/TextContent carry the per-intent payload.
	CharacterDesc  string `json:"character_desc,omitempty" jsonschema:"description=Description of the new character (for change_character)"`
	BackgroundDesc string `json:"background_desc,omitempty" jsonschema:"description=Description of the new background (for change_background)"`
	TextContent    string `json:"text_content,omitempty" jsonschema:"description=The new copy/text to render (for change_text)"`
	// ReuseComposition preserves the source/reference image composition.
	ReuseComposition bool `json:"reuse_composition,omitempty" jsonschema:"description=Reuse the reference image composition and base elements"`
}

type editResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (d ToolDeps) newEditTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"edit_image",
		"Generate or edit a marketing asset image by changing the character, background, or text/copy. "+
			"Performs color harmonization automatically. Use source_asset_id to adjust an already-generated image. "+
			"Returns a task id; progress streams over SSE and the result lands in the workspace.",
		func(ctx context.Context, a editArgs) (editResult, error) {
			log.Printf("edit_image: invoked intent=%q source=%q refs=%v", a.Intent, a.SourceAssetID, a.ReferenceAssetIDs)
			kind := generation.EditKind(a.Intent)
			switch kind {
			case generation.EditCharacter, generation.EditBackground, generation.EditText:
			default:
				return editResult{}, fmt.Errorf("unsupported intent %q", a.Intent)
			}
			taskID, err := d.Generation.Start(ctx, generation.GenerateParams{
				SessionID:         d.SessionID,
				SourceAssetID:     a.SourceAssetID,
				ReferenceAssetIDs: a.ReferenceAssetIDs,
				Lossless:          d.Lossless,
				Slots: generation.Slots{
					Kind:             kind,
					CharacterDesc:    a.CharacterDesc,
					BackgroundDesc:   a.BackgroundDesc,
					TextContent:      a.TextContent,
					ReuseComposition: a.ReuseComposition,
				},
			})
			if err != nil {
				log.Printf("edit_image: Start error: %v", err)
				return editResult{}, err
			}
			log.Printf("edit_image: started task=%s", taskID)
			return editResult{TaskID: taskID, Status: "queued", Note: "Generation started; watch task progress."}, nil
		},
		utils.WithMarshalOutput(friendlyMarshal("好的，正在按你的要求处理这张图，产物会很快出现在右侧工作区。")),
	)
}

// --- crop_to_sizes ---------------------------------------------------------

type cropArgs struct {
	SourceAssetID string   `json:"source_asset_id" jsonschema:"description=ID of the workspace asset to crop"`
	SizeIDs       []string `json:"size_ids" jsonschema:"description=Unique size ids to produce (e.g. taptap.icon.512). List valid ids via list_platform_sizes."`
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
		"Crop/resize an existing asset to one or more platform size presets, addressed by their unique size id. "+
			"Pure image processing, no AI. Each produced size becomes a new workspace asset. "+
			"Unknown ids and non-producible sizes (e.g. video specs) are rejected with an error.",
		func(_ context.Context, a cropArgs) ([]cropResultItem, error) {
			results, err := d.Crop.CropToSizes(d.SessionID, a.SourceAssetID, a.SizeIDs, d.Lossless)
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
	Channel string `json:"channel" jsonschema:"description=Optional channel id to filter by (e.g. taptap). Omit to list every channel. Use this to avoid pulling the whole catalog into context."`
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

// --- image_to_video --------------------------------------------------------

type videoArgs struct {
	SourceAssetID string `json:"source_asset_id" jsonschema:"description=ID of the workspace image asset to animate into a video"`
	Motion        string `json:"motion" jsonschema:"description=Describe the desired motion (e.g. 让角色走起来 / 镜头缓慢推进)"`
}

type videoResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (d ToolDeps) newVideoTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"image_to_video",
		"Generate a short video from a single workspace image plus a motion description "+
			"(e.g. make the character walk). Returns a task id; progress streams over SSE and "+
			"the video lands in the workspace. Use only when the user asks to animate/【让图动起来】.",
		func(ctx context.Context, a videoArgs) (videoResult, error) {
			if d.Video == nil || !d.Video.Configured() {
				return videoResult{}, fmt.Errorf("图生视频暂未配置，暂不可用")
			}
			taskID, err := d.Video.Start(ctx, video.Params{
				SessionID:     d.SessionID,
				SourceAssetID: a.SourceAssetID,
				Motion:        a.Motion,
			})
			if err != nil {
				return videoResult{}, err
			}
			return videoResult{TaskID: taskID, Status: "queued", Note: "Video generation started; watch task progress."}, nil
		},
		utils.WithMarshalOutput(friendlyMarshal("好的，正在把这张图生成视频，完成后会出现在右侧工作区。")),
	)
}

// --- crawl_game_assets ------------------------------------------------------

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
		"Crawl image-asset previews for a game by name and add them to the workspace as "+
			"reference material (info retrieval only, not for redistribution). Returns a task id; "+
			"progress streams over SSE. Use when the user asks to fetch/crawl a game's materials.",
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
		utils.WithMarshalOutput(friendlyMarshal("好的，正在抓取该游戏的素材预览，结果会出现在右侧工作区。")),
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
			"supported capability but is missing information required to safely call an "+
			"execution tool (e.g. which image, what to change it to, target size/platform). "+
			"Provide 2-4 concrete options the user can click or edit. Do NOT guess or call "+
			"execution tools in the same turn; this ends the turn and waits for the user's reply.",
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
func friendlyMarshal(msg string) utils.MarshalOutput {
	return func(_ context.Context, _ any) (string, error) { return msg, nil }
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
	tools := []tool.BaseTool{edit, cropTool, listSizes, clarify}
	// image_to_video is only exposed when a video provider is configured, so the
	// agent doesn't advertise a capability that will always fail.
	if d.Video != nil && d.Video.Configured() {
		vid, err := d.newVideoTool()
		if err != nil {
			return nil, fmt.Errorf("video tool: %w", err)
		}
		tools = append(tools, vid)
	}
	// crawl_game_assets is only exposed when a crawl source is configured.
	if d.Crawl != nil && d.Crawl.Configured() {
		cr, err := d.newCrawlTool()
		if err != nil {
			return nil, fmt.Errorf("crawl tool: %w", err)
		}
		tools = append(tools, cr)
	}
	return tools, nil
}

// AsyncTaskTools are the fire-and-forget tools: each only STARTS an async task
// (returning a task id) and its progress is tracked out-of-band over SSE. These
// must return directly to the user after one call — feeding their {status:queued}
// result back into the model makes a small model think the work is unfinished
// and re-invoke the tool forever (a生图 loop). Wired as react ToolReturnDirectly.
func AsyncTaskTools() map[string]struct{} {
	return map[string]struct{}{
		"edit_image":        {},
		"image_to_video":    {},
		"crawl_game_assets": {},
		// clarify_intent ends the turn after asking; its result must not be fed
		// back to the model (there's nothing more to do until the user replies).
		"clarify_intent": {},
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gameasset/internal/config"
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	"gameasset/internal/store"
	"gameasset/internal/video"
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
		}
		if json.Unmarshal(b, &probe) == nil && probe.AssetID != "" {
			return string(b), nil // await path: give full JSON back to model
		}
		return standaloneFriendly, nil
	}
}

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

func (d ToolDeps) newEditTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"edit_image",
		"换背景/换角色/换文案：对已有图片进行编辑（换背景、换角色/主体、替换文案），自动做颜色适配。"+
			"触发词：换背景/改背景/换角色/换人物/换文案/改文案/替换/调整图片/二次修改。"+
			"Use source_asset_id to adjust an already-generated image. "+
			"Returns a task id; progress streams over SSE and the result lands in the workspace. "+
			"Set await_result=true only when you must chain this result into the next tool call.",
		func(ctx context.Context, a editArgs) (editResult, error) {
			log.Printf("edit_image: invoked intent=%q source=%q refs=%v await=%v", a.Intent, a.SourceAssetID, a.ReferenceAssetIDs, a.AwaitResult)
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
				ProviderOverride:  d.ImageOverride,
				Slots: generation.Slots{
					Kind:             kind,
					CharacterDesc:    a.CharacterDesc,
					BackgroundDesc:   a.BackgroundDesc,
					TextContent:      a.TextContent,
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
			log.Printf("generate_icon: invoked source=%q desc=%q size=%dx%d", a.SourceAssetID, a.Desc, a.Width, a.Height)
			if a.SourceAssetID == "" {
				return editResult{}, fmt.Errorf("generate_icon requires source_asset_id")
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
			log.Printf("generate_image_from_text: invoked desc=%q size=%dx%d await=%v", a.Desc, a.Width, a.Height, a.AwaitResult)
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
		utils.WithMarshalOutput(func(_ context.Context, v any) (string, error) {
			b, _ := json.Marshal(v)
			var probe struct {
				AssetID string `json:"asset_id"`
			}
			if json.Unmarshal(b, &probe) == nil && probe.AssetID != "" {
				return string(b), nil
			}
			return "好的，正在把这张图生成视频，完成后会出现在左侧工作区。", nil
		}),
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
		utils.WithMarshalOutput(func(_ context.Context, v any) (string, error) {
			b, _ := json.Marshal(v)
			var probe struct {
				AssetID string `json:"asset_id"`
			}
			if json.Unmarshal(b, &probe) == nil && probe.AssetID != "" {
				return string(b), nil
			}
			return "好的，正在搜索并下载相关图片，结果会出现在左侧工作区。", nil
		}),
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
	icon, err := d.newIconTool()
	if err != nil {
		return nil, fmt.Errorf("icon tool: %w", err)
	}
	tools := []tool.BaseTool{edit, cropTool, listSizes, clarify, icon}
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
		"image_to_video":           {},
		"search_images":            {},
		// clarify_intent ends the turn; result must not feed back to model.
		"clarify_intent": {},
	}
}

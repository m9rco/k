package agent

import (
	"context"
	"fmt"

	"gameasset/internal/crop"
	"gameasset/internal/generation"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ToolDeps carries the concrete services that back the agent tools. The agent
// layer keeps a thin adapter over these so the framework stays isolated
// (design D1: thin wrapper around Eino).
type ToolDeps struct {
	Generation *generation.Service
	Crop       *crop.Service
	// SessionID scopes every tool call to the caller's session so produced
	// tasks and assets stay isolated per session.
	SessionID string
}

// --- change_character / change_background / change_text -------------------

type editArgs struct {
	// Intent is one of change_character, change_background, change_text.
	Intent string `json:"intent" jsonschema:"description=The edit intent: change_character, change_background, or change_text,enum=change_character,enum=change_background,enum=change_text"`
	// SourceAssetID is the existing asset to edit. Empty for a fresh generation.
	SourceAssetID string `json:"source_asset_id,omitempty" jsonschema:"description=ID of an existing workspace asset to edit (二次调整). Empty to generate from scratch."`
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
			kind := generation.EditKind(a.Intent)
			switch kind {
			case generation.EditCharacter, generation.EditBackground, generation.EditText:
			default:
				return editResult{}, fmt.Errorf("unsupported intent %q", a.Intent)
			}
			taskID, err := d.Generation.Start(ctx, generation.GenerateParams{
				SessionID:     d.SessionID,
				SourceAssetID: a.SourceAssetID,
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
			return editResult{TaskID: taskID, Status: "queued", Note: "Generation started; watch task progress."}, nil
		},
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
			results, err := d.Crop.CropToSizes(d.SessionID, a.SourceAssetID, a.SizeIDs)
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
	return []tool.BaseTool{edit, cropTool, listSizes}, nil
}

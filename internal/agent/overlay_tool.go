package agent

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	applog "gameasset/internal/log"
	"gameasset/internal/textoverlay"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// overlayItemArgs is one text/CTA/badge element to composite. Position is either
// a nine-grid anchor (preferred) or normalized x/y; style fields are optional and
// fall back to deterministic defaults.
type overlayItemArgs struct {
	Text     string  `json:"text" jsonschema:"description=要叠加的文字内容（CTA/角标/定档大字等），按原样渲染。"`
	Anchor   string  `json:"anchor,omitempty" jsonschema:"description=九宫格位置，取值 top-left/top/top-right/left/center/right/bottom-left/bottom/bottom-right。留空则用 x,y。"`
	X        float64 `json:"x,omitempty" jsonschema:"description=归一化横坐标 0~1（左上角），仅在未给 anchor 时生效。"`
	Y        float64 `json:"y,omitempty" jsonschema:"description=归一化纵坐标 0~1（左上角），仅在未给 anchor 时生效。"`
	FontPx   float64 `json:"font_px,omitempty" jsonschema:"description=字号（像素），留空按图尺寸自动取值。"`
	Color    string  `json:"color,omitempty" jsonschema:"description=文字颜色 hex（如 #FFFFFF），留空默认白色。"`
	Stroke   string  `json:"stroke,omitempty" jsonschema:"description=描边颜色 hex（如 #000000），留空不描边。"`
	StrokePx int     `json:"stroke_px,omitempty" jsonschema:"description=描边宽度像素，配合 stroke 使用。"`
	BgColor  string  `json:"bg_color,omitempty" jsonschema:"description=背景色块 hex（CTA 按钮/角标底板，如 #7C3AED），留空不画底板。"`
}

// overlayArgs is the overlay_text tool input: a source image plus the overlays.
type overlayArgs struct {
	SourceAssetID string            `json:"source_asset_id" jsonschema:"description=要叠加文字的工作区图片 id。"`
	Overlays      []overlayItemArgs `json:"overlays" jsonschema:"description=一个或多个文字/CTA/角标元素，按顺序叠加（后者覆盖前者）。"`
	SafeInsetFrac float64           `json:"safe_inset_frac,omitempty" jsonschema:"description=安全区内缩比例 0~0.2（如 0.05），保证文字不被平台裁切。留空默认 0.04。"`
}

// overlayResult is the structured product returned to the frontend.
type overlayResult struct {
	Status  string `json:"status"`
	AssetID string `json:"asset_id,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
}

// newOverlayTool registers overlay_text: deterministic font-rendered text/LOGO
// compositing (CTA, discount badge, launch headline) onto a workspace image. The
// product is a new asset linked to its parent. Synchronous (no async task).
func (d ToolDeps) newOverlayTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"overlay_text",
		"文字叠加：把 CTA 按钮文字/折扣角标/定档大字/品牌文字确定性地叠加到工作区某张图上（服务端字体渲染，不经过生图模型，文字清晰无错字）。"+
			"触发词：加个CTA/加按钮/打个折扣角标/加定档大字/贴文字/角标。"+
			"按 anchor 九宫格或 x,y 归一化坐标定位，可设颜色/描边/底板色块，自动遵守安全区。产物为新图回填工作区。",
		func(ctx context.Context, a overlayArgs) (overlayResult, error) {
			if d.Overlay == nil || !d.Overlay.Configured() {
				return overlayResult{}, fmt.Errorf("文字叠加暂未配置，暂不可用")
			}
			if strings.TrimSpace(a.SourceAssetID) == "" {
				return overlayResult{}, fmt.Errorf("overlay_text requires source_asset_id")
			}
			if !d.dedup.firstSeen("overlay_text|" + argSig(a)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "overlay_text").Str("source", a.SourceAssetID).Msg("duplicate same-turn call suppressed")
				return overlayResult{Status: statusDuplicate}, nil
			}
			req, err := buildOverlayRequest(a)
			if err != nil {
				return overlayResult{}, err
			}
			res, err := d.Overlay.Apply(d.SessionID, a.SourceAssetID, req, d.Lossless)
			if err != nil {
				return overlayResult{}, err
			}
			return overlayResult{Status: "done", AssetID: res.AssetID, Width: res.Width, Height: res.Height}, nil
		},
		utils.WithMarshalOutput(friendlyMarshal("好的，文字已叠加好，产物已放到左侧工作区。")),
	)
}

// buildOverlayRequest converts tool args into a textoverlay.Request, parsing hex
// colors and defaulting the safe inset. Returns an error for a malformed color so
// the model can correct rather than silently dropping the style.
func buildOverlayRequest(a overlayArgs) (textoverlay.Request, error) {
	inset := a.SafeInsetFrac
	if inset <= 0 {
		inset = 0.04
	}
	if inset > 0.2 {
		inset = 0.2
	}
	out := textoverlay.Request{SafeInsetFrac: inset}
	for i, it := range a.Overlays {
		fill, err := parseHexColor(it.Color)
		if err != nil {
			return textoverlay.Request{}, fmt.Errorf("overlay %d color: %w", i, err)
		}
		stroke, err := parseHexColor(it.Stroke)
		if err != nil {
			return textoverlay.Request{}, fmt.Errorf("overlay %d stroke: %w", i, err)
		}
		bg, err := parseHexColor(it.BgColor)
		if err != nil {
			return textoverlay.Request{}, fmt.Errorf("overlay %d bg_color: %w", i, err)
		}
		out.Overlays = append(out.Overlays, textoverlay.Overlay{
			Text:       it.Text,
			Anchor:     textoverlay.Anchor(it.Anchor),
			X:          it.X,
			Y:          it.Y,
			FontPx:     it.FontPx,
			Color:      fill,
			Stroke:     stroke,
			StrokePx:   it.StrokePx,
			Background: bg,
		})
	}
	return out, nil
}

// parseHexColor parses #RGB / #RRGGBB / #RRGGBBAA into a color.Color. Empty
// input returns (nil, nil) so an unset field falls back to the renderer default.
func parseHexColor(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	s = strings.TrimPrefix(s, "#")
	switch len(s) {
	case 3: // RGB shorthand
		r := hexNibble(s[0])
		g := hexNibble(s[1])
		b := hexNibble(s[2])
		if r < 0 || g < 0 || b < 0 {
			return nil, fmt.Errorf("invalid hex color %q", s)
		}
		return color.RGBA{R: uint8(r * 17), G: uint8(g * 17), B: uint8(b * 17), A: 255}, nil
	case 6, 8:
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid hex color %q: %w", s, err)
		}
		if len(s) == 6 {
			return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
		}
		return color.RGBA{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}, nil
	default:
		return nil, fmt.Errorf("invalid hex color %q (use #RGB, #RRGGBB or #RRGGBBAA)", s)
	}
}

// hexNibble returns the 0..15 value of a hex digit, or -1 if invalid.
func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

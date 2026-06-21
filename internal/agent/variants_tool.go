package agent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"gameasset/internal/generation"
	applog "gameasset/internal/log"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// Variant batch bounds: a creative A/B set is meaningful at 2+ and unwieldy past
// 8 (cost + workspace clutter). Requests outside the band are clamped, not
// rejected, and the clamp is surfaced to the user.
const (
	variantsDefaultCount = 4
	variantsMinCount     = 2
	variantsMaxCount     = 8
)

// variantStrategies maps a variant dimension to a server-fixed set of style
// offsets. Each offset is a phrasing the EditBackground prompt receives as the
// new-background description, so the foreground subject is preserved while the
// scene/mood/look diverges per variant. The offsets are fully server-controlled
// (never user free text), so they carry no injection surface; the optional user
// brief is Sanitized and prefixed to each offset.
//
// Dimensions:
//   - style:       overall art style / rendering look
//   - palette:     color tonality / mood
//   - composition: framing / camera angle / depth
//   - copy:        copy-emphasis scene framing (hero/value/urgency/social-proof)
var variantStrategies = map[string][]string{
	"style": {
		"科幻霓虹赛博风的场景，冷色霓虹光效",
		"写实电影感的场景，柔和体积光与景深",
		"日系动漫赛璐珞风的场景，明快通透",
		"水彩手绘插画风的场景，柔和笔触",
		"极简扁平设计风的场景，大面积留白",
		"暗黑奇幻史诗风的场景，强对比戏剧光",
		"复古像素/合成波风的场景，怀旧色调",
		"国风水墨意境的场景，淡雅留白",
	},
	"palette": {
		"暖橙金色调的场景，黄昏阳光氛围",
		"冷蓝青色调的场景，夜幕清冷氛围",
		"高饱和撞色的场景，活力张扬氛围",
		"低饱和莫兰迪色调的场景，柔和高级氛围",
		"黑金奢华色调的场景，高端质感氛围",
		"粉紫梦幻色调的场景，柔美治愈氛围",
		"墨绿森系色调的场景，自然沉静氛围",
		"红黑高对比色调的场景，紧张热血氛围",
	},
	"composition": {
		"主体居中的正面英雄构图，背景纵深向后延展",
		"低角度仰拍的场景，强化主体的高大与气势",
		"大特写近景构图，背景虚化突出主体细节",
		"广角全景构图，主体置于黄金分割点，环境叙事感强",
		"俯视斜角构图的场景，呈现更丰富的空间层次",
		"对称式构图的场景，主体两侧元素平衡呼应",
		"留白式构图，主体偏置一侧，另一侧留给文案空间",
		"动态对角线构图的场景，强化方向感与冲击力",
	},
	"copy": {
		"突出主角魅力的英雄主场景，聚焦人物吸引力",
		"突出核心玩法卖点的场景，画面强调可玩内容",
		"营造限时紧迫感的场景，氛围强调「立即行动」",
		"强调口碑与人气的热闹场景，体现众多玩家在场",
		"强调全新上线/版本焕新的场景，氛围崭新醒目",
		"强调福利赠送的场景，画面洋溢慷慨与惊喜感",
		"强调高品质画面表现的场景，极致精美的视觉",
		"强调竞技对抗张力的场景，对峙与热血氛围",
	},
}

// variantsDimensionLabel renders a Chinese label for a dimension key (for the
// acknowledgment text and per-variant labels).
func variantsDimensionLabel(dim string) string {
	switch dim {
	case "style":
		return "风格"
	case "palette":
		return "配色"
	case "composition":
		return "构图"
	case "copy":
		return "文案侧重"
	default:
		return "风格"
	}
}

// variantsArgs is the generate_variants tool input.
type variantsArgs struct {
	SourceAssetID string `json:"source_asset_id" jsonschema:"description=要批量出变体的工作区图片 id。"`
	Count         int    `json:"count,omitempty" jsonschema:"description=变体数量，默认 4，支持 2~8，超出自动收敛。"`
	Dimension     string `json:"dimension,omitempty" jsonschema:"description=变体维度：style(风格)/palette(配色)/composition(构图)/copy(文案侧重)，默认 style。"`
	Brief         string `json:"brief,omitempty" jsonschema:"description=这批变体的统一方向（可选，如「赛博朋克夜景」），会作为每个变体的共同前缀。"`
}

// variantItem describes one launched variant for the frontend (task id + a
// human-readable dimension label).
type variantItem struct {
	TaskID string `json:"task_id"`
	Label  string `json:"label"`
}

// variantsResult is the structured batch returned to the frontend. TaskIDs lets
// the controller group the placeholders that follow into one batch; BatchID is a
// stable grouping key derived from the request.
type variantsResult struct {
	Status    string        `json:"status"`
	BatchID   string        `json:"batch_id,omitempty"`
	Dimension string        `json:"dimension,omitempty"`
	Requested int           `json:"requested,omitempty"`
	Count     int           `json:"count,omitempty"`
	Clamped   bool          `json:"clamped,omitempty"`
	TaskIDs   []string      `json:"task_ids,omitempty"`
	Variants  []variantItem `json:"variants,omitempty"`
	Failed    int           `json:"failed,omitempty"`
}

// newVariantsTool registers generate_variants: from one source image, launch N
// independent generation tasks — each a creative variant along a chosen
// dimension — reusing the existing image pipeline (EditBackground keeps the
// foreground subject, repaints scene/mood per variant). Each variant is its own
// async task, so a single failure never blocks the rest, and placeholders/SSE
// fill-in come for free from the standard task machinery.
func (d ToolDeps) newVariantsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"generate_variants",
		"批量变体：对工作区一张图一次性产出多个不同的 creative 版本（用于买量团队批量出素材测 CTR / A·B 测试）。"+
			"触发词：多出几版/多来几个版本/不同风格的版本/批量变体/A·B素材/出N个版本/测CTR/多个变体。"+
			"维度可选 style(风格)/palette(配色)/composition(构图)/copy(文案侧重)，数量默认 4（支持 2~8）。"+
			"每个变体是独立异步任务并行推进，单个失败不影响其它；产物陆续回填工作区、可分组对比。"+
			"保留原图前景主体，按所选维度重绘场景/氛围。绝不要反复调 edit_image 逐个生成——一次 generate_variants 即可。",
		func(ctx context.Context, a variantsArgs) (variantsResult, error) {
			if d.Generation == nil {
				return variantsResult{}, fmt.Errorf("批量变体暂未配置，暂不可用")
			}
			source := strings.TrimSpace(a.SourceAssetID)
			if source == "" {
				return variantsResult{}, fmt.Errorf("generate_variants requires source_asset_id")
			}

			dim := strings.ToLower(strings.TrimSpace(a.Dimension))
			offsets, ok := variantStrategies[dim]
			if !ok {
				dim = "style"
				offsets = variantStrategies[dim]
			}

			requested := a.Count
			if requested == 0 {
				requested = variantsDefaultCount
			}
			count := requested
			clamped := false
			if count < variantsMinCount {
				count, clamped = variantsMinCount, true
			}
			if count > variantsMaxCount {
				count, clamped = variantsMaxCount, true
			}
			if count > len(offsets) {
				count, clamped = len(offsets), true
			}

			brief := generation.Sanitize(a.Brief)
			// Dedup on the semantic request (source + dimension + count + brief). A
			// second identical same-turn call is suppressed, but a call differing in
			// brief (a genuinely different creative direction) still runs.
			if !d.dedup.firstSeen(fmt.Sprintf("generate_variants|%s|%s|%d|%s", source, dim, count, brief)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "generate_variants").Str("source", source).Msg("duplicate same-turn call suppressed")
				return variantsResult{Status: statusDuplicate}, nil
			}

			batchID := variantsBatchID(source, dim, count)
			dimLabel := variantsDimensionLabel(dim)

			res := variantsResult{
				Status:    "queued",
				BatchID:   batchID,
				Dimension: dim,
				Requested: requested,
				Clamped:   clamped,
			}
			for i := 0; i < count; i++ {
				desc := offsets[i]
				if brief != "" {
					desc = brief + "，" + desc
				}
				taskID, err := d.Generation.Start(ctx, generation.GenerateParams{
					SessionID:        d.SessionID,
					SourceAssetID:    source,
					Lossless:         d.Lossless,
					ProviderOverride: d.ImageOverride,
					Slots: generation.Slots{
						Kind:           generation.EditBackground,
						BackgroundDesc: desc,
					},
				})
				if err != nil {
					// Failure isolation: log and skip this variant, keep launching the
					// rest. The batch is best-effort; a partial batch still ships.
					applog.From(ctx).Warn().Str("event", "tool.variant_start_failed").Str("tool", "generate_variants").Int("index", i).Err(err).Msg("variant task failed to start, continuing")
					res.Failed++
					continue
				}
				res.TaskIDs = append(res.TaskIDs, taskID)
				res.Variants = append(res.Variants, variantItem{
					TaskID: taskID,
					Label:  fmt.Sprintf("%s变体 %d", dimLabel, i+1),
				})
			}
			res.Count = len(res.TaskIDs)
			if res.Count == 0 {
				return variantsResult{}, fmt.Errorf("generate_variants: all %d variant tasks failed to start", count)
			}
			return res, nil
		},
		utils.WithMarshalOutput(variantsMarshal),
	)
}

// variantsMarshal turns the variants result into either a friendly Chinese
// acknowledgment (standalone path — the model just confirms and stops) or an
// empty string for a suppressed duplicate. The variants are async tasks whose
// products stream into the workspace, so the model never needs the JSON back.
func variantsMarshal(_ context.Context, v any) (string, error) {
	res, ok := v.(variantsResult)
	if !ok {
		return "好的，正在为这张图生成多个变体，产物会陆续出现在左侧工作区。", nil
	}
	if res.Status == statusDuplicate {
		return "", nil
	}
	dimLabel := variantsDimensionLabel(res.Dimension)
	var b strings.Builder
	b.WriteString("好的，正在为这张图生成 ")
	b.WriteString(strconv.Itoa(res.Count))
	b.WriteString(" 个不同")
	b.WriteString(dimLabel)
	b.WriteString("的变体，产物会陆续出现在左侧工作区，可分组对比。")
	if res.Clamped {
		b.WriteString("（你请求的数量已收敛到 ")
		b.WriteString(strconv.Itoa(res.Count))
		b.WriteString(" 个）")
	}
	if res.Failed > 0 {
		b.WriteString("（其中 ")
		b.WriteString(strconv.Itoa(res.Failed))
		b.WriteString(" 个未能发起）")
	}
	return b.String(), nil
}

// variantsBatchID derives a stable, short grouping key from the request so the
// frontend can cluster this batch's placeholders without any server-side state.
func variantsBatchID(source, dim string, count int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%d", source, dim, count)))
	return "batch_" + hex.EncodeToString(sum[:6])
}

package agent

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"

	"gameasset/internal/copywriting"
	applog "gameasset/internal/log"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// copyArgs are the generate_copy tool inputs. source_asset_id anchors the copy
// to a workspace image (its cached vision report is reused as context when
// present); platform and max_title_len are optional constraints; brief is the
// user's free-text requirement, treated downstream as a constrained data slot
// (never as instructions — see copywriting.systemPrompt).
type copyArgs struct {
	SourceAssetID string `json:"source_asset_id,omitempty" jsonschema:"description=工作区图片 id：基于这张素材及其宣发分析报告创作文案。可留空（仅按 brief 创作）。"`
	Platform      string `json:"platform,omitempty" jsonschema:"description=目标投放平台/广告位，如 朋友圈信息流 / TapTap / 抖音。可选。"`
	MaxTitleLen   int    `json:"max_title_len,omitempty" jsonschema:"description=主标题字数上限（按字符计），0 表示不限制。"`
	Brief         string `json:"brief,omitempty" jsonschema:"description=用户的文案需求描述（如 强调多人玩法 / 突出福利）。可留空。"`
}

// copyResult is the structured copy returned to the frontend. It mirrors
// copywriting.Copy plus a status so the marshaler can render a copy card. The
// fields are returned to the model/UI directly (this is NOT an async task).
type copyResult struct {
	Status        string   `json:"status"`
	Title         string   `json:"title,omitempty"`
	Slogans       []string `json:"slogans,omitempty"`
	SellingPoints []string `json:"selling_points,omitempty"`
	PlatformCopy  string   `json:"platform_copy,omitempty"`
	Note          string   `json:"note,omitempty"`
}

// newCopywritingTool registers generate_copy: it resolves the (optional) source
// asset's cached vision report, drafts structured marketing copy via the
// copywriting service, and returns the copy as a structured result the frontend
// renders as a copy card (and downstream tools can reference). It is synchronous
// (no async task id) so it is NOT in AsyncTaskTools — its result feeds back so the
// model can present the copy in prose if useful.
func (d ToolDeps) newCopywritingTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"generate_copy",
		"宣发文案生成：为一款游戏创作结构化投放文案（主标题/广告语/卖点/平台投放短文案）。"+
			"触发词：写文案/想几句广告语/来几条卖点/投放文案/标题文案/slogan。"+
			"会基于工作区素材的宣发分析报告（如有）创作，不虚构卖点。可指定 platform 与 max_title_len 约束。",
		func(ctx context.Context, a copyArgs) (copyResult, error) {
			if d.Copywriting == nil || !d.Copywriting.Configured() {
				return copyResult{}, fmt.Errorf("文案生成暂未配置，暂不可用")
			}
			if !d.dedup.firstSeen("generate_copy|" + argSig(a)) {
				applog.From(ctx).Warn().Str("event", "tool.duplicate_suppressed").Str("tool", "generate_copy").Msg("duplicate same-turn call suppressed")
				return copyResult{Status: statusDuplicate}, nil
			}
			report := d.copyVisionReport(ctx, a.SourceAssetID)
			copy, err := d.Copywriting.Generate(ctx, copywriting.Request{
				Platform:     a.Platform,
				MaxTitleLen:  a.MaxTitleLen,
				Brief:        a.Brief,
				VisionReport: report,
			})
			if err != nil {
				return copyResult{}, err
			}
			return copyResult{
				Status:        "done",
				Title:         copy.Title,
				Slogans:       copy.Slogans,
				SellingPoints: copy.SellingPoints,
				PlatformCopy:  copy.PlatformCopy,
			}, nil
		},
		utils.WithMarshalOutput(copyMarshal()),
	)
}

// copyVisionReport returns the cached marketing-analysis report for a source
// asset (keyed by the asset file's md5, matching the adapt pre-stage cache), or
// "" when there is no asset, no store, or no cached report. Best-effort: any read
// error degrades to "" so copy generation proceeds without a report.
func (d ToolDeps) copyVisionReport(ctx context.Context, assetID string) string {
	if assetID == "" || d.Store == nil {
		return ""
	}
	asset, err := d.Store.GetAsset(d.SessionID, assetID)
	if err != nil || asset == nil {
		return ""
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		return ""
	}
	key := fmt.Sprintf("%x", md5.Sum(data))
	report, err := d.Store.GetVisionReport(key)
	if err != nil {
		applog.From(ctx).Warn().Str("event", "copy.report_lookup_failed").Err(err).Msg("vision report lookup failed; generating copy without report")
		return ""
	}
	return report
}

// copyMarshal renders the copy result. A duplicate same-turn call yields no
// bubble; otherwise the full structured JSON is returned so the frontend can
// render a copy card and the model can reference the copy. generate_copy is NOT
// ToolReturnDirectly, so the model still gets the result to optionally narrate.
func copyMarshal() utils.MarshalOutput {
	return func(_ context.Context, v any) (string, error) {
		b, _ := json.Marshal(v)
		var probe struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(b, &probe) == nil && probe.Status == statusDuplicate {
			return "", nil
		}
		return string(b), nil
	}
}

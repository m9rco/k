package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	applog "gameasset/internal/log"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// maxPlanSteps bounds a submit_plan plan so a runaway decomposition cannot drive
// an unbounded serial chain. Steps beyond this are truncated (and reported).
const maxPlanSteps = 6

// plannableTools is the set of tools a plan step may invoke. It is the subset of
// the whitelist that PRODUCES a workspace asset and can therefore be chained
// (its product feeds a later step). clarify_intent / list_platform_sizes /
// generate_copy and submit_plan itself are intentionally excluded: they either
// end the turn, return non-asset data, or would recurse.
var plannableTools = map[string]struct{}{
	"edit_image":               {},
	"adapt_to_platform":        {},
	"image_to_video":           {},
	"generate_variants":        {},
	"generate_image_from_text": {},
	"search_images":            {},
	"overlay_text":             {},
	"extract_layer":            {},
}

// toolTitle renders a short Chinese title for a plan step's tool, used in the
// plan_created event so the frontend card reads naturally. Falls back to the raw
// tool name for anything unmapped.
func toolTitle(toolName string) string {
	switch toolName {
	case "edit_image":
		return "编辑图片（换背景/换角色/换文案）"
	case "adapt_to_platform":
		return "平台尺寸适配"
	case "image_to_video":
		return "生成视频"
	case "generate_variants":
		return "批量变体"
	case "generate_image_from_text":
		return "文生图"
	case "search_images":
		return "搜索图片"
	case "overlay_text":
		return "文字叠加"
	case "extract_layer":
		return "抠图"
	default:
		return toolName
	}
}

// PlanStepInfo is the per-step descriptor carried in the plan_created event so
// the frontend can render the plan card skeleton up front.
type PlanStepInfo struct {
	ID    string `json:"id"`
	Tool  string `json:"tool"`
	Title string `json:"title"`
}

// PlanEmitter surfaces execution-plan lifecycle events to the frontend. It is
// injected by the orchestrator (which maps the calls to transport events over
// the hub) so this file stays free of the transport layer. A nil emitter is
// tolerated by the executor (events are simply dropped).
type PlanEmitter interface {
	Created(planID string, steps []PlanStepInfo)
	StepStarted(planID, stepID string)
	StepDone(planID, stepID, assetID string, assetIDs []string)
	StepFailed(planID, stepID, reason string)
	Done(planID, status string)
}

// planStepArg is one step the model submits via submit_plan: a stable id, the
// tool to run, and that tool's argument object. Args values may carry product
// placeholders ($stepId.asset_id / $stepId.asset_ids) referencing earlier steps.
type planStepArg struct {
	ID    string         `json:"id" jsonschema:"description=Stable step id (e.g. step1) used to reference this step's product from later steps"`
	Tool  string         `json:"tool" jsonschema:"description=The tool this step runs. Must be one of: edit_image, adapt_to_platform, image_to_video, generate_variants, generate_image_from_text, search_images, overlay_text, extract_layer"`
	Title string         `json:"title,omitempty" jsonschema:"description=Optional short human-readable title for this step shown in the plan card"`
	Args  map[string]any `json:"args" jsonschema:"description=The argument object for the step's tool. To consume an earlier step's product use the placeholder string \"$step1.asset_id\" (single id) or \"$step1.asset_ids\" (id list) as the field value; the server substitutes the real id before running the step."`
}

// submitPlanArgs is the submit_plan tool input: an ordered list of steps.
type submitPlanArgs struct {
	Steps []planStepArg `json:"steps" jsonschema:"description=Ordered steps to execute serially. Each step runs after the previous one completes; a later step may reference an earlier step's product via a placeholder. Max 6 steps."`
}

// planStepResult is the per-step outcome reported back to the model so it can
// summarize for the user.
type planStepResult struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Status  string `json:"status"` // done | failed | skipped
	AssetID string `json:"asset_id,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// planResult is submit_plan's structured output (fed back to the model, NOT
// ToolReturnDirectly) so the model can tell the user what completed and where it
// stopped.
type planResult struct {
	PlanID    string           `json:"plan_id"`
	Status    string           `json:"status"` // completed | aborted
	Truncated bool             `json:"truncated,omitempty"`
	Steps     []planStepResult `json:"steps"`
}

// stepOutput is the product extracted from a completed step, addressable by
// later steps via placeholders.
type stepOutput struct {
	assetID  string
	assetIDs []string
}

// placeholderRe matches a WHOLE-value product placeholder like "$step1.asset_id"
// or "$step1.asset_ids". Only full-value references are supported (not embedded
// in a larger string), which keeps substitution unambiguous.
var placeholderRe = regexp.MustCompile(`^\$([A-Za-z0-9_-]+)\.(asset_id|asset_ids)$`)

// resolvePlaceholders walks args in place, replacing any whole-value product
// placeholder with the referenced earlier step's product. It returns an error
// when a placeholder references an unknown/incomplete step or an empty product,
// so the executor can fail that step instead of running it with a bad input.
func resolvePlaceholders(v any, outputs map[string]stepOutput) (any, error) {
	switch t := v.(type) {
	case string:
		m := placeholderRe.FindStringSubmatch(t)
		if m == nil {
			return t, nil
		}
		ref, field := m[1], m[2]
		out, ok := outputs[ref]
		if !ok {
			return nil, fmt.Errorf("引用了未完成或不存在的步骤 %q", ref)
		}
		if field == "asset_ids" {
			if len(out.assetIDs) == 0 {
				return nil, fmt.Errorf("步骤 %q 没有可引用的产物列表", ref)
			}
			return out.assetIDs, nil
		}
		if out.assetID == "" {
			return nil, fmt.Errorf("步骤 %q 的产物为空，无法引用", ref)
		}
		return out.assetID, nil
	case map[string]any:
		for k, val := range t {
			rv, err := resolvePlaceholders(val, outputs)
			if err != nil {
				return nil, err
			}
			t[k] = rv
		}
		return t, nil
	case []any:
		for i, val := range t {
			rv, err := resolvePlaceholders(val, outputs)
			if err != nil {
				return nil, err
			}
			t[i] = rv
		}
		return t, nil
	default:
		return v, nil
	}
}

// parseStepOutput extracts the product asset id(s) from a tool's marshaled JSON
// result. It handles both the single-asset shape ({"asset_id": "..."}) and the
// adapt_to_platform multi-outcome shape ({"outcomes": [{"asset_id": "..."}]}).
func parseStepOutput(resultJSON string) stepOutput {
	var probe struct {
		AssetID  string `json:"asset_id"`
		Outcomes []struct {
			AssetID string `json:"asset_id"`
		} `json:"outcomes"`
	}
	_ = json.Unmarshal([]byte(resultJSON), &probe)
	out := stepOutput{assetID: probe.AssetID}
	if probe.AssetID != "" {
		out.assetIDs = append(out.assetIDs, probe.AssetID)
	}
	for _, o := range probe.Outcomes {
		if o.AssetID == "" {
			continue
		}
		if out.assetID == "" {
			out.assetID = o.AssetID
		}
		out.assetIDs = append(out.assetIDs, o.AssetID)
	}
	return out
}

// runPlan executes an ordered plan SERIALLY: each step's product is captured and
// made available to later steps via placeholders. Any step failure (unknown/non-
// chainable tool, unresolved placeholder, tool error, or empty product) aborts
// the plan immediately — no later step runs — while completed products stay in
// the workspace. Lifecycle events are emitted through emit (nil-tolerant).
func (d ToolDeps) runPlan(ctx context.Context, tools map[string]tool.InvokableTool, in submitPlanArgs, emit PlanEmitter) planResult {
	lg := applog.From(ctx)
	planID := fmt.Sprintf("plan-%d", time.Now().UnixNano())
	steps := in.Steps
	truncated := false
	if len(steps) > maxPlanSteps {
		steps = steps[:maxPlanSteps]
		truncated = true
	}

	infos := make([]PlanStepInfo, len(steps))
	for i, s := range steps {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = toolTitle(s.Tool)
		}
		infos[i] = PlanStepInfo{ID: s.ID, Tool: s.Tool, Title: title}
	}
	if emit != nil {
		emit.Created(planID, infos)
	}
	lg.Info().Str("event", "plan.start").Str("plan", planID).Int("steps", len(steps)).Bool("truncated", truncated).Msg("execution plan started")

	res := planResult{PlanID: planID, Truncated: truncated, Steps: make([]planStepResult, 0, len(steps))}
	outputs := make(map[string]stepOutput, len(steps))

	abort := func(i int, st planStepArg, reason string) planResult {
		if emit != nil {
			emit.StepFailed(planID, st.ID, reason)
		}
		lg.Warn().Str("event", "plan.step_failed").Str("plan", planID).Str("step", st.ID).Str("tool", st.Tool).Str("reason", reason).Msg("plan step failed, aborting")
		res.Steps = append(res.Steps, planStepResult{ID: st.ID, Tool: st.Tool, Status: "failed", Reason: reason})
		// Remaining steps are reported as skipped so the model can tell the user.
		for _, sk := range steps[i+1:] {
			res.Steps = append(res.Steps, planStepResult{ID: sk.ID, Tool: sk.Tool, Status: "skipped"})
		}
		res.Status = "aborted"
		if emit != nil {
			emit.Done(planID, res.Status)
		}
		return res
	}

	for i, st := range steps {
		if ctx.Err() != nil {
			return abort(i, st, "已取消")
		}
		if emit != nil {
			emit.StepStarted(planID, st.ID)
		}
		lg.Info().Str("event", "plan.step_start").Str("plan", planID).Str("step", st.ID).Str("tool", st.Tool).Msg("plan step started")

		tl, ok := tools[st.Tool]
		if !ok {
			return abort(i, st, fmt.Sprintf("未知或不可用的工具 %q", st.Tool))
		}
		if _, allowed := plannableTools[st.Tool]; !allowed {
			return abort(i, st, fmt.Sprintf("工具 %q 不能用于计划串联", st.Tool))
		}

		args := st.Args
		if args == nil {
			args = map[string]any{}
		}
		resolved, err := resolvePlaceholders(args, outputs)
		if err != nil {
			return abort(i, st, err.Error())
		}
		argMap, _ := resolved.(map[string]any)
		if argMap == nil {
			argMap = map[string]any{}
		}
		// Force synchronous completion so the product is ready before the next step
		// (and so a failure surfaces here, not after the turn ends).
		argMap["await_result"] = true
		argsJSON, err := json.Marshal(argMap)
		if err != nil {
			return abort(i, st, "参数序列化失败")
		}

		resJSON, err := tl.InvokableRun(ctx, string(argsJSON))
		if err != nil {
			return abort(i, st, err.Error())
		}
		out := parseStepOutput(resJSON)
		if out.assetID == "" && len(out.assetIDs) == 0 {
			return abort(i, st, "该步骤未产出可用产物")
		}
		outputs[st.ID] = out
		res.Steps = append(res.Steps, planStepResult{ID: st.ID, Tool: st.Tool, Status: "done", AssetID: out.assetID})
		if emit != nil {
			emit.StepDone(planID, st.ID, out.assetID, out.assetIDs)
		}
		lg.Info().Str("event", "plan.step_done").Str("plan", planID).Str("step", st.ID).Str("asset", out.assetID).Int("assets", len(out.assetIDs)).Msg("plan step done")
	}

	res.Status = "completed"
	if emit != nil {
		emit.Done(planID, res.Status)
	}
	lg.Info().Str("event", "plan.done").Str("plan", planID).Str("status", res.Status).Msg("execution plan completed")
	return res
}

// newSubmitPlanTool builds the submit_plan tool. It is given the already-built
// chainable tools (name → tool) so the executor can dispatch each step to the
// real implementation. Its result is fed back to the model (NOT
// ToolReturnDirectly) so the model can summarize the run for the user.
func (d ToolDeps) newSubmitPlanTool(tools map[string]tool.InvokableTool) (tool.InvokableTool, error) {
	return utils.InferTool(
		"submit_plan",
		"多步串联编排：当用户一句话要求多个【有先后依赖】的连续操作（如「换角色然后切尺寸」「找图再生视频」「换好背景后做成各平台尺寸」）时，用本工具一次性提交完整的有序步骤，由系统按顺序串行执行。"+
			"每个步骤含：id（如 step1）、tool（要调用的工具名）、args（该工具参数）。"+
			"后续步骤要用到前面步骤的产物时，在 args 里用占位符字符串 \"$step1.asset_id\"（单个产物）或 \"$step1.asset_ids\"（产物列表）引用，系统会在执行该步前替换为真实 id。"+
			"系统逐步执行，任意步骤失败会立即停止并保留已完成产物。最多 6 步。"+
			"仅用于多步依赖请求；单个操作请直接调用对应工具，不要用本工具。",
		func(ctx context.Context, a submitPlanArgs) (planResult, error) {
			if len(a.Steps) == 0 {
				return planResult{}, fmt.Errorf("submit_plan requires at least one step")
			}
			return d.runPlan(ctx, tools, a, d.PlanEvents), nil
		},
	)
}

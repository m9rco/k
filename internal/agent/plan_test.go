package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// mockTool is a fake InvokableTool capturing the JSON args it was run with and
// returning a canned result. It lets the plan executor tests exercise placeholder
// substitution and chaining without the real generation pipeline.
type mockTool struct {
	name      string
	result    string // canned JSON returned by InvokableRun
	err       error  // when non-nil, InvokableRun returns it (simulates failure)
	gotArgs   string // captured args of the last call
	callCount int
}

func (m *mockTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: m.name}, nil
}

func (m *mockTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	m.callCount++
	m.gotArgs = argsJSON
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

// recordEmitter captures plan lifecycle events for assertions.
type recordEmitter struct {
	created   bool
	stepDone  []string
	stepFail  []string
	doneState string
}

func (r *recordEmitter) Created(_ string, _ []PlanStepInfo) { r.created = true }
func (r *recordEmitter) StepStarted(_, _ string)            {}
func (r *recordEmitter) StepDone(_, stepID, _ string, _ []string) {
	r.stepDone = append(r.stepDone, stepID)
}
func (r *recordEmitter) StepFailed(_, stepID, reason string) {
	r.stepFail = append(r.stepFail, stepID+":"+reason)
}
func (r *recordEmitter) Done(_, status string) { r.doneState = status }

func TestResolvePlaceholders(t *testing.T) {
	outputs := map[string]stepOutput{
		"step1": {assetID: "asset-A", assetIDs: []string{"asset-A"}},
		"step2": {assetID: "asset-B", assetIDs: []string{"asset-B", "asset-C"}},
	}
	t.Run("single asset_id", func(t *testing.T) {
		args := map[string]any{"source_asset_id": "$step1.asset_id"}
		got, err := resolvePlaceholders(args, outputs)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.(map[string]any)["source_asset_id"] != "asset-A" {
			t.Errorf("got %v, want asset-A", got)
		}
	})
	t.Run("asset_ids list", func(t *testing.T) {
		args := map[string]any{"reference_asset_ids": "$step2.asset_ids"}
		got, err := resolvePlaceholders(args, outputs)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		ids, ok := got.(map[string]any)["reference_asset_ids"].([]string)
		if !ok || len(ids) != 2 || ids[0] != "asset-B" {
			t.Errorf("got %v, want [asset-B asset-C]", got)
		}
	})
	t.Run("nested in slice", func(t *testing.T) {
		args := map[string]any{"reference_asset_ids": []any{"$step1.asset_id", "literal"}}
		got, err := resolvePlaceholders(args, outputs)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		list := got.(map[string]any)["reference_asset_ids"].([]any)
		if list[0] != "asset-A" || list[1] != "literal" {
			t.Errorf("got %v, want [asset-A literal]", list)
		}
	})
	t.Run("unknown step errors", func(t *testing.T) {
		_, err := resolvePlaceholders(map[string]any{"x": "$stepX.asset_id"}, outputs)
		if err == nil {
			t.Error("expected error for unknown step reference")
		}
	})
	t.Run("empty product errors", func(t *testing.T) {
		_, err := resolvePlaceholders(map[string]any{"x": "$step3.asset_id"}, map[string]stepOutput{"step3": {}})
		if err == nil {
			t.Error("expected error for empty product reference")
		}
	})
	t.Run("non-placeholder string untouched", func(t *testing.T) {
		got, _ := resolvePlaceholders(map[string]any{"desc": "中国风背景"}, outputs)
		if got.(map[string]any)["desc"] != "中国风背景" {
			t.Error("literal string should be untouched")
		}
	})
}

func TestParseStepOutput(t *testing.T) {
	t.Run("top-level asset_id", func(t *testing.T) {
		out := parseStepOutput(`{"task_id":"t1","status":"done","asset_id":"a1"}`)
		if out.assetID != "a1" || len(out.assetIDs) != 1 {
			t.Errorf("got %+v, want assetID a1", out)
		}
	})
	t.Run("adapt outcomes", func(t *testing.T) {
		out := parseStepOutput(`{"outcomes":[{"size_id":"s1","asset_id":"a1"},{"size_id":"s2","asset_id":"a2"}]}`)
		if out.assetID != "a1" || len(out.assetIDs) != 2 {
			t.Errorf("got %+v, want a1 + 2 ids", out)
		}
	})
	t.Run("no asset", func(t *testing.T) {
		out := parseStepOutput(`{"status":"queued"}`)
		if out.assetID != "" || len(out.assetIDs) != 0 {
			t.Errorf("got %+v, want empty", out)
		}
	})
}

func TestRunPlanSuccessChain(t *testing.T) {
	edit := &mockTool{name: "edit_image", result: `{"task_id":"t1","status":"done","asset_id":"edited-1"}`}
	adapt := &mockTool{name: "adapt_to_platform", result: `{"outcomes":[{"size_id":"ios.1","asset_id":"sz-1"},{"size_id":"ios.2","asset_id":"sz-2"}]}`}
	tools := map[string]tool.InvokableTool{"edit_image": edit, "adapt_to_platform": adapt}

	in := submitPlanArgs{Steps: []planStepArg{
		{ID: "step1", Tool: "edit_image", Args: map[string]any{"intent": "change_character", "source_asset_id": "img2", "reference_asset_ids": []any{"img1"}, "character_desc": "机甲战士"}},
		{ID: "step2", Tool: "adapt_to_platform", Args: map[string]any{"source_asset_id": "$step1.asset_id", "size_ids": []any{"ios.1", "ios.2"}}},
	}}
	emit := &recordEmitter{}
	res := (ToolDeps{}).runPlan(context.Background(), tools, in, emit)

	if res.Status != "completed" {
		t.Fatalf("status = %q, want completed", res.Status)
	}
	if len(res.Steps) != 2 || res.Steps[0].Status != "done" || res.Steps[1].Status != "done" {
		t.Fatalf("steps = %+v, want both done", res.Steps)
	}
	// step2 must have received the resolved asset id from step1, plus await_result.
	var got map[string]any
	if err := json.Unmarshal([]byte(adapt.gotArgs), &got); err != nil {
		t.Fatalf("adapt args not JSON: %v", err)
	}
	if got["source_asset_id"] != "edited-1" {
		t.Errorf("adapt source_asset_id = %v, want edited-1 (resolved placeholder)", got["source_asset_id"])
	}
	if got["await_result"] != true {
		t.Errorf("adapt await_result = %v, want true (forced for chaining)", got["await_result"])
	}
	if emit.doneState != "completed" || len(emit.stepDone) != 2 {
		t.Errorf("emit = %+v, want 2 step_done + completed", emit)
	}
}

func TestRunPlanAbortsOnFailure(t *testing.T) {
	edit := &mockTool{name: "edit_image", err: context.DeadlineExceeded}
	adapt := &mockTool{name: "adapt_to_platform", result: `{"outcomes":[{"asset_id":"x"}]}`}
	tools := map[string]tool.InvokableTool{"edit_image": edit, "adapt_to_platform": adapt}

	in := submitPlanArgs{Steps: []planStepArg{
		{ID: "step1", Tool: "edit_image", Args: map[string]any{"intent": "change_character"}},
		{ID: "step2", Tool: "adapt_to_platform", Args: map[string]any{"source_asset_id": "$step1.asset_id", "size_ids": []any{"ios.1"}}},
	}}
	emit := &recordEmitter{}
	res := (ToolDeps{}).runPlan(context.Background(), tools, in, emit)

	if res.Status != "aborted" {
		t.Fatalf("status = %q, want aborted", res.Status)
	}
	if adapt.callCount != 0 {
		t.Errorf("adapt should NOT run after step1 failed, got %d calls", adapt.callCount)
	}
	if len(res.Steps) != 2 || res.Steps[0].Status != "failed" || res.Steps[1].Status != "skipped" {
		t.Errorf("steps = %+v, want step1 failed + step2 skipped", res.Steps)
	}
	if len(emit.stepFail) != 1 || emit.doneState != "aborted" {
		t.Errorf("emit = %+v, want 1 step_failed + aborted", emit)
	}
}

func TestRunPlanRejectsUnchainableTool(t *testing.T) {
	tools := map[string]tool.InvokableTool{}
	in := submitPlanArgs{Steps: []planStepArg{{ID: "step1", Tool: "clarify_intent", Args: map[string]any{}}}}
	res := (ToolDeps{}).runPlan(context.Background(), tools, in, nil)
	if res.Status != "aborted" || res.Steps[0].Status != "failed" {
		t.Errorf("expected abort on unknown/non-chainable tool, got %+v", res)
	}
}

func TestRunPlanTruncatesOverLimit(t *testing.T) {
	mk := &mockTool{name: "edit_image", result: `{"asset_id":"a"}`}
	tools := map[string]tool.InvokableTool{"edit_image": mk}
	steps := make([]planStepArg, maxPlanSteps+2)
	for i := range steps {
		steps[i] = planStepArg{ID: "step" + itoa(i+1), Tool: "edit_image", Args: map[string]any{"intent": "change_background", "background_desc": "x"}}
	}
	res := (ToolDeps{}).runPlan(context.Background(), tools, submitPlanArgs{Steps: steps}, nil)
	if !res.Truncated {
		t.Error("expected Truncated=true for over-limit plan")
	}
	if len(res.Steps) != maxPlanSteps {
		t.Errorf("executed %d steps, want capped at %d", len(res.Steps), maxPlanSteps)
	}
}

// guard against a stray import if the test file evolves.
var _ = strings.TrimSpace

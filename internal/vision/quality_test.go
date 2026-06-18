package vision

import (
	"strings"
	"testing"
)

func TestEvaluateComplianceRedLine(t *testing.T) {
	q := &QualityChecker{threshold: 75}
	// Compliance fails → fail regardless of high scores.
	content := `{"compliance":{"pass":false,"violations":["露骨内容"]},"scores":{"subject_consistency":99,"character_appeal":99,"overall_quality":99},"total":99,"hints":"移除违规元素"}`
	v, err := q.evaluate(content)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v.Pass {
		t.Errorf("expected fail on compliance red line, got pass (total=%d)", v.Total)
	}
	if v.Compliant {
		t.Errorf("expected Compliant=false")
	}
	if len(v.Reasons) == 0 || v.Reasons[0] != "合规红线" {
		t.Errorf("expected 合规红线 reason, got %v", v.Reasons)
	}
}

func TestEvaluateThreshold(t *testing.T) {
	q := &QualityChecker{threshold: 75}
	tests := []struct {
		name     string
		content  string
		wantPass bool
	}{
		{
			name:     "above threshold passes",
			content:  `{"compliance":{"pass":true},"scores":{"subject_consistency":90,"character_appeal":80,"overall_quality":85,"canvas_fill":90},"total":85,"hints":""}`,
			wantPass: true,
		},
		{
			name:     "below threshold fails",
			content:  `{"compliance":{"pass":true},"scores":{"subject_consistency":50,"character_appeal":60,"overall_quality":40},"total":50,"hints":"主体更突出"}`,
			wantPass: false,
		},
		{
			name:     "derive total from dims when omitted",
			content:  `{"compliance":{"pass":true},"scores":{"subject_consistency":30,"character_appeal":30,"overall_quality":30},"hints":""}`,
			wantPass: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := q.evaluate(tc.content)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if v.Pass != tc.wantPass {
				t.Errorf("Pass=%v want %v (total=%d reasons=%v)", v.Pass, tc.wantPass, v.Total, v.Reasons)
			}
		})
	}
}

func TestEvaluateUnparseableDegradesToPass(t *testing.T) {
	q := &QualityChecker{threshold: 75}
	// No JSON at all → degrade to pass with an error so the caller proceeds.
	v, err := q.evaluate("the image looks fine to me")
	if err == nil {
		t.Errorf("expected an error for unparseable output")
	}
	if !v.Pass {
		t.Errorf("expected degrade-to-pass on parse failure")
	}
}

func TestExtractJSONFromFencedProse(t *testing.T) {
	in := "Sure, here is the result:\n```json\n{\"compliance\":{\"pass\":true},\"total\":80}\n```\nthanks"
	got := extractJSON(in)
	want := `{"compliance":{"pass":true},"total":80}`
	if got != want {
		t.Errorf("extractJSON = %q, want %q", got, want)
	}
}

func TestNewQualityCheckerDisabledWithoutCreds(t *testing.T) {
	if NewQualityChecker("", "", "", 0, 0) != nil {
		t.Errorf("expected nil checker without baseURL/apiKey")
	}
	if NewQualityChecker("https://x", "", "", 0, 0) != nil {
		t.Errorf("expected nil checker without apiKey")
	}
	qc := NewQualityChecker("https://x", "k", "", 0, 0)
	if qc == nil || !qc.Configured() {
		t.Fatalf("expected configured checker")
	}
	if qc.threshold != 75 {
		t.Errorf("expected default threshold 75, got %d", qc.threshold)
	}
	if qc.model != "doubao-seed-1-6-vision-250815" {
		t.Errorf("expected default model, got %q", qc.model)
	}
	if qc.keyElementsFidelityMin != 60 {
		t.Errorf("expected default key-elements-fidelity-min 60, got %d", qc.keyElementsFidelityMin)
	}
}

// TestEvaluateKeyElementsFidelityRedLine verifies a low key_elements_fidelity
// fails the verdict regardless of an otherwise high weighted total (asset_8825 /
// asset_c0fbd56 regression: missing subject/LOGO or rewritten text was masked).
func TestEvaluateKeyElementsFidelityRedLine(t *testing.T) {
	q := &QualityChecker{threshold: 75, keyElementsFidelityMin: 60}
	// High subject/appeal/quality/canvas → total 86, but text was rewritten (fidelity 30).
	content := `{"compliance":{"pass":true},"scores":{"subject_consistency":100,"character_appeal":95,"overall_quality":80,"canvas_fill":95,"key_elements_fidelity":30},"total":86,"hints":"保持LOGO文字不变"}`
	v, err := q.evaluate(content)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v.Pass {
		t.Errorf("expected fail on key-elements red line despite total=%d", v.Total)
	}
	if len(v.Reasons) == 0 || v.Reasons[0] != "核心主体/LOGO 缺失或文字被改写" {
		t.Errorf("expected key-elements reason, got %v", v.Reasons)
	}
}

// TestEvaluateKeyElementsFidelityDisabled verifies a zero threshold disables the
// red line (pre-feature behavior: high total passes even with low fidelity).
func TestEvaluateKeyElementsFidelityDisabled(t *testing.T) {
	q := &QualityChecker{threshold: 75, keyElementsFidelityMin: 0}
	content := `{"compliance":{"pass":true},"scores":{"subject_consistency":100,"character_appeal":95,"overall_quality":80,"canvas_fill":95,"key_elements_fidelity":10},"total":86,"hints":""}`
	v, err := q.evaluate(content)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !v.Pass {
		t.Errorf("expected pass when red line disabled (total=%d)", v.Total)
	}
}

func TestEvaluateAdAppealLowAddsHint(t *testing.T) {
	q := &QualityChecker{threshold: 75}
	// total=85 passes threshold, canvas_fill=90 passes red line, but ad_appeal=35
	content := `{"compliance":{"pass":true},"scores":{"subject_consistency":90,"character_appeal":85,"overall_quality":80,"canvas_fill":90,"key_elements_fidelity":80,"ad_appeal":35},"total":85,"hints":""}`
	v, err := q.evaluate(content)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !v.Pass {
		t.Errorf("expected pass (total=%d)", v.Total)
	}
	if !strings.Contains(v.Hints, "宣发吸引力") {
		t.Errorf("expected ad_appeal hint in Hints, got %q", v.Hints)
	}
}

func TestEvaluateAdAppealHighNoHint(t *testing.T) {
	q := &QualityChecker{threshold: 75}
	content := `{"compliance":{"pass":true},"scores":{"subject_consistency":90,"character_appeal":85,"overall_quality":80,"canvas_fill":90,"key_elements_fidelity":80,"ad_appeal":82},"total":85,"hints":""}`
	v, err := q.evaluate(content)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !v.Pass {
		t.Errorf("expected pass")
	}
	if strings.Contains(v.Hints, "宣发吸引力") {
		t.Errorf("did not expect ad_appeal hint when score is high, got %q", v.Hints)
	}
}

package generation

import (
	"strings"
	"testing"
)

// TestSafeBandFraction verifies the central safe-band占比 = genRatio/dstRatio,
// rounded to a 5% grid and clamped, symmetric for banners and strips.
func TestSafeBandFraction(t *testing.T) {
	cases := []struct {
		genW, genH, dstW, dstH int
		want                   float64
		label                  string
	}{
		{3, 1, 6, 1, 0.50, "3:1 gen → 6:1 target → 50%"},
		{3, 1, 4, 1, 0.75, "3:1 gen → 4:1 target → 75%"},
		{1, 3, 1, 6, 0.50, "1:3 gen → 1:6 target → 50% (strip symmetric)"},
		{1, 1, 1, 1, 1.0, "no reshape → full band"},
		{3, 1, 2, 1, 1.0, "target less extreme than gen → full band"},
		{0, 1, 6, 1, 0, "zero gen dim → 0"},
	}
	for _, c := range cases {
		got := safeBandFraction(c.genW, c.genH, c.dstW, c.dstH)
		if got != c.want {
			t.Errorf("[%s] safeBandFraction(%d,%d,%d,%d) = %v, want %v",
				c.label, c.genW, c.genH, c.dstW, c.dstH, got, c.want)
		}
	}
}

// TestExtremeRatioHint asserts the safe-zone cue carries the dynamic central
// percentage and the right axis wording, and is empty for ordinary ratios.
func TestExtremeRatioHint(t *testing.T) {
	// 6:1 banner, generated at the 3:1 clamp → central 50% 高度带, with the
	// hard-constraint framing (deleted edges, decapitation warning).
	h61 := extremeRatioHint(3008, 1008, 1008, 168)
	for _, sub := range []string{"中央 50%", "高度带", "DELETED", "throw-away", "decapitated"} {
		if !strings.Contains(h61, sub) {
			t.Errorf("6:1 hint missing %q: %s", sub, h61)
		}
	}
	if strings.Contains(h61, "宽度带") {
		t.Errorf("6:1 banner hint should not mention 宽度带: %s", h61)
	}

	// 4:1 banner → central 75% 高度带 (band grows as ratio relaxes).
	h41 := extremeRatioHint(3008, 1008, 1120, 280)
	if !strings.Contains(h41, "中央 75%") {
		t.Errorf("4:1 hint should say 中央 75%%: %s", h41)
	}

	// Extreme vertical strip → symmetric 中央 N% 宽度带 wording, left+right edges.
	hStrip := extremeRatioHint(1008, 3008, 168, 1008)
	for _, sub := range []string{"中央", "宽度带", "left", "right", "sliced"} {
		if !strings.Contains(hStrip, sub) {
			t.Errorf("strip hint missing %q: %s", sub, hStrip)
		}
	}

	// Unknown gen dims fall back to the 3:1 clamp → 6:1 still yields 中央 50%.
	hFallback := extremeRatioHint(0, 0, 1008, 168)
	if !strings.Contains(hFallback, "中央 50%") {
		t.Errorf("fallback (no gen dims) 6:1 hint should say 中央 50%%: %s", hFallback)
	}

	// Ordinary ratios get no safe-zone cue.
	for _, c := range []struct{ w, h int }{{1920, 1080}, {1080, 1080}, {1080, 1920}, {1280, 720}} {
		if got := extremeRatioHint(0, 0, c.w, c.h); got != "" {
			t.Errorf("ordinary %dx%d should have no extreme hint, got: %s", c.w, c.h, got)
		}
	}
}

// TestBuildPromptExtremeSafeZone verifies the safe-zone cue is wired through
// BuildPrompt for an extreme adapt target (GenWidth/GenHeight from the resolver),
// and absent for an ordinary target — without disturbing the THEME/PRESERVE segs.
func TestBuildPromptExtremeSafeZone(t *testing.T) {
	extreme := Slots{
		Kind:         EditAdaptPlatform,
		AssetTypeKey: "banner",
		TargetWidth:  1008,
		TargetHeight: 168,
		GenWidth:     3008,
		GenHeight:    1008,
		ThemeReport:  "兔子主体居中，品牌LOGO在左上",
	}
	got, err := BuildPrompt(extreme, nil)
	if err != nil {
		t.Fatalf("BuildPrompt extreme: %v", err)
	}
	for _, sub := range []string{"中央 50%", "THEME:", "PRESERVE:", "MODIFY:"} {
		if !strings.Contains(got, sub) {
			t.Errorf("extreme prompt missing %q:\n%s", sub, got)
		}
	}

	ordinary := Slots{
		Kind:         EditAdaptPlatform,
		AssetTypeKey: "cover",
		TargetWidth:  1280,
		TargetHeight: 720,
		GenWidth:     1456,
		GenHeight:    816,
	}
	got2, err := BuildPrompt(ordinary, nil)
	if err != nil {
		t.Fatalf("BuildPrompt ordinary: %v", err)
	}
	if strings.Contains(got2, "中央") || strings.Contains(got2, "SAFE ZONE") {
		t.Errorf("ordinary 16:9 prompt should not carry safe-zone cue:\n%s", got2)
	}
}

// TestReproportionHint verifies the copy-preserving recompose cue (Q2 fix).
func TestReproportionHint(t *testing.T) {
	tests := []struct {
		name     string
		srcW,srcH,dstW,dstH int
		sizeNote string
		wantHint bool
		wantCopy bool // true = hint should mention copy/text
	}{
		// 16:9 → 3:2: diff 15.6%, triggers cue with copy mention
		{"16:9 to 3:2 needs cue", 1920, 1080, 900, 600, "", true, true},
		// 16:9 → 3:2 but 无文案: cue fires but no copy mention
		{"16:9 to 3:2 no-copy placement", 1920, 1080, 900, 600, "无文案", true, false},
		// 16:9 → 1.8 (900x500): diff 1.2%, within tolerance, no cue
		{"16:9 to 1.8 close ratio", 1920, 1080, 900, 500, "", false, false},
		// Same ratio: no cue
		{"same ratio", 1920, 1080, 1280, 720, "", false, false},
		// Zero source: no cue
		{"zero source", 0, 0, 900, 600, "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reproportionHint(tc.srcW, tc.srcH, tc.dstW, tc.dstH, tc.sizeNote)
			hasHint := got != ""
			if hasHint != tc.wantHint {
				t.Errorf("reproportionHint(%d,%d,%d,%d,%q): got hint=%v, want %v (hint=%q)",
					tc.srcW, tc.srcH, tc.dstW, tc.dstH, tc.sizeNote, hasHint, tc.wantHint, got)
			}
			if tc.wantHint {
				hasCopy := strings.Contains(got, "copy") || strings.Contains(got, "text labels") || strings.Contains(got, "marketing copy")
				if hasCopy != tc.wantCopy {
					t.Errorf("reproportionHint copy mention: got %v, want %v (hint=%q)", hasCopy, tc.wantCopy, got)
				}
			}
		})
	}
}

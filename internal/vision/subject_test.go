package vision

import (
	"strings"
	"testing"
)

func TestParseSubject(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantErr  bool
		wantX    float64
		wantY    float64
		wantConf int
	}{
		{
			name:     "clean json",
			content:  `{"center_x":0.42,"center_y":0.61,"confidence":88}`,
			wantX:    0.42,
			wantY:    0.61,
			wantConf: 88,
		},
		{
			name:     "prose-wrapped with fence",
			content:  "好的，分析如下：\n```json\n{\"center_x\":0.5,\"center_y\":0.3,\"confidence\":70}\n```\n",
			wantX:    0.5,
			wantY:    0.3,
			wantConf: 70,
		},
		{
			name:     "out-of-range clamps to [0,1]",
			content:  `{"center_x":1.4,"center_y":-0.2,"confidence":55}`,
			wantX:    1.0,
			wantY:    0.0,
			wantConf: 55,
		},
		{
			name:    "no json",
			content: "我无法判断主体位置。",
			wantErr: true,
		},
		{
			name:    "malformed json",
			content: `{"center_x":0.5,`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			box, err := parseSubject(c.content)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got box %+v", box)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if box.CenterX != c.wantX || box.CenterY != c.wantY || box.Confidence != c.wantConf {
				t.Errorf("got (%v,%v,%d), want (%v,%v,%d)",
					box.CenterX, box.CenterY, box.Confidence, c.wantX, c.wantY, c.wantConf)
			}
		})
	}
}

func TestNewSubjectDetectorNilWhenUnconfigured(t *testing.T) {
	if d := NewSubjectDetector("", "key", "model"); d != nil {
		t.Error("empty baseURL should yield nil detector")
	}
	if d := NewSubjectDetector("http://x", "", "model"); d != nil {
		t.Error("empty apiKey should yield nil detector")
	}
	if d := NewSubjectDetector("http://x", "key", ""); d == nil || !d.Configured() {
		t.Error("configured detector should be non-nil and Configured()")
	}
}

func TestParseSubjects(t *testing.T) {
	t.Run("clean list with fence and prose", func(t *testing.T) {
		content := "这是结果：\n```json\n{\"subjects\":[{\"desc\":\"左侧红甲战士\",\"box\":{\"x\":0.1,\"y\":0.2,\"w\":0.3,\"h\":0.5}},{\"desc\":\"右上角LOGO\",\"box\":{\"x\":0.8,\"y\":0.05,\"w\":0.15,\"h\":0.1}}]}\n```"
		subs, err := parseSubjects(content)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 2 {
			t.Fatalf("want 2 subjects, got %d", len(subs))
		}
		if subs[0].Desc != "左侧红甲战士" || subs[0].Box.W != 0.3 {
			t.Errorf("unexpected first subject %+v", subs[0])
		}
	})
	t.Run("empty list", func(t *testing.T) {
		subs, err := parseSubjects(`{"subjects":[]}`)
		if err != nil || len(subs) != 0 {
			t.Fatalf("want empty, got %v err=%v", subs, err)
		}
	})
	t.Run("drops empty desc and clamps box", func(t *testing.T) {
		subs, err := parseSubjects(`{"subjects":[{"desc":"","box":{"x":0,"y":0,"w":1,"h":1}},{"desc":"主体","box":{"x":-0.5,"y":2,"w":0.4,"h":0.4}}]}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 1 {
			t.Fatalf("want 1 (empty desc dropped), got %d", len(subs))
		}
		if subs[0].Box.X != 0 || subs[0].Box.Y != 1 {
			t.Errorf("box not clamped: %+v", subs[0].Box)
		}
	})
	t.Run("caps at maxDetectedSubjects (5)", func(t *testing.T) {
		// Seven candidates → only the first 5 (by model order) are kept.
		content := `{"subjects":[` +
			`{"desc":"s1","box":{"x":0,"y":0,"w":0.1,"h":0.1}},` +
			`{"desc":"s2","box":{"x":0,"y":0,"w":0.1,"h":0.1}},` +
			`{"desc":"s3","box":{"x":0,"y":0,"w":0.1,"h":0.1}},` +
			`{"desc":"s4","box":{"x":0,"y":0,"w":0.1,"h":0.1}},` +
			`{"desc":"s5","box":{"x":0,"y":0,"w":0.1,"h":0.1}},` +
			`{"desc":"s6","box":{"x":0,"y":0,"w":0.1,"h":0.1}},` +
			`{"desc":"s7","box":{"x":0,"y":0,"w":0.1,"h":0.1}}]}`
		subs, err := parseSubjects(content)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != maxDetectedSubjects {
			t.Fatalf("want capped at %d, got %d", maxDetectedSubjects, len(subs))
		}
		if maxDetectedSubjects != 5 {
			t.Errorf("maxDetectedSubjects should be 5, got %d", maxDetectedSubjects)
		}
	})
	t.Run("no json", func(t *testing.T) {
		if _, err := parseSubjects("no json here"); err == nil {
			t.Error("expected error")
		}
	})
}

func TestParseSubjectMasks(t *testing.T) {
	// A valid 1×1 PNG as a data URI, so mask decoding yields real bytes.
	const png1x1 = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	near := func(a, b float64) bool {
		d := a - b
		return d < 1e-9 && d > -1e-9
	}

	t.Run("box_2d→box conversion + mask decode", func(t *testing.T) {
		// box_2d = [ymin,xmin,ymax,xmax] = [200,100,600,400] in 0..1000.
		content := `{"subjects":[{"desc":"左侧战士","box_2d":[200,100,600,400],"mask":"` + png1x1 + `"}]}`
		subs, err := parseSubjectMasks(content)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 1 {
			t.Fatalf("want 1 subject, got %d", len(subs))
		}
		s := subs[0]
		if !near(s.Box.X, 0.1) || !near(s.Box.Y, 0.2) || !near(s.Box.W, 0.3) || !near(s.Box.H, 0.4) {
			t.Errorf("box_2d conversion wrong: %+v", s.Box)
		}
		if len(s.Mask) == 0 {
			t.Error("mask data URI should decode to bytes")
		}
	})
	t.Run("label fallback + missing mask kept as nil", func(t *testing.T) {
		content := `{"subjects":[{"label":"顶部主标题","box_2d":[0,0,100,200]}]}`
		subs, err := parseSubjectMasks(content)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 1 || subs[0].Desc != "顶部主标题" {
			t.Fatalf("label should fill desc when desc empty: %+v", subs)
		}
		if subs[0].Mask != nil {
			t.Error("a subject with no mask must keep Mask=nil (caller falls back to opaque crop)")
		}
	})
	t.Run("drops empty desc and degenerate box", func(t *testing.T) {
		content := `{"subjects":[{"desc":"","box_2d":[0,0,100,100]},{"desc":"x","box_2d":[500,500,500,500]}]}`
		subs, err := parseSubjectMasks(content)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 0 {
			t.Fatalf("want 0 (empty desc dropped, zero-area box dropped), got %d: %+v", len(subs), subs)
		}
	})
	t.Run("caps at maxDetectedSubjects (5)", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(`{"subjects":[`)
		for i := 0; i < 7; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"desc":"s","box_2d":[0,0,100,100]}`)
		}
		b.WriteString(`]}`)
		subs, err := parseSubjectMasks(b.String())
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != maxDetectedSubjects {
			t.Fatalf("want capped at %d, got %d", maxDetectedSubjects, len(subs))
		}
	})
	t.Run("no json", func(t *testing.T) {
		if _, err := parseSubjectMasks("nope"); err == nil {
			t.Error("expected error")
		}
	})
}

// TestSubjectsPromptScopesToTwoKinds locks the detection scope to people +
// marketing copy only — LOGO / props / scenery must be steered to the background.
func TestSubjectsPromptScopesToTwoKinds(t *testing.T) {
	for _, want := range []string{"角色/人物", "宣发文案", "品牌 LOGO", "道具", "保留在背景层", "最多列出 5 个"} {
		if !strings.Contains(subjectsPrompt, want) {
			t.Errorf("subjectsPrompt missing %q", want)
		}
	}
	// Must NOT invite the model to split out the hero object/prop as its own layer.
	if strings.Contains(subjectsPrompt, "核心主体或显著道具") {
		t.Error("subjectsPrompt should no longer list 核心主体/道具 as a separable subject")
	}
}

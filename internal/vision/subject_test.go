package vision

import "testing"

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

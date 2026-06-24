package agent

import "testing"

// TestClassifyIntentCompound verifies the deterministic detection of compound
// multi-step requests (which should nudge the model toward submit_plan), while
// single-action requests must NOT be flagged compound.
func TestClassifyIntentCompound(t *testing.T) {
	compound := []struct {
		name string
		text string
	}{
		{"换角色+切尺寸(连接词)", "[工作区: 图1=a1(上传), 图2=a2(上传)] 把图2 的人物换成图1 的角色，然后做成 iOS 4 个尺寸"},
		{"两个强动作", "[工作区: 图1=a1(上传)] 帮我换个背景再切成各平台尺寸"},
		{"找图+生视频", "搜一张赛博朋克的图然后生成一段视频"},
		{"做成多个尺寸(无连接词)", "[工作区: 图1=a1(上传)] 把角色换成机甲战士，做成几个尺寸"},
	}
	for _, c := range compound {
		t.Run(c.name, func(t *testing.T) {
			h := ClassifyIntent(c.text)
			if !h.Compound {
				t.Errorf("text %q should be compound, got Compound=false (labels=%v)", c.text, h.Labels)
			}
		})
	}

	single := []struct {
		name string
		text string
	}{
		{"只换背景", "[工作区: 图1=a1(上传)] 帮我换个背景，改成黄昏的城市"},
		{"只切尺寸", "[工作区: 图1=a1(上传)] 帮我切成各平台的尺寸"},
		{"只搜图", "帮我搜一张赛博朋克城市的图"},
		{"闲聊不命中", "今天天气怎么样"},
	}
	for _, c := range single {
		t.Run(c.name, func(t *testing.T) {
			h := ClassifyIntent(c.text)
			if h.Compound {
				t.Errorf("text %q should NOT be compound, got Compound=true (labels=%v)", c.text, h.Labels)
			}
		})
	}
}

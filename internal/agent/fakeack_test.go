package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestLooksLikeFakeExecAck(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  bool
	}{
		{
			name:  "observed fake ack",
			reply: "好的，正在处理图1的背景修改，产物会出现在左侧工作区。",
			want:  true,
		},
		{
			name:  "observed fake ack with words between 正在 and verb",
			reply: "好的，正在按你的要求处理这张图，产物会很快出现在左侧工作区。",
			want:  true,
		},
		{
			name:  "fake ack generate variant",
			reply: "马上为你生成，稍后查看工作区即可。",
			want:  true,
		},
		{
			name:  "capability description (no progress verb)",
			reply: "我是你的游戏宣发素材生成助手，能用文字帮你快速生成图片、剪辑视频或优化素材。",
			want:  false,
		},
		{
			name:  "plain chat",
			reply: "你好，有什么可以帮你的吗？",
			want:  false,
		},
		{
			name:  "clarifying question",
			reply: "你想把背景换成什么颜色或场景呢？",
			want:  false,
		},
		{
			name:  "progress verb but no artifact reference",
			reply: "正在思考你的问题。",
			want:  false,
		},
		{
			name:  "artifact reference but no progress verb",
			reply: "产物会显示在左侧工作区，你可以下载或打包。",
			want:  false,
		},
		{
			name:  "empty",
			reply: "",
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeFakeExecAck(tc.reply); got != tc.want {
				t.Errorf("looksLikeFakeExecAck(%q) = %v, want %v", tc.reply, got, tc.want)
			}
		})
	}
}

func TestShouldRetryFakeAck(t *testing.T) {
	const maxAttempts = 2
	fakeAck := "好的，正在处理图1的背景修改，产物会出现在左侧工作区。"
	realReply := "已完成，图片已生成。"
	cases := []struct {
		name      string
		attempt   int
		toolCalls int
		reply     string
		want      bool
	}{
		{name: "first attempt, fake ack, no tools -> retry", attempt: 1, toolCalls: 0, reply: fakeAck, want: true},
		{name: "tools were called -> no retry", attempt: 1, toolCalls: 1, reply: fakeAck, want: false},
		{name: "not a fake ack -> no retry", attempt: 1, toolCalls: 0, reply: realReply, want: false},
		{name: "last attempt -> no retry even if fake", attempt: 2, toolCalls: 0, reply: fakeAck, want: false},
		{name: "empty reply -> no retry", attempt: 1, toolCalls: 0, reply: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetryFakeAck(tc.attempt, maxAttempts, tc.toolCalls, tc.reply); got != tc.want {
				t.Errorf("shouldRetryFakeAck(attempt=%d, tools=%d, %q) = %v, want %v",
					tc.attempt, tc.toolCalls, tc.reply, got, tc.want)
			}
		})
	}
}

func TestRemediationAction(t *testing.T) {
	whitelistMissing := IntentHint{Whitelisted: true, MissingKeyParam: true, Labels: []string{"换背景"}}
	whitelistOK := IntentHint{Whitelisted: true, MissingKeyParam: false, Labels: []string{"换背景"}}
	offWhitelist := IntentHint{Whitelisted: false}
	fakeAck := "好的，正在处理图1的背景修改，产物会出现在左侧工作区。"
	cases := []struct {
		name         string
		toolCalls    int
		cancelled    bool
		capsuleAsked bool
		replyEmpty   bool
		reply        string
		hint         IntentHint
		want         remediation
	}{
		{name: "whitelist + missing key param -> clarify", toolCalls: 0, replyEmpty: true, hint: whitelistMissing, want: remediateClarify},
		{name: "off-whitelist + empty reply -> refuse", toolCalls: 0, replyEmpty: true, hint: offWhitelist, want: remediateRefuse},
		{name: "tool already called -> none", toolCalls: 1, replyEmpty: true, hint: whitelistMissing, want: remediateNone},
		{name: "cancelled turn -> none", toolCalls: 0, cancelled: true, replyEmpty: true, hint: whitelistMissing, want: remediateNone},
		{name: "capsule already asked -> none", toolCalls: 0, capsuleAsked: true, replyEmpty: true, hint: whitelistMissing, want: remediateNone},
		{name: "whitelist with key param present -> none", toolCalls: 0, replyEmpty: true, hint: whitelistOK, want: remediateNone},
		{name: "off-whitelist but model gave a body -> none", toolCalls: 0, replyEmpty: false, hint: offWhitelist, want: remediateNone},
		// fake-exec ack survived the retry budget (zero tool calls, non-empty fake prose).
		{name: "fake ack + off-whitelist -> honest fail", toolCalls: 0, replyEmpty: false, reply: fakeAck, hint: offWhitelist, want: remediateHonestFail},
		{name: "fake ack + whitelist ok -> honest fail", toolCalls: 0, replyEmpty: false, reply: fakeAck, hint: whitelistOK, want: remediateHonestFail},
		{name: "fake ack + whitelist missing param -> clarify", toolCalls: 0, replyEmpty: false, reply: fakeAck, hint: whitelistMissing, want: remediateClarify},
		{name: "fake ack but tool was called -> none", toolCalls: 1, replyEmpty: false, reply: fakeAck, hint: offWhitelist, want: remediateNone},
		{name: "fake ack but cancelled -> none", toolCalls: 0, cancelled: true, replyEmpty: false, reply: fakeAck, hint: offWhitelist, want: remediateNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remediationAction(tc.toolCalls, tc.cancelled, tc.capsuleAsked, tc.replyEmpty, tc.reply, tc.hint)
			if got != tc.want {
				t.Errorf("remediationAction = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRemediationClarifyBuildsOptions(t *testing.T) {
	hint := IntentHint{Whitelisted: true, MissingKeyParam: true, Labels: []string{"换背景"}}
	q, opts := remediationClarify(hint)
	if q == "" {
		t.Fatal("expected a non-empty clarify question")
	}
	if len(opts) < 2 || len(opts) > 4 {
		t.Fatalf("expected 2-4 options, got %d", len(opts))
	}
	for i, o := range opts {
		if o.Label == "" || o.Value == "" {
			t.Errorf("option[%d] missing label/value: %+v", i, o)
		}
	}
}

func TestLooksLikeMissingOutputComplaint(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{name: "video not generated", text: "你怎么没生成那个视频啊", want: true},
		{name: "icon not made", text: "icon 没做出来啊", want: true},
		{name: "image missing", text: "刚才那张图没看到，是不是没生成", want: true},
		{name: "crop failed", text: "裁剪失败了吗？工作区没有结果", want: true},
		{name: "forward request not a complaint", text: "再生成一张竖版的", want: false},
		{name: "plain new request", text: "帮我把背景换成夜晚", want: false},
		{name: "negation without output word", text: "没事了，谢谢", want: false},
		{name: "output word without negation", text: "这个视频很好看", want: false},
		{name: "empty", text: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeMissingOutputComplaint(tc.text); got != tc.want {
				t.Errorf("looksLikeMissingOutputComplaint(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestPrevTurnHadToolCall(t *testing.T) {
	idx := 0
	withTool := schema.AssistantMessage("", []schema.ToolCall{{ID: "t1", Index: &idx, Function: schema.FunctionCall{Name: "edit_image"}}})
	proseOnly := schema.AssistantMessage("好的，正在处理，产物会出现在左侧工作区。", nil)
	user := schema.UserMessage("换个背景")
	sys := schema.SystemMessage("you are ...")

	cases := []struct {
		name string
		msgs []*schema.Message
		want bool
	}{
		{name: "no prior assistant", msgs: []*schema.Message{sys, user}, want: false},
		{name: "last assistant called a tool", msgs: []*schema.Message{sys, user, withTool}, want: true},
		{name: "last assistant prose only", msgs: []*schema.Message{sys, user, proseOnly}, want: false},
		{name: "scans past trailing user message", msgs: []*schema.Message{sys, withTool, user}, want: true},
		{name: "picks most recent assistant", msgs: []*schema.Message{sys, withTool, user, proseOnly}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prevTurnHadToolCall(tc.msgs); got != tc.want {
				t.Errorf("prevTurnHadToolCall = %v, want %v", got, tc.want)
			}
		})
	}
}

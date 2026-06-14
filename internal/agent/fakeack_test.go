package agent

import "testing"

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
	cases := []struct {
		name         string
		toolCalls    int
		cancelled    bool
		capsuleAsked bool
		replyEmpty   bool
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remediationAction(tc.toolCalls, tc.cancelled, tc.capsuleAsked, tc.replyEmpty, tc.hint)
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

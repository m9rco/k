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

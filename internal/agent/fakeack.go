package agent

import "regexp"

// A weak chat model sometimes "confirms" an action in prose ("好的，正在处理图1
// 的背景修改，产物会出现在左侧工作区。") without ever emitting the tool call that
// would actually do it. The system prompt forbids this, but small models ignore
// the rule. looksLikeFakeExecAck detects that pattern so the turn can self-correct
// (retry with a stern instruction) instead of silently leaving the workspace empty.
//
// Detection is deliberately conservative — it fires only when BOTH a
// progress/execution verb AND a product/workspace reference are present, the exact
// shape of the observed fake acks. This avoids misfiring on:
//   - capability descriptions ("能用文字帮你快速生成图片…") — no progress verb
//   - plain chat / clarifying questions — neither signal
//
// Pairing the two signals keeps false positives near zero, which matters because a
// false positive triggers a wasted retry.
var (
	fakeAckProgressRe = regexp.MustCompile(`正在(处理|生成|制作|修改|裁剪|替换|为你|帮你)|(这就|马上|立刻|稍等|稍候)(为你|帮你|开始)?(处理|生成|制作)`)
	fakeAckArtifactRe = regexp.MustCompile(`工作区|产物|左侧|生成好|处理好|稍后(查看|出现|展示)`)
)

// looksLikeFakeExecAck reports whether reply reads as an execution confirmation
// (a promise that work is underway) rather than the result of an actual tool call.
// Callers should only consult it when the turn made zero tool calls.
func looksLikeFakeExecAck(reply string) bool {
	return fakeAckProgressRe.MatchString(reply) && fakeAckArtifactRe.MatchString(reply)
}

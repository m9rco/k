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
	// "正在……处理/生成/搜索/下载/…" — allow a few chars between 正在 and the verb.
	fakeAckProgressRe = regexp.MustCompile(`正在.{0,10}(处理|生成|制作|修改|裁剪|替换|绘制|制图|搜索|下载|查找|为你|帮你)|(这就|马上|立刻|稍等|稍候).{0,8}(处理|生成|制作|搜索|下载)`)
	fakeAckArtifactRe = regexp.MustCompile(`工作区|产物|左侧|生成好|处理好|稍后(查看|出现|展示)`)
)

// looksLikeFakeExecAck reports whether reply reads as an execution confirmation
// (a promise that work is underway) rather than the result of an actual tool call.
// Callers should only consult it when the turn made zero tool calls.
func looksLikeFakeExecAck(reply string) bool {
	return fakeAckProgressRe.MatchString(reply) && fakeAckArtifactRe.MatchString(reply)
}

// shouldRetryFakeAck decides whether a finished attempt warrants a self-correcting
// retry: the model made no tool call, its reply looks like a fake execution ack,
// and an attempt budget remains. Pulled out as a pure function so the decision
// table is unit-testable without standing up a live model.
func shouldRetryFakeAck(attempt, maxAttempts, toolCalls int, reply string) bool {
	return toolCalls == 0 && attempt < maxAttempts && looksLikeFakeExecAck(reply)
}

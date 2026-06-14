package agent

import "regexp"

// fakeAckCorrection is the stern instruction appended before a self-correcting
// retry when the model only faked execution in prose. It tells the model its
// previous turn did not actually do anything and demands a real tool call now.
const fakeAckCorrection = "你刚才只用文字描述了「正在处理」，但并没有真正调用任何工具，工作区没有任何产物。请立即真正调用对应的工具来完成这个请求；如果确实缺少必要信息（如不知道操作哪张图），则改用 clarify_intent 询问。不要再只用文字假装执行。"

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

// remediation is the post-turn fallback action when the model made no tool call.
type remediation int

const (
	remediateNone    remediation = iota // leave the turn as-is (normal reply / empty)
	remediateClarify                    // ask a structured clarify question
	remediateRefuse                     // deterministic polite refusal
)

// remediationAction decides how to recover a turn that ended with zero tool
// calls, using the deterministic pre-classification. It is a pure decision table
// (unit-testable without a live model):
//   - a cancelled turn, a turn that already called a tool, or one that already
//     produced a clarify capsule is left untouched (remediateNone),
//   - a whitelisted intent missing a key param → remediateClarify (so the turn
//     never ends empty when we know what the user wanted),
//   - a non-whitelisted request that produced no body → remediateRefuse (polite
//     capability list, no extra model round-trip).
func remediationAction(toolCalls int, cancelled, capsuleAsked, replyEmpty bool, hint IntentHint) remediation {
	if toolCalls > 0 || cancelled || capsuleAsked {
		return remediateNone
	}
	switch {
	case hint.Whitelisted && hint.MissingKeyParam:
		return remediateClarify
	case !hint.Whitelisted && replyEmpty:
		return remediateRefuse
	default:
		return remediateNone
	}
}

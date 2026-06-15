package agent

import (
	"regexp"

	"github.com/cloudwego/eino/schema"
)

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

// A user whose previous turn was faked (the model promised work but never called
// a tool) often comes back to complain: "你怎么没生成那个视频", "icon 没做出来啊",
// "刚才那张图没看到". missingOutputComplaintRe detects that pattern so the next turn
// can inject a remediation hint that nudges the model to ACTUALLY call the tool
// this time, instead of (again) just replying in prose.
//
// Like looksLikeFakeExecAck, detection pairs two signals to keep false positives
// low: a negation/absence cue AND a production/output cue. A plain "没事了" or a
// forward request like "再生成一张" carries only one (or neither) and won't fire.
var (
	// 没/未/怎么没/还没 + (有)? — negation of completion, plus a few colloquial forms.
	missingComplaintNegRe = regexp.MustCompile(`(没有|没看到|没生成|没做|没出来|没跑|未生成|未完成|怎么没|还没|哪里|哪儿|去哪|失败了|没成功|没反应)`)
	// production / artifact words the complaint refers to.
	missingComplaintOutRe = regexp.MustCompile(`(视频|图|icon|图标|产物|结果|素材|裁剪|动效|生成|做出来|搞出来)`)
)

// looksLikeMissingOutputComplaint reports whether userText reads as the user
// telling us a previous operation never actually happened / produced nothing.
// It is deliberately conservative (two paired signals) so normal forward requests
// ("再来一张", "换个背景") don't misfire. Callers MUST additionally confirm the
// previous assistant turn made zero tool calls before acting (prevTurnHadToolCall),
// which narrows the trigger to genuine fake-exec follow-ups.
func looksLikeMissingOutputComplaint(userText string) bool {
	return missingComplaintNegRe.MatchString(userText) && missingComplaintOutRe.MatchString(userText)
}

// prevTurnHadToolCall reports whether the most recent assistant message in msgs
// carried any tool call. It scans from the end and inspects the first assistant
// message it finds (tool result messages and the just-appended user message are
// skipped). Returns false when there is no prior assistant turn. Used to gate the
// missing-output remediation hint: a complaint only warrants a nudge when the
// turn the user is complaining about in fact called nothing.
func prevTurnHadToolCall(msgs []*schema.Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.Assistant {
			return len(msgs[i].ToolCalls) > 0
		}
	}
	return false
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
	remediateNone       remediation = iota // leave the turn as-is (normal reply / empty)
	remediateClarify                       // ask a structured clarify question
	remediateRefuse                        // deterministic polite refusal
	remediateHonestFail                    // replace a leftover fake-exec ack with honest feedback
)

// honestFailMessage replaces a fake-execution ack that survived the retry budget.
// The model promised work was underway but never called a tool, and a self-
// correcting retry did not fix it. Rather than letting the false confirmation
// pose as a successful reply, we tell the user the truth: nothing ran, the
// workspace is unchanged, and what to do next. Tone follows CLAUDE.md — plain,
// no hype, points at the next step.
const honestFailMessage = "抱歉，这个操作我没能真正执行，工作区里还没有对应的产物。可能是我没正确发起调用。请再说一次你的需求，或补充一下要处理哪张图、想要的效果，我会立即重新执行。"

// remediationAction decides how to recover a turn that ended with zero tool
// calls, using the deterministic pre-classification. It is a pure decision table
// (unit-testable without a live model):
//   - a cancelled turn, a turn that already called a tool, or one that already
//     produced a clarify capsule is left untouched (remediateNone),
//   - a leftover fake-exec ack (model promised work, called nothing, retry did
//     not fix it): if the intent is whitelisted but missing a key param →
//     remediateClarify (ask for it); otherwise → remediateHonestFail (replace the
//     false confirmation with honest feedback instead of letting it pose as done),
//   - a whitelisted intent missing a key param → remediateClarify (so the turn
//     never ends empty when we know what the user wanted),
//   - a non-whitelisted request that produced no body → remediateRefuse (polite
//     capability list, no extra model round-trip).
//
// The fake-ack branch is checked before the generic ones so a non-empty fake
// confirmation is never mistaken for a real reply (replyEmpty would be false).
func remediationAction(toolCalls int, cancelled, capsuleAsked, replyEmpty bool, reply string, hint IntentHint) remediation {
	if toolCalls > 0 || cancelled || capsuleAsked {
		return remediateNone
	}
	// Note: when a "[上次产物: 图N]" annotation was injected upstream,
	// hasWorkspaceImage treats it as an available image, so ClassifyIntent leaves
	// MissingKeyParam=false and the clarify branches below never fire. That is
	// intentional (sticky-last-output / clarify-recent-context): with a known
	// last output we default to editing it rather than asking which image.
	switch {
	case looksLikeFakeExecAck(reply) && hint.Whitelisted && hint.MissingKeyParam:
		return remediateClarify
	case looksLikeFakeExecAck(reply):
		return remediateHonestFail
	case hint.Whitelisted && hint.MissingKeyParam:
		return remediateClarify
	case !hint.Whitelisted && replyEmpty:
		return remediateRefuse
	default:
		return remediateNone
	}
}

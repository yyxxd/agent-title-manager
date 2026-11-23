package engine

import "strings"

const (
	decisionManual        = "manual_locked"
	decisionProtected     = "unknown_protected"
	decisionModelKeep     = "model_keep"
	decisionKeepGoal      = "model_keep_goal"
	decisionKeepProgress  = "model_keep_progress"
	decisionKeepUnknown   = "model_keep_unspecified"
	decisionEmpty         = "empty_title"
	decisionConfirmation  = "confirmation_only"
	decisionThrottle      = "throttled"
	decisionDuplicate     = "duplicate_turn"
	decisionNoContent     = "no_content"
	decisionCollision     = "title_collision"
	decisionSame          = "same_title"
	decisionOrcaProtected = "orca_title_protected"
)

func (s session) protection() string {
	if s.protectionReason == decisionManual {
		return decisionManual
	}
	if s.manual {
		return decisionProtected
	}
	return ""
}

func keptDecision(base string, r modelResult) string {
	switch {
	case r.decision.Action == "rename":
		return decisionSame
	case base == "":
		return decisionEmpty
	default:
		return classifyKeepReason(r.decision.Reason)
	}
}

func classifyKeepReason(reason string) string {
	// 仅识别完整的简短理由。含有会话片段、额外解释或不支持的表述时保留未知。
	reason = strings.TrimRight(strings.ToLower(strings.TrimSpace(reason)), ".。")
	switch reason {
	case "延续原标题目标", "延续原标题的目标", "延续原目标", "仍在延续原标题的目标", "仍在原有主线", "原标题仍准确描述当前目标",
		"目标未变", "主线未变", "the goal is unchanged", "the original title still describes the goal":
		return decisionKeepGoal
	case "只是已有工作的步骤或进展", "只是已有工作的步骤/进展", "只是原任务的后续步骤", "仅为已有工作的后续步骤",
		"仅是原任务的进展", "只是原有目标下的进展", "这是原任务的测试步骤",
		"this is progress on the existing task", "this is a step in the existing task":
		return decisionKeepProgress
	default:
		return decisionKeepUnknown
	}
}

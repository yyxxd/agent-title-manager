package engine

const (
	decisionManual       = "manual_locked"
	decisionProtected    = "unknown_protected"
	decisionModelKeep    = "model_keep"
	decisionEmpty        = "empty_title"
	decisionConfirmation = "confirmation_only"
	decisionThrottle     = "throttled"
	decisionDuplicate    = "duplicate_turn"
	decisionNoContent    = "no_content"
	decisionCollision    = "title_collision"
	decisionSame         = "same_title"
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
		return decisionModelKeep
	}
}

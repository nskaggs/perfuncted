package accessibility

import "time"

// ActionReceipt records the exact semantic target and AT-SPI
// action selected by InvokeSemanticAction. The action is never replaced by a
// physical input fallback.
type ActionReceipt struct {
	Node         Node               `json:"node"`
	Action       Action             `json:"action"`
	Operation    string             `json:"operation,omitempty"`
	Mechanism    string             `json:"mechanism"`
	Generation   uint64             `json:"generation"`
	Dispatch     DispatchOutcome    `json:"dispatch"`
	DispatchedAt time.Time          `json:"dispatchedAt,omitempty"`
	Outcome      ActionOutcomeProof `json:"outcome"`
}

// ActionOutcomeStatus describes independent observation after dispatch.
type ActionOutcomeStatus string

const (
	// ActionOutcomeNotObserved means no independent postcondition was evaluated.
	ActionOutcomeNotObserved ActionOutcomeStatus = "not-observed"
	// ActionOutcomeVerified means the caller's postcondition became true.
	ActionOutcomeVerified ActionOutcomeStatus = "verified"
	// ActionOutcomeNotVerified means the postcondition did not complete successfully.
	ActionOutcomeNotVerified ActionOutcomeStatus = "not-verified"
)

// ActionOutcomeProof records whether the caller's independent condition was
// observed after the provider acknowledged dispatch.
type ActionOutcomeProof struct {
	Status     ActionOutcomeStatus `json:"status"`
	Condition  string              `json:"condition,omitempty"`
	ObservedAt time.Time           `json:"observedAt,omitempty"`
}

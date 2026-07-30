package continuity

import "errors"

var (
	ErrReplayConfig   = errors.New("invalid replay journal configuration")
	ErrReplayBudget   = errors.New("replay journal budget exceeded")
	ErrReplayOffset   = errors.New("invalid replay journal offset")
	ErrReplayClosed   = errors.New("replay journal closed")
	ErrReplayOverflow = errors.New("replay journal offset exhausted")

	ErrLeaseTicketConfig   = errors.New("invalid lease ticket registry configuration")
	ErrLeaseTicketBinding  = errors.New("invalid lease ticket binding")
	ErrLeaseTicketCapacity = errors.New("lease ticket registry capacity exceeded")
	ErrLeaseTicketEntropy  = errors.New("lease ticket entropy unavailable")
	ErrLeaseTicketInvalid  = errors.New("invalid lease ticket")
	ErrLeaseTicketClosed   = errors.New("lease ticket registry closed")

	ErrFlowRegistryConfig = errors.New("invalid logical flow registry configuration")
	ErrFlowRegistryClosed = errors.New("logical flow registry closed")
	ErrFlowCapacity       = errors.New("logical flow registry capacity exceeded")
	ErrFlowBinding        = errors.New("logical flow authentication binding mismatch")
	ErrFlowConflict       = errors.New("logical flow lease epoch conflict")
	ErrFlowNotFound       = errors.New("logical flow not found")
	ErrFlowEntropy        = errors.New("logical flow id entropy unavailable")

	ErrResumableConfig = errors.New("invalid resumable stream configuration")
	ErrResumableClosed = errors.New("resumable stream closed")
	ErrResumableState  = errors.New("invalid resumable stream state")
)

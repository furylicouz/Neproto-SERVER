package windowsclient

type State string

const (
	StateStopped       State = "stopped"
	StateConnecting    State = "connecting"
	StateConnected     State = "connected"
	StateDisconnecting State = "disconnecting"
	StateFailed        State = "failed"
)

func CanTransition(from, to State) bool {
	switch from {
	case StateStopped:
		return to == StateConnecting
	case StateConnecting:
		return to == StateConnected || to == StateFailed || to == StateDisconnecting
	case StateConnected:
		return to == StateDisconnecting || to == StateFailed
	case StateDisconnecting:
		return to == StateStopped || to == StateFailed
	case StateFailed:
		return to == StateConnecting || to == StateStopped
	default:
		return false
	}
}

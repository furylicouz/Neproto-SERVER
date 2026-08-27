package windowsclient

const (
	// MaxIPCClients bounds concurrently executing local pipe requests.
	MaxIPCClients = 16

	// PipeSecurityDescriptor denies anonymous and network logons before granting
	// full control to SYSTEM/Administrators and read/write only to local
	// interactive users. The owner is deliberately inherited from the creating
	// token: forcing SYSTEM as owner makes legitimate non-SYSTEM verification
	// fail with ERROR_INVALID_OWNER. go-winio additionally creates every
	// instance with FILE_PIPE_REJECT_REMOTE_CLIENTS.
	PipeSecurityDescriptor = "D:P" +
		"(D;;GA;;;AN)" +
		"(D;;GA;;;NU)" +
		"(A;;GA;;;SY)" +
		"(A;;GA;;;BA)" +
		"(A;;GRGW;;;IU)"
)

package windowsclient

const (
	// MaxIPCClients bounds concurrently executing local pipe requests.
	MaxIPCClients = 16

	// PipeSecurityDescriptor denies anonymous and network logons before granting
	// full control to SYSTEM/Administrators and read/write only to local
	// interactive users. go-winio additionally creates every instance with
	// FILE_PIPE_REJECT_REMOTE_CLIENTS.
	PipeSecurityDescriptor = "O:SYG:SYD:P" +
		"(D;;GA;;;AN)" +
		"(D;;GA;;;NU)" +
		"(A;;GA;;;SY)" +
		"(A;;GA;;;BA)" +
		"(A;;GRGW;;;IU)"
)

package windowsclient

import (
	"strings"
	"testing"
)

func TestPipeSecurityPolicyIsLocalInteractiveLeastPrivilege(t *testing.T) {
	if MaxIPCClients != 16 {
		t.Fatalf("client bound=%d", MaxIPCClients)
	}
	for _, required := range []string{
		"(D;;GA;;;AN)",
		"(D;;GA;;;NU)",
		"(A;;GA;;;SY)",
		"(A;;GA;;;BA)",
		"(A;;GRGW;;;IU)",
	} {
		if !strings.Contains(PipeSecurityDescriptor, required) {
			t.Fatalf("missing %s in %s", required, PipeSecurityDescriptor)
		}
	}
	for _, forbidden := range []string{";;;AU)", ";;;WD)", "(A;;GA;;;IU)"} {
		if strings.Contains(PipeSecurityDescriptor, forbidden) {
			t.Fatalf("broad grant %s in %s", forbidden, PipeSecurityDescriptor)
		}
	}
	if strings.Index(PipeSecurityDescriptor, "(D;;GA;;;AN)") >
		strings.Index(PipeSecurityDescriptor, "(A;;GRGW;;;IU)") {
		t.Fatal("deny ACEs must precede interactive allow ACE")
	}
}

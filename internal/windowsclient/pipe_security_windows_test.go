//go:build windows

package windowsclient

import (
	"testing"

	"github.com/Microsoft/go-winio"
)

func TestPipeSecurityDescriptorParsesOnWindows(t *testing.T) {
	descriptor, err := winio.SddlToSecurityDescriptor(PipeSecurityDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor) == 0 {
		t.Fatal("empty binary security descriptor")
	}
}

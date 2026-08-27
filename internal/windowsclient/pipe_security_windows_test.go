//go:build windows

package windowsclient

import (
	"fmt"
	"os"
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

func TestPipeSecurityDescriptorCreatesNamedPipeOnWindows(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\NeProto.SecurityTest.%d`, os.Getpid())
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: PipeSecurityDescriptor,
		InputBufferSize:    MaxIPCMessageBytes + 4,
		OutputBufferSize:   MaxIPCMessageBytes + 4,
	})
	if err != nil {
		t.Fatalf("create protected named pipe: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close protected named pipe: %v", err)
	}
}

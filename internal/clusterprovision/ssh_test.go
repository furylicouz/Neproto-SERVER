package clusterprovision

import (
	"bytes"
	"testing"
)

func TestMergeRemoteSessionOutputKeepsStdoutAndStderr(t *testing.T) {
	stdout := &boundedBuffer{maximum: maxRemoteOutput}
	stderr := &boundedBuffer{maximum: maxRemoteOutput}
	_, _ = stdout.Write([]byte("attestation-json\n"))
	_, _ = stderr.Write([]byte("diagnostic\n"))

	output, err := mergeRemoteSessionOutput(stdout, stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, []byte("attestation-json\ndiagnostic\n")) {
		t.Fatalf("merged output=%q", output)
	}
}

func TestMergeRemoteSessionOutputRejectsEitherOverflow(t *testing.T) {
	for _, buffers := range [][2]*boundedBuffer{
		{{maximum: 1, overflow: true}, {maximum: 1}},
		{{maximum: 1}, {maximum: 1, overflow: true}},
	} {
		if _, err := mergeRemoteSessionOutput(buffers[0], buffers[1]); err == nil {
			t.Fatal("remote output overflow was ignored")
		}
	}
}

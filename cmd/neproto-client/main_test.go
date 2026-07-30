package main

import (
	"bytes"
	"strings"
	"testing"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/protocol"
)

func TestClientVersionAndUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := execute([]string{"version"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "neproto-client") {
		t.Fatalf("version code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"run"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("usage code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestWriteProbeResultPreservesStatusAndAddsCoverDiagnostics(t *testing.T) {
	var output bytes.Buffer
	writeProbeResult(&output, app.ProbeResult{
		Kind: protocol.CarrierHTTPS, UsedFallback: true,
		MosaicEnabled: true, CoverClass: "stream", CoverTransitions: 3,
		ConstellationEnabled: true, ForwardSecrecyEnabled: true,
	})
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || lines[0] != "carrier=https fallback=true authentication=ok" ||
		lines[1] != "cover=mosaic class=stream transitions=3" ||
		lines[2] != "constellation=true forward_secrecy=true" {
		t.Fatalf("probe output=%q", output.String())
	}
}

func TestWriteProbeResultNamesHTTP3Carrier(t *testing.T) {
	var output bytes.Buffer
	writeProbeResult(&output, app.ProbeResult{Kind: protocol.CarrierHTTP3, CoverClass: "web"})
	if !strings.HasPrefix(output.String(), "carrier=http3 fallback=false authentication=ok\n") {
		t.Fatalf("probe output=%q", output.String())
	}
}

func TestParseProbeMode(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{value: "auto", valid: true},
		{value: "webrtc", valid: true},
		{value: "https", valid: true},
		{value: "http3", valid: true},
		{value: "udp", valid: false},
		{value: "", valid: false},
	}
	for _, test := range tests {
		if _, err := parseProbeMode(test.value); (err == nil) != test.valid {
			t.Fatalf("parseProbeMode(%q) error=%v valid=%v", test.value, err, test.valid)
		}
	}
}

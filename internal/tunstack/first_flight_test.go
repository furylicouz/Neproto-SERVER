package tunstack

import "testing"

func TestInspectFirstFlightRejectsMalformedOrUntrustedNames(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		decision firstFlightDecision
	}{
		{name: "partial TLS header", payload: []byte{0x16, 3, 1}, decision: firstFlightNeedMore},
		{name: "invalid TLS length", payload: []byte{0x16, 3, 1, 0xff, 0xff}, decision: firstFlightUseNumeric},
		{name: "unknown binary", payload: []byte{1, 2, 3}, decision: firstFlightUseNumeric},
		{name: "partial HTTP", payload: []byte("GET / HTTP/1.1\r\nHost: chatgpt.com"), decision: firstFlightNeedMore},
		{name: "IP HTTP host", payload: []byte("GET / HTTP/1.1\r\nHost: 203.0.113.4\r\n\r\n"), decision: firstFlightUseNumeric},
		{name: "invalid HTTP host", payload: []byte("GET / HTTP/1.1\r\nHost: bad host\r\n\r\n"), decision: firstFlightUseNumeric},
		{name: "terminator before headers", payload: []byte("GET 0 HTTP/1.\r\n\r\n"), decision: firstFlightNeedMore},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, decision := inspectFirstFlight(test.payload)
			if decision != test.decision {
				t.Fatalf("decision=%d, want %d", decision, test.decision)
			}
		})
	}
}

func FuzzInspectFirstFlight(f *testing.F) {
	f.Add(testTLSClientHello("chatgpt.com"))
	f.Add([]byte("GET / HTTP/1.1\r\nHost: api.openai.com\r\n\r\n"))
	f.Add([]byte{0x16, 3, 1, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, payload []byte) {
		domain, decision := inspectFirstFlight(payload)
		if decision > firstFlightUseDomain {
			t.Fatalf("invalid decision=%d", decision)
		}
		if decision == firstFlightUseDomain {
			canonical, ok := canonicalDNSName(domain)
			if !ok || canonical != domain {
				t.Fatalf("non-canonical domain=%q", domain)
			}
		}
	})
}

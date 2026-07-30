package comparativelab

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMeasureDirectProducesBoundedSuccessfulSamples(t *testing.T) {
	payload := make([]byte, 128*1024)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	samples, err := Measure(context.Background(), MeasureConfig{
		RunID: "run-1", Implementation: "direct", Profile: "baseline",
		Transport: "direct", Network: "localhost", Endpoint: "fixture-128k",
		URL: server.URL, Runs: 2, ExpectedBytes: int64(len(payload)),
		Timeout: 5 * time.Second, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	})
	if err != nil {
		t.Fatalf("Measure() error = %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(samples))
	}
	for index, sample := range samples {
		if !sample.Success || sample.Bytes != int64(len(payload)) || sample.HTTPStatus != http.StatusOK {
			t.Fatalf("sample %d = %+v", index, sample)
		}
		if sample.ThroughputBPS <= 0 || sample.TotalMS <= 0 || sample.TTFBMS <= 0 {
			t.Fatalf("sample %d has incomplete timings: %+v", index, sample)
		}
		if sample.ErrorCategory != "" {
			t.Fatalf("sample %d leaked error category on success: %+v", index, sample)
		}
	}
}

func TestMeasureStoresStableFailureCategoryWithoutRawError(t *testing.T) {
	samples, err := Measure(context.Background(), MeasureConfig{
		RunID: "run-1", Implementation: "np2", Profile: "constellation",
		Transport: "https", Network: "localhost", Endpoint: "closed-port",
		URL: "https://127.0.0.1:1/secret-path-must-not-appear", Runs: 1,
		Timeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Measure() setup error = %v", err)
	}
	if len(samples) != 1 || samples[0].Success || samples[0].ErrorCategory != "connect" {
		t.Fatalf("samples = %+v", samples)
	}
	if got := fmt.Sprintf("%+v", samples[0]); got == "" || containsAny(got, "secret-path", "127.0.0.1") {
		t.Fatalf("sample contains raw target data: %s", got)
	}
}

func TestMeasureRejectsNonLoopbackSOCKSProxy(t *testing.T) {
	_, err := Measure(context.Background(), MeasureConfig{
		RunID: "run-1", Implementation: "vless", Profile: "vision",
		Transport: "vision", Network: "localhost", Endpoint: "fixture",
		URL: "https://example.com/file", ProxyURL: "socks5://192.0.2.10:1080",
		Runs: 1, Timeout: time.Second,
	})
	if err == nil {
		t.Fatal("Measure() accepted a non-loopback SOCKS proxy")
	}
}

func TestResolveDialAddressPinsRequestedAddressFamily(t *testing.T) {
	address, err := resolveDialAddress(context.Background(), "localhost:443", "4", "")
	if err != nil {
		t.Fatalf("resolveDialAddress() error = %v", err)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", address, err)
	}
	if port != "443" || net.ParseIP(host) == nil || net.ParseIP(host).To4() == nil {
		t.Fatalf("resolved address = %q, want IPv4 port 443", address)
	}
}

func TestResolveDialAddressUsesPinnedTargetWithoutChangingPort(t *testing.T) {
	address, err := resolveDialAddress(context.Background(), "example.com:8443", "4", "192.0.2.44")
	if err != nil {
		t.Fatalf("resolveDialAddress() error = %v", err)
	}
	if address != "192.0.2.44:8443" {
		t.Fatalf("resolved address = %q", address)
	}
}

func TestMeasureRejectsUnknownAddressFamily(t *testing.T) {
	_, err := Measure(context.Background(), MeasureConfig{
		RunID: "run-1", Implementation: "direct", Profile: "baseline",
		Transport: "direct", Network: "localhost", Endpoint: "fixture",
		URL: "https://example.com/file", Runs: 1, Timeout: time.Second,
		AddressFamily: "ipx",
	})
	if err == nil {
		t.Fatal("Measure() accepted an unknown address family")
	}
}

func TestMeasureRejectsNonIPAddressTargetPin(t *testing.T) {
	_, err := Measure(context.Background(), MeasureConfig{
		RunID: "run-1", Implementation: "direct", Profile: "baseline",
		Transport: "direct", Network: "localhost", Endpoint: "fixture",
		URL: "https://example.com/file", Runs: 1, Timeout: time.Second,
		AddressFamily: "4", TargetAddress: "hidden.example.com",
	})
	if err == nil {
		t.Fatal("Measure() accepted a non-IP target pin")
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if len(needle) > 0 && len(value) >= len(needle) {
			for index := 0; index+len(needle) <= len(value); index++ {
				if value[index:index+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}

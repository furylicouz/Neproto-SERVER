package tunstack

import (
	"net/netip"
	"testing"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func TestTrafficStatsReportsOneSecondRateAndTotals(t *testing.T) {
	statistic.DefaultManager.ResetStatistic()
	t.Cleanup(statistic.DefaultManager.ResetStatistic)
	statistic.DefaultManager.PushUploaded(12_345)
	statistic.DefaultManager.PushDownloaded(67_890)

	stack := &Stack{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		uploadRate, downloadRate, uploadTotal, downloadTotal := stack.TrafficStats()
		if uploadTotal != 12_345 || downloadTotal != 67_890 {
			t.Fatalf(
				"totals=(%d,%d), want (12345,67890)",
				uploadTotal,
				downloadTotal,
			)
		}
		if uploadRate == 12_345 && downloadRate == 67_890 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	uploadRate, downloadRate, _, _ := stack.TrafficStats()
	t.Fatalf("rate=(%d,%d), want (12345,67890)", uploadRate, downloadRate)
}

func TestStackReportsDestinationFreeDNSAttributionStats(t *testing.T) {
	dns := newDNSAttribution(time.Now)
	dns.misses = 2
	dns.hits = 3
	dns.queries = 4
	dns.responses = 4
	dns.addresses[netip.MustParseAddr("203.0.113.4")] = dnsAddress{
		domain: "example.org", expires: time.Now().Add(time.Minute),
	}
	stack := &Stack{dialer: &Dialer{dns: dns}}
	if got := stack.DNSAttributionStats(); got.Queries != 4 || got.Responses != 4 ||
		got.Hits != 3 || got.Misses != 2 || got.Cached != 1 {
		t.Fatalf("stats=%+v", got)
	}
}

func TestStartWithSessionRouterDeviceRejectsNilDevice(t *testing.T) {
	if _, err := StartWithSessionRouterDevice(nil, 1500, &SessionRouter{}); err != ErrInvalidStackConfig {
		t.Fatalf("error=%v, want %v", err, ErrInvalidStackConfig)
	}
}

func TestUDPTransportHandlerRejectsQUICBeforeCreatingAssociation(t *testing.T) {
	forwarded := 0
	rejected := 0
	handler := newUDPTransportHandler(
		func(stack.TransportEndpointID, *stack.PacketBuffer) bool {
			forwarded++
			return true
		},
		func(port uint16) bool {
			if port != 443 {
				return false
			}
			rejected++
			return true
		},
	)

	if handled := handler(stack.TransportEndpointID{LocalPort: 443}, nil); handled {
		t.Fatal("UDP/443 was consumed; false is required for immediate ICMP port-unreachable")
	}
	if rejected != 1 || forwarded != 0 {
		t.Fatalf("UDP/443 rejected=%d forwarded=%d, want 1/0", rejected, forwarded)
	}
	if handled := handler(stack.TransportEndpointID{LocalPort: 53}, nil); !handled {
		t.Fatal("ordinary UDP was rejected")
	}
	if rejected != 1 || forwarded != 1 {
		t.Fatalf("UDP/53 rejected=%d forwarded=%d, want 1/1", rejected, forwarded)
	}
}

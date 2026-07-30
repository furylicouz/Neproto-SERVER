package tunstack

import (
	"context"
	"net/netip"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"golang.org/x/net/dns/dnsmessage"

	np2proxy "neproto.local/chameleon/internal/proxy"
)

func TestDNSAttributionPreservesDomainForNumericTUNTCPFlow(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	attribution := newDNSAttribution(func() time.Time { return now })
	question := dnsmessage.Question{
		Name: mustDNSName(t, "2ip.ru."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
	}
	query, err := packDNSMessage(dnsmessage.Message{
		Header: dnsmessage.Header{ID: 41, RecursionDesired: true}, Questions: []dnsmessage.Question{question},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := packDNSMessage(dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 41, Response: true, RecursionAvailable: true},
		Questions: []dnsmessage.Question{question},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120},
			Body:   &dnsmessage.AResource{A: [4]byte{203, 0, 113, 27}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	attribution.observeQuery(query)
	attribution.observeResponse(response)

	var opened []byte
	dialer := &Dialer{
		dns: attribution,
		open: func(_ context.Context, metadata []byte) (streamConnection, error) {
			opened = append([]byte(nil), metadata...)
			return &stubStream{}, nil
		},
	}
	connection, err := dialer.DialContext(context.Background(), &M.Metadata{
		Network: M.TCP, DstIP: netip.MustParseAddr("203.0.113.27"), DstPort: 443,
		SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49152,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	target, err := np2proxy.DecodeTarget(opened)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "2ip.ru" || target.Port != 443 {
		t.Fatalf("NP/2 target=%+v, want attributed domain", target)
	}
}

func TestDNSAttributionRejectsUnsolicitedAndExpiredAnswers(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	attribution := newDNSAttribution(func() time.Time { return now })
	question := dnsmessage.Question{Name: mustDNSName(t, "example.org."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	response, err := packDNSMessage(dnsmessage.Message{
		Header: dnsmessage.Header{ID: 99, Response: true}, Questions: []dnsmessage.Question{question},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1},
			Body:   &dnsmessage.AResource{A: [4]byte{198, 51, 100, 4}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	attribution.observeResponse(response)
	if _, ok := attribution.domainFor(netip.MustParseAddr("198.51.100.4")); ok {
		t.Fatal("unsolicited DNS response populated attribution cache")
	}
	query, _ := packDNSMessage(dnsmessage.Message{Header: dnsmessage.Header{ID: 99}, Questions: []dnsmessage.Question{question}})
	attribution.observeQuery(query)
	attribution.observeResponse(response)
	if domain, ok := attribution.domainFor(netip.MustParseAddr("198.51.100.4")); !ok || domain != "example.org" {
		t.Fatalf("attribution=(%q,%t)", domain, ok)
	}
	now = now.Add(2 * time.Second)
	if _, ok := attribution.domainFor(netip.MustParseAddr("198.51.100.4")); ok {
		t.Fatal("expired DNS attribution remained active")
	}
}

func TestDNSAttributionStatsExplainDomainRoutingReadiness(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	attribution := newDNSAttribution(func() time.Time { return now })
	question := dnsmessage.Question{Name: mustDNSName(t, "chatgpt.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	query, _ := packDNSMessage(dnsmessage.Message{
		Header: dnsmessage.Header{ID: 51}, Questions: []dnsmessage.Question{question},
	})
	answer := [4]byte{203, 0, 113, 51}
	response, _ := packDNSMessage(dnsmessage.Message{
		Header: dnsmessage.Header{ID: 51, Response: true}, Questions: []dnsmessage.Question{question},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.AResource{A: answer},
		}},
	})

	attribution.observeQuery(query)
	attribution.observeResponse(response)
	attribution.domainFor(netip.AddrFrom4(answer))
	attribution.domainFor(netip.MustParseAddr("198.51.100.99"))
	stats := attribution.stats()
	if stats.Queries != 1 || stats.Responses != 1 || stats.Hits != 1 || stats.Misses != 1 || stats.Cached != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func packDNSMessage(message dnsmessage.Message) ([]byte, error) {
	return message.Pack()
}

func mustDNSName(t *testing.T, value string) dnsmessage.Name {
	t.Helper()
	name, err := dnsmessage.NewName(value)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

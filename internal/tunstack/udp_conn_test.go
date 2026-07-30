package tunstack

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	np2proxy "neproto.local/chameleon/internal/proxy"
)

func TestDNSQueryIsAttributedBeforeAssociationCanDeliverResponse(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	attribution := newDNSAttribution(func() time.Time { return now })
	question := dnsmessage.Question{
		Name: mustDNSName(t, "chatgpt.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
	}
	query, err := packDNSMessage(dnsmessage.Message{
		Header: dnsmessage.Header{ID: 73, RecursionDesired: true}, Questions: []dnsmessage.Question{question},
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := [4]byte{203, 0, 113, 44}
	response, err := packDNSMessage(dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 73, Response: true, RecursionAvailable: true},
		Questions: []dnsmessage.Question{question},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120},
			Body:   &dnsmessage.AResource{A: answer},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	remote := net.UDPAddrFromAddrPort(netip.MustParseAddrPort("1.1.1.1:53"))
	association := &immediateDNSResponseAssociation{
		onWrite: func() { attribution.observeResponse(response) },
	}
	connection := &udpPacketConnection{
		association: association, remote: remote, dns: attribution, observeDNS: true,
	}
	if written, err := connection.WriteTo(query, remote); err != nil || written != len(query) {
		t.Fatalf("WriteTo()=(%d,%v)", written, err)
	}
	if domain, ok := attribution.domainFor(netip.AddrFrom4(answer)); !ok || domain != "chatgpt.com" {
		t.Fatalf("attribution=(%q,%t), want chatgpt.com", domain, ok)
	}
}

func TestFailedDNSWriteDoesNotLeaveAttributionPending(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	attribution := newDNSAttribution(func() time.Time { return now })
	question := dnsmessage.Question{
		Name: mustDNSName(t, "chatgpt.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
	}
	query, _ := packDNSMessage(dnsmessage.Message{
		Header: dnsmessage.Header{ID: 74, RecursionDesired: true}, Questions: []dnsmessage.Question{question},
	})
	answer := [4]byte{203, 0, 113, 45}
	response, _ := packDNSMessage(dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 74, Response: true, RecursionAvailable: true},
		Questions: []dnsmessage.Question{question},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120},
			Body:   &dnsmessage.AResource{A: answer},
		}},
	})

	remote := net.UDPAddrFromAddrPort(netip.MustParseAddrPort("1.1.1.1:53"))
	connection := &udpPacketConnection{
		association: &immediateDNSResponseAssociation{writeErr: errors.New("send failed")},
		remote:      remote, dns: attribution, observeDNS: true,
	}
	if _, err := connection.WriteTo(query, remote); err == nil {
		t.Fatal("WriteTo succeeded")
	}
	attribution.observeResponse(response)
	if domain, ok := attribution.domainFor(netip.AddrFrom4(answer)); ok {
		t.Fatalf("failed query populated attribution=%q", domain)
	}
}

type immediateDNSResponseAssociation struct {
	onWrite  func()
	writeErr error
}

func (a *immediateDNSResponseAssociation) WriteDatagram([]byte, *np2proxy.Target) error {
	if a.onWrite != nil {
		a.onWrite()
	}
	return a.writeErr
}

func (*immediateDNSResponseAssociation) ReadDatagram() ([]byte, np2proxy.Target, error) {
	return nil, np2proxy.Target{}, io.EOF
}

func (*immediateDNSResponseAssociation) Close() error { return nil }
func (*immediateDNSResponseAssociation) Abort() error { return nil }

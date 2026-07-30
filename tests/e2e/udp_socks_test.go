package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier/httpsws"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
)

func TestSOCKSUDPAssociateOverAuthenticatedHTTPSCarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acceptor, err := httpsws.NewAcceptor(httpsws.AcceptorConfig{Path: "/static/chunks/udp"})
	if err != nil {
		t.Fatal(err)
	}
	defer acceptor.Close()
	tlsServer := httptest.NewUnstartedServer(acceptor.Handler())
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsServer.StartTLS()
	defer tlsServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	clientCarrier, err := httpsws.Dial(ctx, httpsws.DialConfig{
		URL:       "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/static/chunks/udp",
		TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clientCarrier.Close()
	defer serverCarrier.Close()

	parameters := protocol.ExtensionParameters{
		Capabilities: protocol.CapabilityReliableUDP, MaxUDPAssociations: 16,
		MaxUDPPayload: 4096, UDPIdleTimeoutMS: 5000,
		MaxSessionReceiveBytes: 1024 * 1024, MaxStreamWindowBytes: 64 * 1024,
	}
	secret := [protocol.RootSecretSize]byte{0xa8, 0x44, 0x92, 0x11}
	base := session.AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileInteractive,
		InitialWindow: 128 * 1024, MaxStreams: 32, MaxCoverOverheadPercent: 20,
		ExtensionTimeout: time.Second,
	}
	serverConfig := base
	serverConfig.ExtensionOffer = &parameters
	serverResult := make(chan struct {
		authenticated *session.Authenticated
		err           error
	}, 1)
	go func() {
		authenticated, authErr := session.AcceptServer(ctx, serverCarrier, serverConfig)
		serverResult <- struct {
			authenticated *session.Authenticated
			err           error
		}{authenticated, authErr}
	}()
	clientConfig := base
	clientConfig.ExtensionRequest = &parameters
	clientConfig.RequiredExtensions = protocol.CapabilityReliableUDP
	clientSession, err := session.ConnectClient(ctx, clientCarrier, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	serverAuth := <-serverResult
	if serverAuth.err != nil {
		t.Fatal(serverAuth.err)
	}
	defer clientSession.Mux.Close()
	defer serverAuth.authenticated.Mux.Close()

	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buffer := make([]byte, 4096)
		length, source, readErr := target.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = target.WriteToUDP(buffer[:length], source)
		}
	}()
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- (proxy.Server{
			Mux: serverAuth.authenticated.Mux, Policy: proxy.DestinationPolicy{AllowPrivate: true},
			MaxUDPPayload: 4096, UDPIdleTimeout: 5 * time.Second,
		}).Serve(ctx)
	}()

	socksListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connector := proxy.Connector{Mux: clientSession.Mux, MaxUDPPayload: 4096}
	socksDone := make(chan error, 1)
	go func() {
		socksDone <- (socks5.Server{
			Connect: connector.Connect, AssociateUDP: connector.AssociateUDP,
		}).Serve(ctx, socksListener)
	}()
	control, relay := openE2EUDPAssociate(t, socksListener.Addr().String())
	defer control.Close()
	udpClient, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udpClient.Close()
	targetAddress := target.LocalAddr().(*net.UDPAddr)
	payload := []byte("NP/2 desktop SOCKS UDP E2E")
	request := []byte{0, 0, 0, 1, 127, 0, 0, 1}
	request = binary.BigEndian.AppendUint16(request, uint16(targetAddress.Port))
	request = append(request, payload...)
	if _, err := udpClient.WriteToUDP(request, relay); err != nil {
		t.Fatal(err)
	}
	if err := udpClient.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 65_507)
	length, _, err := udpClient.ReadFromUDP(response)
	if err != nil {
		t.Fatal(err)
	}
	if length < 10 || !bytes.Equal(response[10:length], payload) ||
		binary.BigEndian.Uint16(response[8:10]) != uint16(targetAddress.Port) {
		t.Fatalf("SOCKS UDP response=%x", response[:length])
	}

	_ = control.Close()
	cancel()
	waitResult(t, "UDP SOCKS server", socksDone)
	waitResult(t, "UDP proxy server", proxyDone)
}

func openE2EUDPAssociate(t *testing.T, address string) (net.Conn, *net.UDPAddr) {
	t.Helper()
	control, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _ = control.Write([]byte{5, 1, 0})
	method := make([]byte, 2)
	if _, err := io.ReadFull(control, method); err != nil || !bytes.Equal(method, []byte{5, 0}) {
		t.Fatalf("method=%x error=%v", method, err)
	}
	_, _ = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	reply := make([]byte, 10)
	if _, err := io.ReadFull(control, reply); err != nil || reply[1] != socks5.ReplySucceeded || reply[3] != 1 {
		t.Fatalf("UDP ASSOCIATE reply=%x error=%v", reply, err)
	}
	_ = control.SetDeadline(time.Time{})
	return control, &net.UDPAddr{
		IP:   net.IPv4(reply[4], reply[5], reply[6], reply[7]),
		Port: int(binary.BigEndian.Uint16(reply[8:10])),
	}
}

package clusterprovision

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestDiscoverSSHHostKeyOnlyRequiresConnectionFields(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		configuration := &ssh.ServerConfig{NoClientAuth: true}
		configuration.AddHostKey(hostKey)
		_, _, _, _ = ssh.NewServerConn(connection, configuration)
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fingerprint, err := DiscoverSSHHostKey(ctx, EnrollmentRequest{
		Host: "127.0.0.1", Port: uint16(port), User: "root", Password: []byte("temporary"),
	})
	if err != nil {
		t.Fatalf("DiscoverSSHHostKey(%s) error = %v", strconv.Itoa(port), err)
	}
	if fingerprint != ssh.FingerprintSHA256(hostKey.PublicKey()) {
		t.Fatalf("fingerprint=%q", fingerprint)
	}
	<-serverDone
}

func TestEnrollmentRequestRejectsShellAndNetworkInjection(t *testing.T) {
	valid := EnrollmentRequest{Host: "203.0.113.10", Port: 22, User: "root", Password: []byte("temporary"), PinnedHostKey: "SHA256:pin", NodeID: "edge-01", Name: "Edge", Region: "Helsinki"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request error = %v", err)
	}
	invalid := []EnrollmentRequest{
		func() EnrollmentRequest { value := valid; value.Host = "host;reboot"; return value }(),
		func() EnrollmentRequest { value := valid; value.User = "root $(id)"; return value }(),
		func() EnrollmentRequest { value := valid; value.NodeID = "../edge"; return value }(),
		func() EnrollmentRequest { value := valid; value.Region = "Helsinki\nroot"; return value }(),
	}
	for _, request := range invalid {
		if err := request.Validate(); err == nil {
			t.Fatalf("accepted unsafe request: %+v", request)
		}
	}
}

func TestPinnedHostKeyCallbackRequiresExplicitFingerprint(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := ssh.FingerprintSHA256(publicKey)

	unknown := PinnedHostKeyCallback("")("edge", nil, publicKey)
	var unknownKey *UnknownHostKeyError
	if !errors.As(unknown, &unknownKey) || unknownKey.Fingerprint != fingerprint {
		t.Fatalf("unknown host key error = %v", unknown)
	}
	if err := PinnedHostKeyCallback("SHA256:wrong")("edge", nil, publicKey); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("mismatched host key error = %v", err)
	}
	if err := PinnedHostKeyCallback(fingerprint)("edge", nil, publicKey); err != nil {
		t.Fatalf("pinned host key error = %v", err)
	}
}

func TestProvisionerUploadsRunsInstallerAttestsAndZeroesPassword(t *testing.T) {
	remote := &fakeRemote{}
	password := []byte("temporary-password")
	var progress []EnrollmentProgress
	provisioner := Provisioner{
		Dial:   func(context.Context, EnrollmentRequest) (Remote, error) { return remote, nil },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, stagingRandomBytes)),
		Progress: func(update EnrollmentProgress) {
			progress = append(progress, update)
		},
	}
	request := EnrollmentRequest{Host: "203.0.113.10", Port: 22, User: "root", Password: password, PinnedHostKey: "SHA256:pin", NodeID: "edge-01", Name: "Edge", Region: "Helsinki"}
	result, err := provisioner.Enroll(context.Background(), request, strings.NewReader("bundle"), []byte(`{"cluster_id":"cluster-01","node_id":"edge-01"}`))
	if err != nil {
		t.Fatalf("Enroll() error = %v", err)
	}
	if result.NodeID != "edge-01" || result.Attestation != "NP2_CLUSTER_NODE_READY" {
		t.Fatalf("Enroll() result = %+v", result)
	}
	for _, value := range password {
		if value != 0 {
			t.Fatal("password was retained after enrolment")
		}
	}
	joined := strings.Join(remote.commands, "\n")
	for _, expected := range []string{"uname -s", "bundle.tar.gz", "bootstrap.json", "cluster-node-install.sh", "neproto-server cluster-attest"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("remote workflow missing %q:\n%s", expected, joined)
		}
	}
	if strings.Contains(joined, "temporary-password") || strings.Contains(joined, request.Host) || strings.Contains(joined, request.User) {
		t.Fatalf("credential or connection input leaked into remote commands: %s", joined)
	}
	wantStages := []string{"connect", "preflight", "upload-bundle", "upload-bootstrap", "install", "remote-attestation", "complete"}
	if len(progress) != len(wantStages) {
		t.Fatalf("progress stages=%+v, want %v", progress, wantStages)
	}
	for index, want := range wantStages {
		if progress[index].Stage != want || progress[index].Step != index+1 || progress[index].Total != len(wantStages) {
			t.Fatalf("progress[%d]=%+v, want stage=%q step=%d/%d", index, progress[index], want, index+1, len(wantStages))
		}
	}
}

func TestProvisionerCleansStagingOnInstallerFailure(t *testing.T) {
	remote := &fakeRemote{failContaining: "cluster-node-install.sh", failureOutput: []byte("ERROR: existing node recovery failed\n")}
	provisioner := Provisioner{Dial: func(context.Context, EnrollmentRequest) (Remote, error) { return remote, nil }, Random: bytes.NewReader(bytes.Repeat([]byte{0x24}, stagingRandomBytes))}
	request := EnrollmentRequest{Host: "edge.example.com", Port: 22, User: "root", Password: []byte("temporary"), PinnedHostKey: "SHA256:pin", NodeID: "edge-01", Name: "Edge", Region: "Helsinki"}
	_, enrollmentErr := provisioner.Enroll(context.Background(), request, strings.NewReader("bundle"), []byte(`{}`))
	if enrollmentErr == nil {
		t.Fatal("installer failure was ignored")
	}
	if !strings.Contains(enrollmentErr.Error(), "ERROR: existing node recovery failed") {
		t.Fatalf("installer diagnostic was hidden: %v", enrollmentErr)
	}
	if len(remote.commands) == 0 || !strings.Contains(remote.commands[len(remote.commands)-1], "rm -rf -- /var/lib/neproto-bootstrap/") {
		t.Fatalf("staging cleanup missing: %v", remote.commands)
	}
	deadline := remote.deadlines[len(remote.deadlines)-1]
	ok := !deadline.IsZero()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > cleanupTimeout {
		t.Fatalf("staging cleanup is not bounded by %v: deadline=%v ok=%t", cleanupTimeout, deadline, ok)
	}
}

func TestProvisionerAcceptsIdentityBoundJSONAttestationWithSSHBanner(t *testing.T) {
	remote := &fakeRemote{attestationOutput: []byte(
		"Welcome to Ubuntu 24.04 LTS\n" +
			"\x1b]0;root@edge\x07{\"ready\":true,\"cluster_id\":\"cluster-01\",\"node_id\":\"edge-01\"}\r\n",
	)}
	provisioner := Provisioner{
		Dial:   func(context.Context, EnrollmentRequest) (Remote, error) { return remote, nil },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x31}, stagingRandomBytes)),
	}
	request := EnrollmentRequest{
		Host: "203.0.113.10", Port: 22, User: "root", Password: []byte("temporary"),
		PinnedHostKey: "SHA256:pin", NodeID: "edge-01", Name: "Edge", Region: "Helsinki",
	}
	result, err := provisioner.Enroll(
		context.Background(), request, strings.NewReader("bundle"),
		[]byte(`{"cluster_id":"cluster-01","node_id":"edge-01"}`),
	)
	if err != nil {
		t.Fatalf("banner-prefixed JSON attestation rejected: %v", err)
	}
	if result.NodeID != "edge-01" || result.Attestation != "NP2_CLUSTER_NODE_READY" {
		t.Fatalf("unexpected result: %+v", result)
	}
	joined := strings.Join(remote.commands, "\n")
	if !strings.Contains(joined, "cluster-attest --format json") {
		t.Fatalf("identity-bound JSON attestation was not requested: %s", joined)
	}
}

func TestProvisionerRejectsJSONAttestationForDifferentCluster(t *testing.T) {
	remote := &fakeRemote{attestationOutput: []byte("{\"ready\":true,\"cluster_id\":\"other-cluster\",\"node_id\":\"edge-01\"}\n")}
	provisioner := Provisioner{
		Dial:   func(context.Context, EnrollmentRequest) (Remote, error) { return remote, nil },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x32}, stagingRandomBytes)),
	}
	request := EnrollmentRequest{
		Host: "203.0.113.10", Port: 22, User: "root", Password: []byte("temporary"),
		PinnedHostKey: "SHA256:pin", NodeID: "edge-01", Name: "Edge", Region: "Helsinki",
	}
	_, err := provisioner.Enroll(
		context.Background(), request, strings.NewReader("bundle"),
		[]byte(`{"cluster_id":"cluster-01","node_id":"edge-01"}`),
	)
	if !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("cross-cluster attestation error=%v", err)
	}
	for _, expected := range []string{"cluster-01", "edge-01", "other-cluster"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("attestation diagnostic missing %q: %v", expected, err)
		}
	}
}

func TestValidEnrollmentAttestationAllowsRepeatedIdenticalRecords(t *testing.T) {
	raw := []byte("{\"ready\":true,\"cluster_id\":\"cluster-01\",\"node_id\":\"edge-01\"}\n{\"ready\":true,\"cluster_id\":\"cluster-01\",\"node_id\":\"edge-01\"}\n")
	if !validEnrollmentAttestation(raw, "cluster-01", "edge-01") {
		t.Fatal("identical repeated attestation records were rejected")
	}
}

type fakeRemote struct {
	commands          []string
	deadlines         []time.Time
	failContaining    string
	failureOutput     []byte
	attestationOutput []byte
}

func (remote *fakeRemote) Run(ctx context.Context, command string, stdin io.Reader) ([]byte, error) {
	remote.commands = append(remote.commands, command)
	deadline, _ := ctx.Deadline()
	remote.deadlines = append(remote.deadlines, deadline)
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	if strings.Contains(command, remote.failContaining) && remote.failContaining != "" {
		return append([]byte(nil), remote.failureOutput...), errors.New("remote command failed")
	}
	if strings.Contains(command, "cluster-attest") {
		if remote.attestationOutput != nil {
			return append([]byte(nil), remote.attestationOutput...), nil
		}
		return []byte("{\"ready\":true,\"cluster_id\":\"cluster-01\",\"node_id\":\"edge-01\"}\n"), nil
	}
	return []byte("ok\n"), nil
}

func (*fakeRemote) Close() error { return nil }

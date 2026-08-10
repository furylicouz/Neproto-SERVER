package clusterprovision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"
)

const (
	stagingRandomBytes = 16
	maxBootstrapBytes  = 64 << 10
	maxFailureRunes    = 4 << 10
	cleanupTimeout     = 10 * time.Second
)

var (
	ErrInvalidEnrollment  = errors.New("invalid cluster node enrolment request")
	ErrInvalidAttestation = errors.New("invalid cluster node attestation")
)

type EnrollmentRequest struct {
	Host          string
	Port          uint16
	User          string
	Password      []byte
	PrivateKeyPEM []byte
	PinnedHostKey string
	NodeID        string
	Name          string
	Region        string
}

func (request EnrollmentRequest) Validate() error {
	if err := request.ValidateSSHConnection(); err != nil {
		return err
	}
	if !validNodeID(request.NodeID) {
		return invalidEnrollmentField("node_id", "must start with a lowercase letter or digit and contain only lowercase letters, digits, '-' or '_'")
	}
	if !validLabel(request.Name, 96) {
		return invalidEnrollmentField("name", "must be 1-96 characters without surrounding whitespace or line breaks")
	}
	if !validLabel(request.Region, 96) {
		return invalidEnrollmentField("region", "must be 1-96 characters without surrounding whitespace or line breaks")
	}
	return nil
}

// ValidateSSHConnection validates only the transport and authentication
// fields needed to discover a host key. Node metadata is intentionally not
// required until the operator submits the final enrolment request.
func (request EnrollmentRequest) ValidateSSHConnection() error {
	if !validSSHHost(request.Host) {
		return invalidEnrollmentField("host", "must be an IP address or DNS hostname without a URL scheme")
	}
	if request.Port == 0 {
		return invalidEnrollmentField("port", "must be between 1 and 65535")
	}
	if !validSSHUser(request.User) {
		return invalidEnrollmentField("user", "must contain only letters, digits, '-' or '_'")
	}
	passwordLength, privateKeyLength := len(request.Password), len(request.PrivateKeyPEM)
	if (passwordLength == 0) == (privateKeyLength == 0) || passwordLength > 1024 || privateKeyLength > 64<<10 {
		return invalidEnrollmentField("authentication", "must contain exactly one bounded password or private key")
	}
	if request.PinnedHostKey != "" && !strings.HasPrefix(request.PinnedHostKey, "SHA256:") {
		return invalidEnrollmentField("fingerprint", "must use the SHA256 format")
	}
	return nil
}

func invalidEnrollmentField(field, requirement string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidEnrollment, field, requirement)
}

type Remote interface {
	Run(context.Context, string, io.Reader) ([]byte, error)
	Close() error
}

type DialFunc func(context.Context, EnrollmentRequest) (Remote, error)

type Provisioner struct {
	Dial     DialFunc
	Random   io.Reader
	Progress func(EnrollmentProgress)
}

// EnrollmentProgress describes a secret-free, user-visible provisioning step.
// Callers may render these updates directly; no host credentials, transport
// paths, or bootstrap material are included.
type EnrollmentProgress struct {
	Stage   string
	Message string
	Step    int
	Total   int
}

const enrollmentProgressTotal = 7

func (provisioner Provisioner) reportProgress(stage, message string, step int) {
	if provisioner.Progress != nil {
		provisioner.Progress(EnrollmentProgress{
			Stage: stage, Message: message, Step: step, Total: enrollmentProgressTotal,
		})
	}
}

type EnrollmentResult struct {
	NodeID      string
	Attestation string
}

func (provisioner Provisioner) Enroll(ctx context.Context, request EnrollmentRequest, bundle io.Reader, bootstrap []byte) (EnrollmentResult, error) {
	defer zeroBytes(request.Password)
	defer zeroBytes(request.PrivateKeyPEM)
	defer zeroBytes(bootstrap)
	if ctx == nil || provisioner.Dial == nil || bundle == nil || len(bootstrap) == 0 || len(bootstrap) > maxBootstrapBytes || request.PinnedHostKey == "" {
		return EnrollmentResult{}, ErrInvalidEnrollment
	}
	if err := request.Validate(); err != nil {
		return EnrollmentResult{}, err
	}
	random := provisioner.Random
	if random == nil {
		random = rand.Reader
	}
	identifierRaw := make([]byte, stagingRandomBytes)
	if _, err := io.ReadFull(random, identifierRaw); err != nil {
		return EnrollmentResult{}, fmt.Errorf("create staging identifier: %w", err)
	}
	identifier := hex.EncodeToString(identifierRaw)
	zeroBytes(identifierRaw)
	staging := "/var/lib/neproto-bootstrap/" + identifier

	provisioner.reportProgress("connect", "Connecting to the pinned SSH host", 1)
	remote, err := provisioner.Dial(ctx, request)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("connect cluster node: %w", err)
	}
	defer remote.Close()
	cleanup := func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_, _ = remote.Run(cleanupContext, "rm -rf -- "+staging, nil)
	}
	committed := false
	defer func() {
		if !committed {
			cleanup()
		}
	}()

	commands := []struct {
		stage   string
		message string
		step    int
		command string
		stdin   io.Reader
	}{
		{stage: "preflight", message: "Checking Linux, root access and systemd", step: 2, command: "set -eu; test \"$(id -u)\" -eq 0; test \"$(uname -s)\" = Linux; command -v tar >/dev/null; command -v systemctl >/dev/null; test -r /etc/os-release; uname -s; uname -m"},
		{stage: "upload-bundle", message: "Uploading the signed NeProto server bundle", step: 3, command: "install -d -m 0700 " + staging + "; umask 077; cat > " + staging + "/bundle.tar.gz", stdin: bundle},
		{stage: "upload-bootstrap", message: "Uploading the encrypted cluster bootstrap", step: 4, command: "umask 077; cat > " + staging + "/bootstrap.json", stdin: bytes.NewReader(bootstrap)},
		{stage: "install", message: "Installing and starting the NP/2 node", step: 5, command: "set -eu; tar -xzf " + staging + "/bundle.tar.gz -C " + staging + "; installer=$(find " + staging + " -mindepth 1 -maxdepth 2 -type f -name cluster-node-install.sh -print -quit); test -n \"$installer\"; \"$installer\" --bootstrap " + staging + "/bootstrap.json"},
	}
	for _, step := range commands {
		provisioner.reportProgress(step.stage, step.message, step.step)
		output, err := remote.Run(ctx, step.command, step.stdin)
		if err != nil {
			return EnrollmentResult{}, remoteProvisioningError(err, output)
		}
	}
	expectedClusterID, expectedNodeID, err := enrollmentIdentity(bootstrap, request.NodeID)
	if err != nil {
		return EnrollmentResult{}, err
	}
	provisioner.reportProgress("remote-attestation", "Verifying the installed NP/2 service identity", 6)
	attestation, err := remote.Run(ctx, "neproto-server cluster-attest --format json", nil)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("cluster node attestation failed: %w", err)
	}
	if !validEnrollmentAttestation(attestation, expectedClusterID, expectedNodeID) {
		return EnrollmentResult{}, fmt.Errorf(
			"%w\nexpected: cluster_id=%q node_id=%q\nremote output:\n%q",
			ErrInvalidAttestation, expectedClusterID, expectedNodeID, remoteOutputDetail(attestation),
		)
	}
	cleanup()
	committed = true
	provisioner.reportProgress("complete", "Remote node installation verified", 7)
	return EnrollmentResult{NodeID: request.NodeID, Attestation: "NP2_CLUSTER_NODE_READY"}, nil
}

func remoteProvisioningError(remoteErr error, output []byte) error {
	detail := remoteOutputDetail(output)
	if detail == "" {
		return fmt.Errorf("cluster node provisioning failed: %w", remoteErr)
	}
	return fmt.Errorf("cluster node provisioning failed: %w\nremote output:\n%s", remoteErr, detail)
}

func remoteOutputDetail(output []byte) string {
	detail := strings.TrimSpace(strings.ToValidUTF8(string(output), "�"))
	detail = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || character >= ' ' && character != 0x7f {
			return character
		}
		return -1
	}, detail)
	runes := []rune(detail)
	if len(runes) > maxFailureRunes {
		detail = string(runes[:maxFailureRunes]) + "\n[remote output truncated]"
	}
	return detail
}

func enrollmentIdentity(bootstrap []byte, requestNodeID string) (string, string, error) {
	var identity struct {
		ClusterID string `json:"cluster_id"`
		NodeID    string `json:"node_id"`
	}
	if err := json.Unmarshal(bootstrap, &identity); err != nil || !validNodeID(identity.ClusterID) ||
		!validNodeID(identity.NodeID) || identity.NodeID != requestNodeID {
		return "", "", ErrInvalidEnrollment
	}
	return identity.ClusterID, identity.NodeID, nil
}

func validEnrollmentAttestation(raw []byte, expectedClusterID, expectedNodeID string) bool {
	type attestation struct {
		Ready     bool   `json:"ready"`
		ClusterID string `json:"cluster_id"`
		NodeID    string `json:"node_id"`
	}
	matches := 0
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		objectStart := bytes.IndexByte(line, '{')
		objectEnd := bytes.LastIndexByte(line, '}')
		if objectStart < 0 || objectEnd < objectStart {
			continue
		}
		line = line[objectStart : objectEnd+1]
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var candidate attestation
		if err := decoder.Decode(&candidate); err != nil {
			continue
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			continue
		}
		if !candidate.Ready || candidate.ClusterID != expectedClusterID || candidate.NodeID != expectedNodeID {
			return false
		}
		matches++
	}
	return matches > 0
}

func validSSHHost(value string) bool {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "\x00\r\n\t ;'\"`$(){}[]\\/|") {
		return false
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	labels := strings.Split(strings.ToLower(value), ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validSSHUser(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validNodeID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func validLabel(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func sshAddress(request EnrollmentRequest) string {
	return net.JoinHostPort(request.Host, fmt.Sprintf("%d", request.Port))
}

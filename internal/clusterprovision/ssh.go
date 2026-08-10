package clusterprovision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	sshConnectTimeout = 15 * time.Second
	maxRemoteOutput   = 64 << 10
)

var ErrHostKeyMismatch = errors.New("SSH host key fingerprint mismatch")

type UnknownHostKeyError struct {
	Fingerprint string
}

func (err *UnknownHostKeyError) Error() string {
	return "SSH host key is not pinned: " + err.Fingerprint
}

func PinnedHostKeyCallback(pinned string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if pinned == "" {
			return &UnknownHostKeyError{Fingerprint: fingerprint}
		}
		if pinned != fingerprint {
			return ErrHostKeyMismatch
		}
		return nil
	}
}

// DiscoverSSHHostKey performs only the SSH transport handshake and returns the
// presented SHA-256 fingerprint. The caller must show it for explicit approval
// before invoking DialSSH with the pin populated.
func DiscoverSSHHostKey(ctx context.Context, request EnrollmentRequest) (string, error) {
	defer zeroBytes(request.Password)
	defer zeroBytes(request.PrivateKeyPEM)
	if ctx == nil {
		return "", ErrInvalidEnrollment
	}
	request.PinnedHostKey = ""
	remote, err := dialSSH(ctx, request, false)
	if remote != nil {
		_ = remote.Close()
		return "", ErrInvalidEnrollment
	}
	var unknown *UnknownHostKeyError
	if errors.As(err, &unknown) && unknown.Fingerprint != "" {
		return unknown.Fingerprint, nil
	}
	return "", err
}

func DialSSH(ctx context.Context, request EnrollmentRequest) (Remote, error) {
	return dialSSH(ctx, request, true)
}

func dialSSH(ctx context.Context, request EnrollmentRequest, requireNodeMetadata bool) (Remote, error) {
	if ctx == nil {
		return nil, ErrInvalidEnrollment
	}
	var validationErr error
	if requireNodeMetadata {
		validationErr = request.Validate()
	} else {
		validationErr = request.ValidateSSHConnection()
	}
	if validationErr != nil {
		return nil, validationErr
	}
	auth := make([]ssh.AuthMethod, 0, 1)
	if len(request.Password) > 0 {
		auth = append(auth, ssh.Password(string(request.Password)))
	} else {
		signer, err := ssh.ParsePrivateKey(request.PrivateKeyPEM)
		if err != nil {
			return nil, ErrInvalidEnrollment
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	configuration := &ssh.ClientConfig{
		User: request.User, Auth: auth, HostKeyCallback: PinnedHostKeyCallback(request.PinnedHostKey),
		Timeout: sshConnectTimeout,
	}
	dialer := net.Dialer{Timeout: sshConnectTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", sshAddress(request))
	if err != nil {
		return nil, fmt.Errorf("SSH TCP connection: %w", err)
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, sshAddress(request), configuration)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	return &sshRemote{client: ssh.NewClient(clientConnection, channels, requests)}, nil
}

type sshRemote struct {
	client *ssh.Client
	once   sync.Once
}

func (remote *sshRemote) Run(ctx context.Context, command string, stdin io.Reader) ([]byte, error) {
	if remote == nil || remote.client == nil || ctx == nil || command == "" {
		return nil, ErrInvalidEnrollment
	}
	session, err := remote.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if stdin != nil {
		session.Stdin = stdin
	}
	stdout := &boundedBuffer{maximum: maxRemoteOutput}
	stderr := &boundedBuffer{maximum: maxRemoteOutput}
	session.Stdout = stdout
	session.Stderr = stderr
	if err := session.Start(command); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case err := <-done:
		output, outputErr := mergeRemoteSessionOutput(stdout, stderr)
		if outputErr != nil {
			return nil, outputErr
		}
		return output, err
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		<-done
		return nil, ctx.Err()
	}
}

func mergeRemoteSessionOutput(stdout, stderr *boundedBuffer) ([]byte, error) {
	if stdout == nil || stderr == nil || stdout.overflow || stderr.overflow || stdout.Len()+stderr.Len() > maxRemoteOutput {
		return nil, errors.New("remote output limit exceeded")
	}
	output := make([]byte, 0, stdout.Len()+stderr.Len())
	output = append(output, stdout.Bytes()...)
	output = append(output, stderr.Bytes()...)
	return output, nil
}

func (remote *sshRemote) Close() error {
	if remote == nil || remote.client == nil {
		return nil
	}
	var err error
	remote.once.Do(func() { err = remote.client.Close() })
	return err
}

type boundedBuffer struct {
	bytes.Buffer
	maximum  int
	overflow bool
}

func (buffer *boundedBuffer) Write(payload []byte) (int, error) {
	if buffer.Buffer.Len()+len(payload) > buffer.maximum {
		remaining := max(0, buffer.maximum-buffer.Buffer.Len())
		if remaining > 0 {
			_, _ = buffer.Buffer.Write(payload[:remaining])
		}
		buffer.overflow = true
		return len(payload), nil
	}
	return buffer.Buffer.Write(payload)
}

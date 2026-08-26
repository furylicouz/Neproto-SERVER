package clienthost

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

type Code string

const (
	CodeUnknown               Code = "UNKNOWN"
	CodeHostUnavailable       Code = "HOST_UNAVAILABLE"
	CodeUnsupportedAPIVersion Code = "UNSUPPORTED_API_VERSION"
	CodeInvalidProfile        Code = "INVALID_PROFILE"
	CodeCredentialUnavailable Code = "CREDENTIAL_UNAVAILABLE"
	CodeNoSafeUplink          Code = "NO_SAFE_UPLINK"
	CodeDNSFailed             Code = "DNS_FAILED"
	CodeUDPUnreachable        Code = "UDP_UNREACHABLE"
	CodeTLSFailed             Code = "TLS_FAILED"
	CodeHTTP3Timeout          Code = "HTTP3_TIMEOUT"
	CodeNP2AuthFailed         Code = "NP2_AUTH_FAILED"
	CodeTUNSetupFailed        Code = "TUN_SETUP_FAILED"
	CodeCancelled             Code = "CANCELLED"
	CodeInternal              Code = "INTERNAL"
)

type Stage string

const (
	StageUnknown             Stage = "UNKNOWN"
	StageHostIPC             Stage = "HOST_IPC"
	StageHostNegotiation     Stage = "HOST_NEGOTIATION"
	StageProfileValidation   Stage = "PROFILE_VALIDATION"
	StageCredentialLoad      Stage = "CREDENTIAL_LOAD"
	StageDNSResolution       Stage = "DNS_RESOLUTION"
	StageEndpointRoute       Stage = "ENDPOINT_ROUTE"
	StageQUICHandshake       Stage = "QUIC_HANDSHAKE"
	StageTLSHandshake        Stage = "TLS_HANDSHAKE"
	StageWebTransportConnect Stage = "WEBTRANSPORT_CONNECT"
	StageNP2Authentication   Stage = "NP2_AUTHENTICATION"
	StageTUNSetup            Stage = "TUN_SETUP"
	StagePacketForwarding    Stage = "PACKET_FORWARDING"
)

type PublicError struct {
	Code        Code   `json:"code"`
	Stage       Stage  `json:"stage"`
	Message     string `json:"message"`
	Retryable   bool   `json:"retryable"`
	OperationID string `json:"operation_id"`
}

func (e PublicError) Validate() error {
	if !validCode(e.Code) || !validStage(e.Stage) ||
		ValidateDiagnosticMessage(e.Message) != nil ||
		ValidateOperationID(e.OperationID) != nil || strings.Contains(e.Message, "np2://") {
		return ErrInvalidInput
	}
	return nil
}

type classifiedError struct {
	public PublicError
	cause  error
}

func (e *classifiedError) Error() string { return e.public.Message }
func (e *classifiedError) Unwrap() error { return e.cause }

func WrapError(code Code, stage Stage, message string, retryable bool, cause error) error {
	if cause == nil {
		return nil
	}
	public := PublicError{
		Code: code, Stage: stage, Message: safeMessage(message), Retryable: retryable,
		OperationID: "internal",
	}
	if public.Validate() != nil {
		public = PublicError{
			Code: CodeInternal, Stage: StageUnknown, Message: "Operation failed.",
			OperationID: "internal",
		}
	}
	return &classifiedError{public: public, cause: cause}
}

func MapError(operationID string, stage Stage, err error) PublicError {
	if err == nil {
		return PublicError{}
	}
	if ValidateOperationID(operationID) != nil {
		operationID = "internal"
	}
	var classified *classifiedError
	if errors.As(err, &classified) {
		result := classified.public
		result.OperationID = operationID
		return result
	}
	if errors.Is(err, context.Canceled) {
		return newPublicError(CodeCancelled, stage, "Operation cancelled.", false, operationID)
	}
	if errors.Is(err, context.DeadlineExceeded) && stage == StageWebTransportConnect {
		return newPublicError(CodeHTTP3Timeout, stage,
			"HTTP/3 WebTransport deadline expired.", true, operationID)
	}
	code, message, retryable := categoryForStage(stage)
	return newPublicError(code, stage, message, retryable, operationID)
}

func newPublicError(code Code, stage Stage, message string, retryable bool, operationID string) PublicError {
	return PublicError{
		Code: code, Stage: stage, Message: safeMessage(message), Retryable: retryable,
		OperationID: operationID,
	}
}

func categoryForStage(stage Stage) (Code, string, bool) {
	switch stage {
	case StageHostIPC:
		return CodeHostUnavailable, "Native host is unavailable.", true
	case StageHostNegotiation:
		return CodeUnsupportedAPIVersion, "Host API version is unsupported.", false
	case StageProfileValidation:
		return CodeInvalidProfile, "Profile is invalid.", false
	case StageCredentialLoad:
		return CodeCredentialUnavailable, "Credential is unavailable.", false
	case StageEndpointRoute:
		return CodeNoSafeUplink, "No safe physical uplink is available.", false
	case StageDNSResolution:
		return CodeDNSFailed, "Carrier host resolution failed.", true
	case StageQUICHandshake:
		return CodeUDPUnreachable, "UDP path could not establish QUIC.", true
	case StageTLSHandshake:
		return CodeTLSFailed, "TLS negotiation failed.", false
	case StageNP2Authentication:
		return CodeNP2AuthFailed, "NP/2 authentication failed.", false
	case StageTUNSetup:
		return CodeTUNSetupFailed, "Packet tunnel setup failed.", false
	default:
		return CodeInternal, "Operation failed.", false
	}
}

func safeMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" || !utf8.ValidString(message) || strings.Contains(message, "np2://") {
		return "Operation failed."
	}
	if len(message) <= MaxDiagnosticMessageBytes {
		return message
	}
	for len(message) > MaxDiagnosticMessageBytes {
		_, size := utf8.DecodeLastRuneInString(message)
		message = message[:len(message)-size]
	}
	return message
}

func validCode(code Code) bool {
	switch code {
	case CodeHostUnavailable, CodeUnsupportedAPIVersion, CodeInvalidProfile,
		CodeCredentialUnavailable, CodeNoSafeUplink, CodeDNSFailed, CodeUDPUnreachable,
		CodeTLSFailed, CodeHTTP3Timeout, CodeNP2AuthFailed, CodeTUNSetupFailed,
		CodeCancelled, CodeInternal:
		return true
	default:
		return false
	}
}

func validStage(stage Stage) bool {
	switch stage {
	case StageHostIPC, StageHostNegotiation, StageProfileValidation, StageCredentialLoad,
		StageDNSResolution, StageEndpointRoute, StageQUICHandshake, StageTLSHandshake,
		StageWebTransportConnect, StageNP2Authentication, StageTUNSetup,
		StagePacketForwarding, StageUnknown:
		return true
	default:
		return false
	}
}

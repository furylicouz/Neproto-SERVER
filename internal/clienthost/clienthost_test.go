package clienthost

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateOperationIDRequiresBoundedPrintableASCII(t *testing.T) {
	for _, value := range []string{"connect-01", strings.Repeat("a", 64)} {
		if err := ValidateOperationID(value); err != nil {
			t.Fatalf("ValidateOperationID(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", " leading", "trailing ", "line\nbreak", strings.Repeat("a", 65)} {
		if err := ValidateOperationID(value); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ValidateOperationID(%q) error = %v", value, err)
		}
	}
}

func TestValidateOnboardingValueCapsUTF8Bytes(t *testing.T) {
	if err := ValidateOnboardingValue("np2://import/v2/value"); err != nil {
		t.Fatalf("ValidateOnboardingValue: %v", err)
	}
	if err := ValidateOnboardingValue("np2://import/v2/" + strings.Repeat("я", MaxOnboardingBytes)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized onboarding error = %v", err)
	}
}

func TestValidateFirstSliceInputBounds(t *testing.T) {
	if err := ValidateProfileID(strings.Repeat("a", MaxProfileIDBytes)); err != nil {
		t.Fatalf("maximum profile ID: %v", err)
	}
	if err := ValidateProfileID(strings.Repeat("a", MaxProfileIDBytes+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized profile ID error = %v", err)
	}
	if err := ValidateDisplayName(strings.Repeat("я", MaxDisplayNameRunes)); err != nil {
		t.Fatalf("maximum display name: %v", err)
	}
	if err := ValidateDisplayName(strings.Repeat("я", MaxDisplayNameRunes+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized display name error = %v", err)
	}
	if err := ValidateDiagnosticsLimit(MaxDiagnosticsEntries); err != nil {
		t.Fatalf("maximum diagnostics limit: %v", err)
	}
	if err := ValidateDiagnosticsLimit(MaxDiagnosticsEntries + 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized diagnostics limit error = %v", err)
	}
	if err := ValidateDiagnosticMessage(strings.Repeat("я", MaxDiagnosticMessageBytes/2)); err != nil {
		t.Fatalf("maximum diagnostic message: %v", err)
	}
	if err := ValidateDiagnosticMessage(strings.Repeat("я", MaxDiagnosticMessageBytes/2+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized diagnostic message error = %v", err)
	}
	if err := ValidateFrameSize(MaxMessageBytes); err != nil {
		t.Fatalf("maximum frame: %v", err)
	}
	if err := ValidateFrameSize(MaxMessageBytes + 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized frame error = %v", err)
	}
}

func TestMapErrorReturnsStableRedactedCategory(t *testing.T) {
	cause := errors.New("dial failed for np2://import/v2/private-value")
	got := MapError("connect-01", StageWebTransportConnect, cause)
	if got.Code != CodeInternal || got.Stage != StageWebTransportConnect || got.Retryable {
		t.Fatalf("MapError = %+v", got)
	}
	if got.Message != "Operation failed." || strings.Contains(got.Message, "np2://") {
		t.Fatalf("unsafe public message %q", got.Message)
	}
}

func TestMapErrorClassifiesHTTP3TimeoutWithoutRawDetails(t *testing.T) {
	got := MapError("connect-02", StageWebTransportConnect, context.DeadlineExceeded)
	if got.Code != CodeHTTP3Timeout || !got.Retryable {
		t.Fatalf("MapError = %+v", got)
	}
	if got.Message != "HTTP/3 WebTransport deadline expired." {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestClassifiedErrorKeepsCauseInternal(t *testing.T) {
	cause := errors.New("credential backend detail")
	err := WrapError(CodeCredentialUnavailable, StageCredentialLoad,
		"Credential is unavailable.", false, cause)
	if !errors.Is(err, cause) {
		t.Fatal("wrapped error does not preserve its internal cause")
	}
	got := MapError("import-01", StageUnknown, err)
	if got.Code != CodeCredentialUnavailable || got.Stage != StageCredentialLoad ||
		got.Message != "Credential is unavailable." {
		t.Fatalf("MapError = %+v", got)
	}
}

func TestSnapshotValidationRejectsUnknownSuccessAndNegativeCounters(t *testing.T) {
	valid := Snapshot{State: StateConnected, Carrier: CarrierHTTP3WebTransport, Sequence: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid snapshot: %v", err)
	}
	for _, snapshot := range []Snapshot{
		{State: StateConnected, Carrier: CarrierUnknown, Sequence: 1},
		{State: StateUnknown, Carrier: CarrierHTTP3WebTransport, Sequence: 1},
		{State: StateConnected, Carrier: CarrierHTTP3WebTransport, UploadTotalBytes: -1, Sequence: 1},
	} {
		if err := snapshot.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("snapshot %+v error = %v", snapshot, err)
		}
	}
}

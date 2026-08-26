package clienthost

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxOperationIDBytes       = 64
	MaxOnboardingBytes        = 16 << 10
	MaxProfileIDBytes         = 128
	MaxDisplayNameRunes       = 128
	MaxMessageBytes           = 256 << 10
	MaxDiagnosticsEntries     = 256
	MaxDiagnosticMessageBytes = 512
)

var ErrInvalidInput = errors.New("invalid client host input")

func ValidateOperationID(value string) error {
	if len(value) == 0 || len(value) > MaxOperationIDBytes {
		return ErrInvalidInput
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return ErrInvalidInput
		}
	}
	return nil
}

func ValidateOnboardingValue(value string) error {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		len(value) == 0 || len(value) > MaxOnboardingBytes ||
		(!strings.HasPrefix(value, "np2://import/v1/") &&
			!strings.HasPrefix(value, "np2://import/v2/")) {
		return ErrInvalidInput
	}
	return nil
}

func ValidateProfileID(value string) error {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		len(value) == 0 || len(value) > MaxProfileIDBytes {
		return ErrInvalidInput
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return ErrInvalidInput
		}
	}
	return nil
}

func ValidateDisplayName(value string) error {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || value == "" ||
		utf8.RuneCountInString(value) > MaxDisplayNameRunes {
		return ErrInvalidInput
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return ErrInvalidInput
		}
	}
	return nil
}

func ValidateDiagnosticsLimit(value int) error {
	if value < 1 || value > MaxDiagnosticsEntries {
		return ErrInvalidInput
	}
	return nil
}

func ValidateDiagnosticMessage(value string) error {
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > MaxDiagnosticMessageBytes {
		return ErrInvalidInput
	}
	return nil
}

func ValidateFrameSize(value int) error {
	if value < 1 || value > MaxMessageBytes {
		return ErrInvalidInput
	}
	return nil
}

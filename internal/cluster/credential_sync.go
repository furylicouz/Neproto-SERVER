package cluster

import (
	"encoding/base64"
	"errors"
)

const CredentialSyncVersion = 1

type CredentialSyncOperation string

const (
	CredentialSyncUpsert CredentialSyncOperation = "upsert"
	CredentialSyncRevoke CredentialSyncOperation = "revoke"
)

var ErrInvalidCredentialSync = errors.New("invalid cluster credential sync request")

type CredentialSyncRequest struct {
	Version      int                     `json:"version"`
	Operation    CredentialSyncOperation `json:"operation"`
	CredentialID string                  `json:"credential_id"`
	Secret       string                  `json:"secret,omitempty"`
}

func ValidateCredentialSync(request CredentialSyncRequest) error {
	identifier, err := base64.RawURLEncoding.DecodeString(request.CredentialID)
	if request.Version != CredentialSyncVersion || err != nil || len(identifier) != 16 ||
		base64.RawURLEncoding.EncodeToString(identifier) != request.CredentialID {
		return ErrInvalidCredentialSync
	}
	switch request.Operation {
	case CredentialSyncUpsert:
		secret, err := base64.RawURLEncoding.DecodeString(request.Secret)
		if err != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != request.Secret || allZeroBytes(secret) {
			return ErrInvalidCredentialSync
		}
	case CredentialSyncRevoke:
		if request.Secret != "" {
			return ErrInvalidCredentialSync
		}
	default:
		return ErrInvalidCredentialSync
	}
	return nil
}

func allZeroBytes(value []byte) bool {
	for _, character := range value {
		if character != 0 {
			return false
		}
	}
	return true
}

package cluster

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestValidateCredentialSyncStrictlySeparatesUpsertAndRevoke(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	if err := ValidateCredentialSync(CredentialSyncRequest{Version: 1, Operation: CredentialSyncUpsert, CredentialID: id, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCredentialSync(CredentialSyncRequest{Version: 1, Operation: CredentialSyncRevoke, CredentialID: id}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []CredentialSyncRequest{
		{Version: 1, Operation: CredentialSyncUpsert, CredentialID: id},
		{Version: 1, Operation: CredentialSyncRevoke, CredentialID: id, Secret: secret},
		{Version: 1, Operation: CredentialSyncUpsert, CredentialID: id, Secret: base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
	} {
		if err := ValidateCredentialSync(request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
}

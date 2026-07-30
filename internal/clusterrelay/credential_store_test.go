package clusterrelay

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"neproto.local/chameleon/internal/cluster"
)

func TestCredentialSyncHandlerAcceptsOnlyMasterAndPersistsAtomically(t *testing.T) {
	directory := t.TempDir()
	handler, err := NewCredentialSyncHandler("edge", "master", map[string]string{"pair-id": "master"}, directory)
	if err != nil {
		t.Fatal(err)
	}
	id := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	upsert := cluster.CredentialSyncRequest{Version: 1, Operation: cluster.CredentialSyncUpsert, CredentialID: id, Secret: secret}
	if err := handler(context.Background(), "client-not-master", upsert); err == nil {
		t.Fatal("non-master peer changed credential store")
	}
	if err := handler(context.Background(), "pair-id", upsert); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, id+".secret")
	if raw, err := os.ReadFile(path); err != nil || string(raw) != secret+"\n" {
		t.Fatalf("stored credential=%q err=%v", raw, err)
	}
	if err := handler(context.Background(), "pair-id", cluster.CredentialSyncRequest{Version: 1, Operation: cluster.CredentialSyncRevoke, CredentialID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("revoked credential remains: %v", err)
	}
}

package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/onboarding"
)

func TestManagerAddsListsAndExportsIndependentUser(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	manager, err := Open(root, bytes.NewReader(bytes.Repeat([]byte{0x42}, 128)), func() time.Time {
		return time.Date(2026, time.July, 17, 21, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}
	user, err := manager.AddUser("Alice iPhone", "web")
	if err != nil {
		t.Fatalf("add user: %v", err)
	}
	if user.ID == "" || user.Name != "Alice iPhone" || user.Profile != "web" || user.Status != StatusActive {
		t.Fatalf("unexpected user: %#v", user)
	}
	secretPath := filepath.Join(root, "etc", "neproto", "users", "active", user.ID+".secret")
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("stat active secret: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode=%o", info.Mode().Perm())
	}
	uri, err := manager.ExportUserURI(user.ID)
	if err != nil {
		t.Fatalf("export URI: %v", err)
	}
	profile, err := onboarding.DecodeURI(uri)
	if err != nil {
		t.Fatalf("decode exported URI: %v", err)
	}
	if profile.CredentialID != user.ID || profile.Name != user.Name || profile.Profile != "web" ||
		profile.ServerIdentity != "vpn.example.com" || len(profile.ServerAddresses) != 1 ||
		profile.Version != 2 || profile.HTTP3Path != "/private_http3_route_01234567890" ||
		!profile.RequireDatagrams || profile.MaxParallelCarriers != 3 ||
		!profile.EnableConstellation || !profile.EnableForwardSecrecy {
		t.Fatalf("exported profile mismatch: %#v", profile)
	}
	indexRaw, err := os.ReadFile(filepath.Join(root, "etc", "neproto", "users", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(indexRaw, []byte(profile.Secret)) {
		t.Fatal("user index contains credential material")
	}
	users, err := manager.ListUsers()
	if err != nil || len(users) != 1 || users[0].ID != user.ID {
		t.Fatalf("list users=%#v err=%v", users, err)
	}
}

func TestManagerRotatesAndRevokesCredentialAtomically(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	random := append(bytes.Repeat([]byte{0x11}, 48), bytes.Repeat([]byte{0x22}, 64)...)
	manager, err := Open(root, bytes.NewReader(random), func() time.Time {
		return time.Date(2026, time.July, 17, 22, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Bob", "interactive")
	if err != nil {
		t.Fatal(err)
	}
	beforeURI, err := manager.ExportUserURI(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := onboarding.DecodeURI(beforeURI)
	if err := manager.RotateUser(user.ID); err != nil {
		t.Fatalf("rotate user: %v", err)
	}
	afterURI, err := manager.ExportUserURI(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := onboarding.DecodeURI(afterURI)
	if before.Secret == after.Secret {
		t.Fatal("rotation preserved old credential")
	}
	if err := manager.RevokeUser(user.ID); err != nil {
		t.Fatalf("revoke user: %v", err)
	}
	if _, err := manager.ExportUserURI(user.ID); err == nil {
		t.Fatal("revoked user remained exportable")
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "neproto", "users", "active", user.ID+".secret")); !os.IsNotExist(err) {
		t.Fatalf("active credential survived revocation: %v", err)
	}
	users, err := manager.ListUsers()
	if err != nil || len(users) != 1 || users[0].Status != StatusRevoked || users[0].RevokedAt == nil {
		t.Fatalf("revoked index=%#v err=%v", users, err)
	}
}

func TestManagerUpdatesDevicePolicyAndTrafficResetGeneration(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	manager, err := Open(root, bytes.NewReader(bytes.Repeat([]byte{0x73}, 256)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Limited phone", "web")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.SetUserDeviceLimit(user.ID, 2)
	if err != nil {
		t.Fatalf("set device limit: %v", err)
	}
	if updated.MaxDevices != 2 {
		t.Fatalf("max devices=%d", updated.MaxDevices)
	}
	if _, err := manager.SetUserDeviceLimit(user.ID, 17); !errors.Is(err, ErrInvalidUser) {
		t.Fatalf("oversized device limit error=%v", err)
	}
	reset, err := manager.ResetUserTraffic(user.ID)
	if err != nil {
		t.Fatalf("reset traffic: %v", err)
	}
	if reset.TrafficResetGeneration != 1 || reset.TrafficResetAt == nil || !reset.TrafficResetAt.Equal(now) {
		t.Fatalf("traffic reset state=%#v", reset)
	}
	reloaded, err := Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	users, err := reloaded.ListUsers()
	if err != nil || len(users) != 1 || users[0].MaxDevices != 2 || users[0].TrafficResetGeneration != 1 {
		t.Fatalf("persisted users=%#v err=%v", users, err)
	}
}

func TestManagerPermanentlyDeletesOnlyRevokedUserAndClusterAccess(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	manager, err := Open(root, bytes.NewReader(bytes.Repeat([]byte{0x63}, 256)), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Disposable iPhone", "web")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetClusterUserAccess(cluster.UserAccess{
		UserID: user.ID, AllowedNodeIDs: []string{state.Nodes[0].ID}, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteUser(user.ID); !errors.Is(err, ErrUserMustBeRevoked) {
		t.Fatalf("active user deletion error=%v", err)
	}
	if err := manager.RevokeUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	users, err := manager.ListUsers()
	if err != nil || len(users) != 0 {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	state, err = manager.ClusterState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Access) != 0 {
		t.Fatalf("deleted user retained cluster access: %+v", state.Access)
	}
	entries, err := os.ReadDir(filepath.Join(root, "etc", "neproto", "users", "revoked"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), user.ID) {
			t.Fatalf("deleted credential remains: %s", entry.Name())
		}
	}
	if _, err := manager.AddUser("Disposable iPhone", "web"); err != nil {
		t.Fatalf("deleted user name was not released: %v", err)
	}
}

func TestManagerRejectsTraversalAndDuplicateNames(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	manager, err := Open(root, bytes.NewReader(bytes.Repeat([]byte{0x51}, 256)), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUser("../root", "web"); err == nil {
		t.Fatal("accepted unsafe user name")
	}
	if _, err := manager.AddUser("Alice", "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUser("alice", "web"); err == nil {
		t.Fatal("accepted duplicate case-insensitive user name")
	}
	if _, err := manager.ExportUserURI("../../etc/passwd"); err == nil {
		t.Fatal("accepted credential ID traversal")
	}
}

func TestManagerSetDomainUpdatesInstallationServerAndIngressAtomically(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	writeRuntimeConfigs(t, root)
	serverDirectory := filepath.Join(root, "etc", "neproto")
	caddyDirectory := filepath.Join(root, "etc", "caddy")
	manager, err := Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.SetDomain("new.example.com", []string{"1.1.1.1", "2606:4700:4700::1111"}); err != nil {
		t.Fatal(err)
	}
	installation := manager.Installation()
	if installation.Domain != "new.example.com" || len(installation.ServerAddresses) != 2 {
		t.Fatalf("installation=%+v", installation)
	}
	updatedServer, err := os.ReadFile(filepath.Join(serverDirectory, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updatedServer, []byte(`"server_identity": "new.example.com"`)) {
		t.Fatalf("server config was not updated: %s", updatedServer)
	}
	updatedCaddy, err := os.ReadFile(filepath.Join(caddyDirectory, "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updatedCaddy, []byte("new.example.com {")) || bytes.Contains(updatedCaddy, []byte("vpn.example.com {")) {
		t.Fatalf("Caddyfile was not updated: %s", updatedCaddy)
	}
}

func TestManagerSetFeaturesUpdatesInstallationAndServerAtomically(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	writeRuntimeConfigs(t, root)
	manager, err := Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetFeatures(false, false); err != nil {
		t.Fatal(err)
	}
	installation := manager.Installation()
	if installation.EnableConstellation || installation.EnableForwardSecrecy {
		t.Fatalf("features were not disabled: %+v", installation)
	}
	serverRaw, err := os.ReadFile(filepath.Join(root, "etc", "neproto", "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var server map[string]any
	if err := json.Unmarshal(serverRaw, &server); err != nil {
		t.Fatal(err)
	}
	if server["enable_constellation"] != false || server["enable_forward_secrecy"] != false {
		t.Fatalf("server features=%v", server)
	}
	if err := manager.SetFeatures(true, true); err != nil {
		t.Fatal(err)
	}
	if installation = manager.Installation(); !installation.EnableConstellation ||
		!installation.EnableForwardSecrecy {
		t.Fatalf("features were not enabled: %+v", installation)
	}
}

func TestManagerBackupRestoreRecoversDomainAndCredential(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	writeRuntimeConfigs(t, root)
	random := bytes.NewReader(bytes.Repeat([]byte{0x61}, 512))
	manager, err := Open(root, random, func() time.Time {
		return time.Date(2026, time.July, 18, 1, 2, 3, 4, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Backup iPhone", "web")
	if err != nil {
		t.Fatal(err)
	}
	beforeURI, err := manager.ExportUserURI(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	backupPath, err := manager.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RotateUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetDomain("changed.example.com", []string{"1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	recoveryPath, err := manager.RestoreBackup(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if recoveryPath == "" || recoveryPath == backupPath {
		t.Fatalf("recovery backup=%q", recoveryPath)
	}
	afterURI, err := manager.ExportUserURI(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterURI != beforeURI {
		t.Fatal("restore did not recover the original client credential")
	}
	if manager.Installation().Domain != "vpn.example.com" {
		t.Fatalf("restored domain=%q", manager.Installation().Domain)
	}
	backups, err := manager.ListBackups()
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
}

func writeRuntimeConfigs(t *testing.T, root string) {
	t.Helper()
	serverDirectory := filepath.Join(root, "etc", "neproto")
	caddyDirectory := filepath.Join(root, "etc", "caddy")
	if err := os.MkdirAll(caddyDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	serverJSON := `{"server_identity":"vpn.example.com","listen":"127.0.0.1:9080","enable_constellation":true,"enable_forward_secrecy":true}`
	if err := os.WriteFile(filepath.Join(serverDirectory, "server.json"), []byte(serverJSON), 0o640); err != nil {
		t.Fatal(err)
	}
	caddyfile := "vpn.example.com {\n\treverse_proxy 127.0.0.1:9080\n}\n"
	if err := os.WriteFile(filepath.Join(caddyDirectory, "Caddyfile"), []byte(caddyfile), 0o640); err != nil {
		t.Fatal(err)
	}
}

func writeInstallation(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "etc", "neproto")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	state := Installation{
		Version: 1, Mode: ModeBareMetal, Domain: "vpn.example.com",
		ServerAddresses:      []string{"8.8.8.8"},
		HTTPSPath:            "/private_https_route_0123456789",
		WebRTCPath:           "/private_webrtc_route_0123456789",
		HTTP3Path:            "/private_http3_route_01234567890",
		RequireDatagrams:     true,
		EnableConstellation:  true,
		EnableForwardSecrecy: true,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "installation.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateInstallationAcceptsBoundedWebPublication(t *testing.T) {
	state := Installation{
		Version: 1, Mode: ModeBareMetal, Domain: "vpn.example.com",
		ServerAddresses:      []string{"8.8.8.8"},
		HTTPSPath:            "/private_https_route_0123456789",
		WebRTCPath:           "/private_webrtc_route_0123456789",
		HTTP3Path:            "/private_http3_route_01234567890",
		EnableConstellation:  true,
		EnableForwardSecrecy: true,
		WebEnabled:           true,
		WebDomain:            "admin.example.com",
		WebPort:              3000,
	}
	if err := validateInstallation(state); err != nil {
		t.Fatalf("valid web publication rejected: %v", err)
	}
	for _, mutate := range []func(*Installation){
		func(candidate *Installation) { candidate.WebPort = 0 },
		func(candidate *Installation) { candidate.WebPort = 443 },
		func(candidate *Installation) { candidate.WebPort = 40000 },
		func(candidate *Installation) { candidate.WebDomain = candidate.Domain },
		func(candidate *Installation) { candidate.WebDomain = "bad value.example.com" },
	} {
		candidate := state
		mutate(&candidate)
		if validateInstallation(candidate) == nil {
			t.Fatalf("invalid web publication accepted: %+v", candidate)
		}
	}
}

package clusterprovision

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"neproto.local/chameleon/internal/cluster"
)

func TestEncodeBootstrapMatchesStrictClusterInstallerSchema(t *testing.T) {
	bootstrap := validBootstrap()
	raw, err := EncodeBootstrap(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 21 || decoded["node_id"] != "edge-fi" || decoded["peer_secret"] != bootstrap.PeerSecret {
		t.Fatalf("unexpected bootstrap document: %#v", decoded)
	}
}

func TestBootstrapRejectsMasterRoleDuplicatePathsAndPrivateAddresses(t *testing.T) {
	for name, mutate := range map[string]func(*Bootstrap){
		"master role":     func(value *Bootstrap) { value.Roles = []cluster.NodeRole{cluster.RoleMaster} },
		"duplicate paths": func(value *Bootstrap) { value.WebRTCPath = value.HTTPSPath },
		"private address": func(value *Bootstrap) { value.Addresses = []string{"10.0.0.1"} },
		"zero secret":     func(value *Bootstrap) { value.PeerSecret = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) },
	} {
		t.Run(name, func(t *testing.T) {
			value := validBootstrap()
			mutate(&value)
			if _, err := EncodeBootstrap(value); err == nil {
				t.Fatal("invalid bootstrap accepted")
			}
		})
	}
}

func validBootstrap() Bootstrap {
	return Bootstrap{
		Version: BootstrapVersion, Mode: "bare-metal", Domain: "edge.example.com", Addresses: []string{"1.1.1.1"},
		HTTPSPath: testPath('1'), WebRTCPath: testPath('2'), HTTP3Path: testPath('3'),
		ClusterID: "cluster-01", NodeID: "edge-fi", Name: "Finland Edge", Region: "Helsinki",
		Roles: []cluster.NodeRole{cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress}, MasterNodeID: "master",
		MasterDomain: "master.example.com", MasterAddresses: []string{"8.8.8.8"},
		MasterHTTPSPath: testPath('4'), MasterWebRTCPath: testPath('5'), MasterHTTP3Path: testPath('6'),
		PeerCredentialID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16)),
		PeerSecret:       base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)),
	}
}

func testPath(fill byte) string { return "/" + string(bytes.Repeat([]byte{fill}, 48)) }

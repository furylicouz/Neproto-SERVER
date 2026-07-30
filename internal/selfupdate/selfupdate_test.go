package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"neproto.local/chameleon/internal/admin"
)

func TestParseVersionRequiresCanonicalReleaseTag(t *testing.T) {
	version, err := ParseVersion("np2-12.34.56")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if version.Major != 12 || version.Minor != 34 || version.Patch != 56 {
		t.Fatalf("unexpected version: %+v", version)
	}
	for _, invalid := range []string{"0.4.0", "np2-0.4", "np2-01.4.0", "np2-0.4.0-rc1", "np2-0.4.0\n"} {
		if _, err := ParseVersion(invalid); err == nil {
			t.Fatalf("ParseVersion(%q) succeeded", invalid)
		}
	}
}

func TestParseLatestReleaseAcceptsOnlyNewStableRelease(t *testing.T) {
	payload := `{"tag_name":"np2-0.4.1","draft":false,"prerelease":false}`
	release, err := ParseLatestRelease(strings.NewReader(payload), "np2-0.4.0")
	if err != nil {
		t.Fatalf("ParseLatestRelease: %v", err)
	}
	if release.Tag != "np2-0.4.1" || !release.Available {
		t.Fatalf("unexpected release: %+v", release)
	}
	wantArchive := "https://github.com/furylicouz/Neproto-SERVER/releases/download/np2-0.4.1/neproto-server-bundle-np2-0.4.1.tar.gz"
	if release.ArchiveURL != wantArchive || release.ChecksumURL != wantArchive+".sha256" {
		t.Fatalf("untrusted release URLs: %+v", release)
	}

	for _, payload := range []string{
		`{"tag_name":"np2-0.4.1","draft":true,"prerelease":false}`,
		`{"tag_name":"np2-0.4.1","draft":false,"prerelease":true}`,
		`{"tag_name":"other","draft":false,"prerelease":false}`,
	} {
		if _, err := ParseLatestRelease(strings.NewReader(payload), "np2-0.4.0"); err == nil {
			t.Fatalf("accepted invalid release %s", payload)
		}
	}
}

func TestParseLatestReleaseRejectsTrailingJSON(t *testing.T) {
	payload := `{"tag_name":"np2-0.4.1","draft":false,"prerelease":false}{"tag_name":"np2-9.9.9"}`
	if _, err := ParseLatestRelease(strings.NewReader(payload), "np2-0.4.0"); err == nil {
		t.Fatal("release metadata with trailing JSON was accepted")
	}
}

func TestParseLatestReleaseDoesNotOfferSameOrOlderVersion(t *testing.T) {
	for _, tag := range []string{"np2-0.4.0", "np2-0.3.9"} {
		release, err := ParseLatestRelease(strings.NewReader(`{"tag_name":"`+tag+`","draft":false,"prerelease":false}`), "np2-0.4.0")
		if err != nil {
			t.Fatalf("ParseLatestRelease(%s): %v", tag, err)
		}
		if release.Available {
			t.Fatalf("offered non-newer version %s", tag)
		}
	}
}

func TestReleaseHTTPClientRejectsRedirectOutsidePinnedGitHubHosts(t *testing.T) {
	client := newReleaseHTTPClient()
	original, _ := url.Parse("https://github.com/furylicouz/Neproto-SERVER/releases/download/np2-0.4.1/archive")
	allowed, _ := url.Parse("https://release-assets.githubusercontent.com/github-production-release-asset/file")
	blocked, _ := url.Parse("https://attacker.example/payload")
	via := []*http.Request{{URL: original}}
	if err := client.CheckRedirect(&http.Request{URL: allowed}, via); err != nil {
		t.Fatalf("trusted release redirect rejected: %v", err)
	}
	if err := client.CheckRedirect(&http.Request{URL: blocked}, via); err == nil {
		t.Fatal("untrusted release redirect accepted")
	}
}

func TestReleaseHTTPClientUsesOperationContextForLargeBundleDeadline(t *testing.T) {
	client := newReleaseHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("whole-response timeout %s can truncate a valid large release", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout <= 0 || transport.TLSHandshakeTimeout <= 0 {
		t.Fatal("release client lacks bounded connection and response-header deadlines")
	}
}

func TestParseChecksumRequiresExpectedArchive(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := ParseChecksum(strings.NewReader(digest+"  neproto-server-bundle-np2-0.4.1.tar.gz\n"), "neproto-server-bundle-np2-0.4.1.tar.gz")
	if err != nil {
		t.Fatalf("ParseChecksum: %v", err)
	}
	if got != digest {
		t.Fatalf("digest = %q", got)
	}
	for _, invalid := range []string{
		digest + "  other.tar.gz\n",
		digest + "  neproto-server-bundle-np2-0.4.1.tar.gz\n" + digest + "  extra\n",
		"not-a-digest  neproto-server-bundle-np2-0.4.1.tar.gz\n",
	} {
		if _, err := ParseChecksum(strings.NewReader(invalid), "neproto-server-bundle-np2-0.4.1.tar.gz"); err == nil {
			t.Fatalf("accepted invalid checksum %q", invalid)
		}
	}
}

func TestExtractBundleRejectsTraversalAndLinks(t *testing.T) {
	for name, header := range map[string]tar.Header{
		"traversal": {Name: "../escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg},
		"absolute":  {Name: "/escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg},
		"symlink":   {Name: "neproto-server-bundle-np2-0.4.1/link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink},
	} {
		t.Run(name, func(t *testing.T) {
			archive := tarGzip(t, []tar.Header{header}, []byte("x"))
			if _, err := ExtractBundle(bytes.NewReader(archive), t.TempDir(), "np2-0.4.1"); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestExtractBundleRequiresExpectedRootAndInstaller(t *testing.T) {
	installer := []byte("#!/bin/sh\nexit 0\n")
	headers := []tar.Header{
		{Name: "neproto-server-bundle-np2-0.4.1/", Mode: 0o755, Typeflag: tar.TypeDir},
		{Name: "neproto-server-bundle-np2-0.4.1/install.sh", Mode: 0o755, Size: int64(len(installer)), Typeflag: tar.TypeReg},
	}
	archive := tarGzip(t, headers, installer)
	root, err := ExtractBundle(bytes.NewReader(archive), t.TempDir(), "np2-0.4.1")
	if err != nil {
		t.Fatalf("ExtractBundle: %v", err)
	}
	if !strings.HasSuffix(root, "neproto-server-bundle-np2-0.4.1") {
		t.Fatalf("unexpected root: %s", root)
	}
}

func TestInstallerArgumentsPreserveInstalledTopology(t *testing.T) {
	uid, gid := 65532, 65532
	installation := admin.Installation{
		Version: 1, Mode: admin.ModeDocker, Domain: "vpn.example.com",
		ServerAddresses: []string{"203.0.113.10", "2001:db8::10"},
		HTTPSPath:       "/111111111111111111111111111111111111111111111111",
		WebRTCPath:      "/222222222222222222222222222222222222222222222222",
		HTTP3Path:       "/333333333333333333333333333333333333333333333333",
		WebEnabled:      true, WebDomain: "admin.example.com", WebPort: 3100,
		ServiceUID: &uid, ServiceGID: &gid,
	}
	got, err := InstallerArguments(installation, "ops@example.com")
	if err != nil {
		t.Fatalf("InstallerArguments: %v", err)
	}
	want := []string{
		"--mode", "docker", "--domain", "vpn.example.com",
		"--addresses", "203.0.113.10,2001:db8::10", "--email", "ops@example.com",
		"--web-domain", "admin.example.com", "--web-port", "3100",
		"--https-path", installation.HTTPSPath, "--webrtc-path", installation.WebRTCPath,
		"--http3-path", installation.HTTP3Path, "--non-interactive",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments:\n got %#v\nwant %#v", got, want)
	}
}

func TestProgressIsMonotonicForActiveStages(t *testing.T) {
	last := -1
	for _, stage := range []string{"checking", "downloading", "verifying", "extracting", "backing_up", "installing", "restarting", "succeeded"} {
		progress, ok := ProgressForStage(stage)
		if !ok || progress < last || progress < 0 || progress > 100 {
			t.Fatalf("stage %s progress %d after %d", stage, progress, last)
		}
		last = progress
	}
}

func tarGzip(t *testing.T, headers []tar.Header, content []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	for _, header := range headers {
		header := header
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size > 0 {
			if _, err := tw.Write(content[:header.Size]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

package geodata

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdaterVerifiesValidatesAndAtomicallyActivatesPair(t *testing.T) {
	directory := t.TempDir()
	oldIP, oldSite := testGeoDataPair("NL", "OLD", "old.example")
	writeGeoDataPair(t, directory, oldIP, oldSite)
	newIP, newSite := testGeoDataPair("DE", "OPENAI", "chatgpt.com")
	server := geoDataTestServer(t, newIP, newSite)
	defer server.Close()

	updater := testUpdater(server.URL)
	status, err := updater.Update(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != UpdateStateReady || status.GeoIPSHA256 != sha256Hex(newIP) || status.GeoSiteSHA256 != sha256Hex(newSite) {
		t.Fatalf("unexpected status: %+v", status)
	}
	engine, err := Load(directory)
	if err != nil || !engine.HasGeoIP("de") || !engine.HasGeoSite("openai") {
		t.Fatalf("new pair was not activated: engine=%v err=%v", engine, err)
	}
}

func TestUpdaterPreservesPreviousPairWhenDownloadedDatabaseIsInvalid(t *testing.T) {
	directory := t.TempDir()
	oldIP, oldSite := testGeoDataPair("NL", "OPENAI", "chatgpt.com")
	writeGeoDataPair(t, directory, oldIP, oldSite)
	newIP, _ := testGeoDataPair("DE", "OTHER", "example.org")
	server := geoDataTestServer(t, newIP, []byte{0xff, 0xff, 0xff})
	defer server.Close()

	if _, err := testUpdater(server.URL).Update(context.Background(), directory); err == nil {
		t.Fatal("invalid downloaded database was accepted")
	}
	assertFileEquals(t, filepath.Join(directory, "geoip.dat"), oldIP)
	assertFileEquals(t, filepath.Join(directory, "geosite.dat"), oldSite)
}

func testUpdater(baseURL string) *Updater {
	return &Updater{
		Client:    http.DefaultClient,
		allowHTTP: true,
		Sources: []Source{
			{Name: "geoip.dat", URL: baseURL + "/geoip.dat", ChecksumURL: baseURL + "/geoip.dat.sha256sum"},
			{Name: "geosite.dat", URL: baseURL + "/geosite.dat", ChecksumURL: baseURL + "/geosite.dat.sha256sum"},
		},
	}
}

func geoDataTestServer(t *testing.T, geoIP, geoSite []byte) *httptest.Server {
	t.Helper()
	files := map[string][]byte{"/geoip.dat": geoIP, "/geosite.dat": geoSite}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if raw, ok := files[request.URL.Path]; ok {
			_, _ = writer.Write(raw)
			return
		}
		for name, raw := range files {
			if request.URL.Path == name+".sha256sum" {
				_, _ = fmt.Fprintf(writer, "%s  %s\n", sha256Hex(raw), filepath.Base(name))
				return
			}
		}
		http.NotFound(writer, request)
	}))
}

func testGeoDataPair(country, category, domainName string) ([]byte, []byte) {
	cidr := message(bytesField(1, []byte{203, 0, 113, 0}), varintField(2, 24))
	geoIP := bytesField(1, message(bytesField(1, []byte(country)), bytesField(2, cidr)))
	domain := message(varintField(1, 2), bytesField(2, []byte(domainName)))
	geoSite := bytesField(1, message(bytesField(1, []byte(category)), bytesField(2, domain)))
	return geoIP, geoSite
}

func writeGeoDataPair(t *testing.T, directory string, geoIP, geoSite []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "geoip.dat"), geoIP, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "geosite.dat"), geoSite, 0o640); err != nil {
		t.Fatal(err)
	}
}

func assertFileEquals(t *testing.T, path string, expected []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != string(expected) {
		t.Fatalf("file %s changed: err=%v raw=%x", path, err, raw)
	}
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

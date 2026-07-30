package geodata

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

const (
	UpdateStateReady = "ready"
	UpdateStateError = "error"
	maxChecksumBytes = 4096
)

var ErrUpdateInProgress = errors.New("NP/2 geodata update is already in progress")

type Source struct {
	Name        string
	URL         string
	ChecksumURL string
}

type UpdateStatus struct {
	State         string    `json:"state"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	GeoIPSHA256   string    `json:"geoip_sha256,omitempty"`
	GeoSiteSHA256 string    `json:"geosite_sha256,omitempty"`
	GeoIPBytes    int64     `json:"geoip_bytes,omitempty"`
	GeoSiteBytes  int64     `json:"geosite_bytes,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type Updater struct {
	Client    *http.Client
	Sources   []Source
	allowHTTP bool
}

func DefaultUpdater() *Updater {
	client := &http.Client{
		Timeout: 4 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 || request.URL.Scheme != "https" || !trustedDownloadHost(request.URL.Hostname()) {
				return errors.New("untrusted geodata redirect")
			}
			return nil
		},
	}
	return &Updater{Client: client, Sources: []Source{
		{Name: "geoip.dat", URL: "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat", ChecksumURL: "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat.sha256sum"},
		{Name: "geosite.dat", URL: "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat", ChecksumURL: "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat.sha256sum"},
	}}
}

func (updater *Updater) Update(ctx context.Context, directory string) (UpdateStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := updater.validate(directory); err != nil {
		return UpdateStatus{State: UpdateStateError, Error: err.Error()}, err
	}
	lock := filepath.Join(directory, ".update.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return UpdateStatus{State: UpdateStateError, Error: ErrUpdateInProgress.Error()}, ErrUpdateInProgress
		}
		return UpdateStatus{State: UpdateStateError, Error: "cannot lock geodata directory"}, err
	}
	defer os.Remove(lock)

	staging, err := os.MkdirTemp(directory, ".update-")
	if err != nil {
		return UpdateStatus{State: UpdateStateError, Error: "cannot create update staging directory"}, err
	}
	defer os.RemoveAll(staging)
	for _, source := range updater.Sources {
		if err := updater.downloadVerified(ctx, source, filepath.Join(staging, source.Name)); err != nil {
			return UpdateStatus{State: UpdateStateError, Error: err.Error()}, err
		}
	}
	if _, err := Load(staging); err != nil {
		wrapped := fmt.Errorf("validate downloaded geodata: %w", err)
		return UpdateStatus{State: UpdateStateError, Error: wrapped.Error()}, wrapped
	}
	if err := activatePair(directory, staging); err != nil {
		wrapped := fmt.Errorf("activate downloaded geodata: %w", err)
		return UpdateStatus{State: UpdateStateError, Error: wrapped.Error()}, wrapped
	}
	status, err := Status(directory)
	if err != nil {
		return UpdateStatus{State: UpdateStateError, Error: err.Error()}, err
	}
	return status, nil
}

func Status(directory string) (UpdateStatus, error) {
	if !validDirectory(directory) {
		return UpdateStatus{State: UpdateStateError}, ErrInvalidDatabase
	}
	geoIPHash, geoIPSize, geoIPTime, err := fileStatus(filepath.Join(directory, "geoip.dat"))
	if err != nil {
		return UpdateStatus{State: UpdateStateError, Error: err.Error()}, err
	}
	geoSiteHash, geoSiteSize, geoSiteTime, err := fileStatus(filepath.Join(directory, "geosite.dat"))
	if err != nil {
		return UpdateStatus{State: UpdateStateError, Error: err.Error()}, err
	}
	updated := geoIPTime
	if geoSiteTime.Before(updated) {
		updated = geoSiteTime
	}
	return UpdateStatus{
		State: UpdateStateReady, UpdatedAt: updated.UTC(), GeoIPSHA256: geoIPHash,
		GeoSiteSHA256: geoSiteHash, GeoIPBytes: geoIPSize, GeoSiteBytes: geoSiteSize,
	}, nil
}

func (updater *Updater) validate(directory string) error {
	if updater == nil || updater.Client == nil || len(updater.Sources) != 2 || !validDirectory(directory) {
		return ErrInvalidDatabase
	}
	wanted := map[string]bool{"geoip.dat": false, "geosite.dat": false}
	for _, source := range updater.Sources {
		if _, ok := wanted[source.Name]; !ok || wanted[source.Name] {
			return ErrInvalidDatabase
		}
		if !validSourceURL(source.URL, updater.allowHTTP) || !validSourceURL(source.ChecksumURL, updater.allowHTTP) {
			return ErrInvalidDatabase
		}
		wanted[source.Name] = true
	}
	return nil
}

func (updater *Updater) downloadVerified(ctx context.Context, source Source, destination string) error {
	expectedRaw, err := updater.download(ctx, source.ChecksumURL, maxChecksumBytes)
	if err != nil {
		return fmt.Errorf("download %s checksum: %w", source.Name, err)
	}
	expected, err := parseChecksum(expectedRaw)
	if err != nil {
		return fmt.Errorf("verify %s checksum response: %w", source.Name, err)
	}
	raw, err := updater.download(ctx, source.URL, maxDatabaseBytes)
	if err != nil {
		return fmt.Errorf("download %s: %w", source.Name, err)
	}
	actual := sha256.Sum256(raw)
	if !strings.EqualFold(expected, hex.EncodeToString(actual[:])) {
		return fmt.Errorf("verify %s: checksum mismatch", source.Name)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	return errors.Join(err, closeErr)
}

func (updater *Updater) download(ctx context.Context, sourceURL string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := updater.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximum {
		return nil, errors.New("unexpected download response")
	}
	limited := io.LimitReader(response.Body, maximum+1)
	raw, err := io.ReadAll(limited)
	if err != nil || int64(len(raw)) == 0 || int64(len(raw)) > maximum {
		return nil, errors.New("download exceeds size limit")
	}
	return raw, nil
}

func activatePair(directory, staging string) error {
	names := []string{"geoip.dat", "geosite.dat"}
	backupDirectory, err := os.MkdirTemp(directory, ".rollback-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(backupDirectory)
	for _, name := range names {
		current := filepath.Join(directory, name)
		backup := filepath.Join(backupDirectory, name)
		if err := copyRegularFile(current, backup); err != nil {
			return err
		}
	}
	activated := make([]string, 0, len(names))
	for _, name := range names {
		destination := filepath.Join(directory, name)
		if err := replaceFile(filepath.Join(staging, name), destination); err != nil {
			var rollbackErrors []error
			for _, activatedName := range activated {
				rollbackErrors = append(rollbackErrors, replaceFile(filepath.Join(backupDirectory, activatedName), filepath.Join(directory, activatedName)))
			}
			return errors.Join(append([]error{err}, rollbackErrors...)...)
		}
		activated = append(activated, name)
	}
	return nil
}

func replaceFile(source, destination string) error {
	temporary := destination + ".next"
	_ = os.Remove(temporary)
	if err := copyRegularFile(source, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Chmod(destination, 0o640)
}

func copyRegularFile(source, destination string) error {
	linkInfo, err := os.Lstat(source)
	if err != nil || !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 || linkInfo.Size() <= 0 || linkInfo.Size() > maxDatabaseBytes {
		return ErrInvalidDatabase
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != linkInfo.Size() || info.Size() <= 0 || info.Size() > maxDatabaseBytes {
		return ErrInvalidDatabase
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, maxDatabaseBytes+1))
	if copyErr == nil {
		copyErr = output.Sync()
	}
	return errors.Join(copyErr, output.Close())
}

func fileStatus(path string) (string, int64, time.Time, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxDatabaseBytes {
		return "", 0, time.Time{}, ErrInvalidDatabase
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxDatabaseBytes+1)); err != nil {
		return "", 0, time.Time{}, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), info.ModTime(), nil
}

func parseChecksum(raw []byte) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	if !scanner.Scan() {
		return "", ErrInvalidDatabase
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return "", ErrInvalidDatabase
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil || len(decoded) != sha256.Size {
		return "", ErrInvalidDatabase
	}
	return strings.ToLower(fields[0]), nil
}

func validDirectory(directory string) bool {
	return directory != "" && filepath.IsAbs(directory) && filepath.Clean(directory) == directory
}

func validSourceURL(raw string, allowHTTP bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if allowHTTP {
		return parsed.Scheme == "http" || parsed.Scheme == "https"
	}
	return parsed.Scheme == "https" && trustedDownloadHost(parsed.Hostname())
}

func trustedDownloadHost(host string) bool {
	switch strings.ToLower(host) {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	default:
		return false
	}
}

// Reloadable keeps routing available while a verified database pair is loaded
// and swaps the matcher only after the complete pair has passed validation.
type Reloadable struct {
	engine atomic.Pointer[Engine]
}

func NewReloadable(engine *Engine) *Reloadable {
	reloadable := &Reloadable{}
	if engine != nil {
		reloadable.engine.Store(engine)
	}
	return reloadable
}

func (reloadable *Reloadable) Reload(directory string) error {
	engine, err := Load(directory)
	if err != nil {
		return err
	}
	reloadable.engine.Store(engine)
	return nil
}

func (reloadable *Reloadable) Match(ctx context.Context, match cluster.RouteMatch, target cluster.Target) bool {
	if reloadable == nil {
		return false
	}
	engine := reloadable.engine.Load()
	return engine != nil && engine.Match(ctx, match, target)
}

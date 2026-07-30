package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"neproto.local/chameleon/internal/admin"
)

const (
	maxBundleBytes       = int64(512 << 20)
	maxInstallationBytes = int64(1 << 20)
)

var ErrNoUpdate = errors.New("no NeProto update is available")

type releaseSource struct {
	apiURL        string
	repositoryURL string
}

type installerRunner func(context.Context, string, []string) error

type Engine struct {
	currentVersion string
	root           string
	store          StatusStore
	source         releaseSource
	client         *http.Client
	runInstaller   installerRunner
}

func NewEngine(currentVersion, root, stateDirectory string) *Engine {
	return &Engine{
		currentVersion: currentVersion,
		root:           filepath.Clean(root),
		store:          StatusStore{Directory: stateDirectory},
		source:         releaseSource{apiURL: latestReleaseAPIURL, repositoryURL: repositoryURL},
		client:         newReleaseHTTPClient(),
		runInstaller:   runInstallerProcess,
	}
}

func newReleaseHTTPClient() *http.Client {
	trustedHosts := map[string]struct{}{
		"api.github.com":                       {},
		"github.com":                           {},
		"release-assets.githubusercontent.com": {},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.IdleConnTimeout = 30 * time.Second
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many release redirects")
			}
			if request.URL.Scheme != "https" || request.URL.User != nil {
				return errors.New("release redirect is not trusted")
			}
			if _, ok := trustedHosts[strings.ToLower(request.URL.Hostname())]; !ok {
				return errors.New("release redirect host is not trusted")
			}
			return nil
		},
	}
}

func (engine *Engine) Check(ctx context.Context) (Status, error) {
	if _, err := ParseVersion(engine.currentVersion); err != nil {
		return Status{}, err
	}
	checking, err := engine.persist(Status{
		State: "checking", CurrentVersion: engine.currentVersion,
		Progress: mustProgress("checking"), Message: "Checking GitHub for updates",
	})
	if err != nil {
		return Status{}, err
	}
	release, err := engine.latest(ctx)
	if err != nil {
		return engine.fail(checking, "check_failed", "Update check failed", err)
	}
	message := "NeProto is up to date"
	if release.Available {
		message = "Update available"
	}
	return engine.persist(Status{
		State: "idle", CurrentVersion: engine.currentVersion, AvailableVersion: release.Tag,
		UpdateAvailable: release.Available, Progress: mustProgress("idle"), Message: message,
	})
}

func (engine *Engine) Apply(ctx context.Context) (Status, error) {
	status, err := engine.persist(Status{
		State: "checking", CurrentVersion: engine.currentVersion,
		Progress: mustProgress("checking"), Message: "Checking release metadata",
	})
	if err != nil {
		return Status{}, err
	}
	release, err := engine.latest(ctx)
	if err != nil {
		return engine.fail(status, "check_failed", "Update check failed", err)
	}
	status.AvailableVersion = release.Tag
	status.UpdateAvailable = release.Available
	if !release.Available {
		status.State = "idle"
		status.Progress = mustProgress("idle")
		status.Message = "NeProto is up to date"
		status, persistErr := engine.persist(status)
		if persistErr != nil {
			return status, persistErr
		}
		return status, ErrNoUpdate
	}

	workDirectory, err := os.MkdirTemp(engine.store.Directory, ".work-*")
	if err != nil {
		return engine.fail(status, "storage_failed", "Cannot prepare update storage", err)
	}
	defer os.RemoveAll(workDirectory)

	status.State = "downloading"
	status.Progress = mustProgress("downloading")
	status.Message = "Downloading verified release bundle"
	status, err = engine.persist(status)
	if err != nil {
		return status, err
	}
	checksumResponse, err := engine.get(ctx, release.ChecksumURL)
	if err != nil {
		return engine.fail(status, "download_failed", "Release download failed", err)
	}
	expectedDigest, checksumErr := ParseChecksum(checksumResponse, ArchiveName(release.Tag))
	checksumResponse.Close()
	if checksumErr != nil {
		return engine.fail(status, "verification_failed", "Release verification failed", checksumErr)
	}
	archivePath := filepath.Join(workDirectory, ArchiveName(release.Tag))
	actualDigest, err := engine.downloadArchive(ctx, release.ArchiveURL, archivePath)
	if err != nil {
		return engine.fail(status, "download_failed", "Release download failed", err)
	}

	status.State = "verifying"
	status.Progress = mustProgress("verifying")
	status.Message = "Verifying SHA-256 checksum"
	status, err = engine.persist(status)
	if err != nil {
		return status, err
	}
	if actualDigest != expectedDigest {
		return engine.fail(status, "verification_failed", "Release verification failed", errors.New("release checksum mismatch"))
	}

	status.State = "extracting"
	status.Progress = mustProgress("extracting")
	status.Message = "Extracting release safely"
	status, err = engine.persist(status)
	if err != nil {
		return status, err
	}
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return engine.fail(status, "extraction_failed", "Release extraction failed", err)
	}
	bundleRoot, extractErr := ExtractBundle(archiveFile, workDirectory, release.Tag)
	archiveFile.Close()
	if extractErr != nil {
		return engine.fail(status, "extraction_failed", "Release extraction failed", extractErr)
	}

	status.State = "backing_up"
	status.Progress = mustProgress("backing_up")
	status.Message = "Preparing transactional backup"
	status, err = engine.persist(status)
	if err != nil {
		return status, err
	}
	installation, acmeEmail, err := engine.installedTopology()
	if err != nil {
		return engine.fail(status, "configuration_failed", "Installed configuration is invalid", err)
	}
	arguments, err := InstallerArguments(installation, acmeEmail)
	if err != nil {
		return engine.fail(status, "configuration_failed", "Installed configuration is invalid", err)
	}
	adminSecret, err := captureAdminSecret(engine.root)
	if err != nil {
		return engine.fail(status, "configuration_failed", "Administrator credential is invalid", err)
	}

	status.State = "installing"
	status.Progress = mustProgress("installing")
	status.Message = "Installing server and web application"
	status, err = engine.persist(status)
	if err != nil {
		return status, err
	}
	installerErr := engine.runInstaller(ctx, filepath.Join(bundleRoot, "install.sh"), arguments)
	_, recoveryErr := adminSecret.restoreIfChanged()
	if recoveryErr != nil {
		return engine.fail(status, "credential_recovery_failed", "Administrator credential recovery failed", errors.Join(installerErr, recoveryErr))
	}
	if installerErr != nil {
		return engine.fail(status, "installation_failed", "Update installation failed", installerErr)
	}

	status.State = "restarting"
	status.Progress = mustProgress("restarting")
	status.Message = "Restarting NeProto services"
	status, err = engine.persist(status)
	if err != nil {
		return status, err
	}
	return engine.persist(Status{
		State: "succeeded", CurrentVersion: release.Tag, AvailableVersion: release.Tag,
		Progress: mustProgress("succeeded"), Message: "NeProto update completed",
	})
}

func (engine *Engine) latest(ctx context.Context) (Release, error) {
	response, err := engine.get(ctx, engine.source.apiURL)
	if err != nil {
		return Release{}, err
	}
	defer response.Close()
	release, err := ParseLatestRelease(response, engine.currentVersion)
	if err != nil {
		return Release{}, err
	}
	return releaseForSource(release.Tag, release.Available, engine.source.repositoryURL), nil
}

func (engine *Engine) get(ctx context.Context, url string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "NeProto-Updater/1")
	response, err := engine.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("release service returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func (engine *Engine) downloadArchive(ctx context.Context, url, destination string) (string, error) {
	response, err := engine.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer response.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response, maxBundleBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written <= 0 || written > maxBundleBytes {
		return "", errors.New("release bundle exceeds size limit")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (engine *Engine) installedTopology() (admin.Installation, string, error) {
	installationPath := filepath.Join(engine.root, "etc", "neproto", "installation.json")
	file, err := os.Open(installationPath)
	if err != nil {
		return admin.Installation{}, "", err
	}
	limited := &io.LimitedReader{R: file, N: maxInstallationBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var installation admin.Installation
	decodeErr := decoder.Decode(&installation)
	if decodeErr == nil {
		if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
			decodeErr = errors.New("installation state contains trailing data")
		}
	}
	file.Close()
	if decodeErr != nil || limited.N <= 0 {
		return admin.Installation{}, "", errors.New("invalid installation state")
	}
	acmePath := filepath.Join(engine.root, "etc", "neproto", "acme-email")
	encodedEmail, emailErr := os.ReadFile(acmePath)
	if emailErr != nil && !errors.Is(emailErr, os.ErrNotExist) {
		return admin.Installation{}, "", emailErr
	}
	if len(encodedEmail) > 512 {
		return admin.Installation{}, "", errors.New("ACME email exceeds size limit")
	}
	return installation, strings.TrimSpace(string(encodedEmail)), nil
}

func (engine *Engine) persist(status Status) (Status, error) {
	normalized, err := engine.store.normalize(status)
	if err != nil {
		return Status{}, err
	}
	if err := engine.store.writeNormalized(normalized); err != nil {
		return Status{}, err
	}
	return normalized, nil
}

func (engine *Engine) fail(status Status, code, message string, cause error) (Status, error) {
	status.State = "failed"
	status.Progress = mustProgress("failed")
	status.Message = message
	status.ErrorCode = code
	failed, writeErr := engine.persist(status)
	if writeErr != nil {
		return status, errors.Join(cause, writeErr)
	}
	return failed, cause
}

func runInstallerProcess(ctx context.Context, installer string, arguments []string) error {
	command := exec.CommandContext(ctx, installer, arguments...)
	command.Env = append(os.Environ(), "NEPROTO_SELF_UPDATE=1")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func mustProgress(stage string) int {
	progress, ok := ProgressForStage(stage)
	if !ok {
		panic("unknown update stage")
	}
	return progress
}

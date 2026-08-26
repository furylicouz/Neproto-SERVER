package windowscandidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	ManifestName           = "candidate-manifest.json"
	PlatformWindowsX64     = "windows-x64"
	CarrierPolicyHTTP3Only = "http3-only"

	manifestSchema    = 1
	maxManifestBytes  = 2 << 20
	maxCandidateFiles = 4096
	maxCandidateFile  = int64(512 << 20)
	maxCandidateBytes = int64(1 << 30)
	maxCandidatePath  = 240
)

var (
	versionPattern = regexp.MustCompile(`^np2-[0-9]+\.[0-9]+\.[0-9]+$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Manifest struct {
	Schema        int    `json:"schema"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Platform      string `json:"platform"`
	CarrierPolicy string `json:"carrier_policy"`
	Files         []File `json:"files"`
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func Create(root, version, commit string) (Manifest, error) {
	manifest := Manifest{
		Schema:        manifestSchema,
		Version:       version,
		Commit:        commit,
		Platform:      PlatformWindowsX64,
		CarrierPolicy: CarrierPolicyHTTP3Only,
	}
	if err := validateIdentity(manifest); err != nil {
		return Manifest{}, err
	}
	files, err := collectFiles(root)
	if err != nil {
		return Manifest{}, err
	}
	if len(files) == 0 {
		return Manifest{}, errors.New("candidate contains no files")
	}
	manifest.Files = files
	return manifest, nil
}

func Write(root string, manifest Manifest) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode candidate manifest: %w", err)
	}
	raw = append(raw, '\n')
	if len(raw) > maxManifestBytes {
		return errors.New("candidate manifest is too large")
	}
	temporary, err := os.CreateTemp(root, ".candidate-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create candidate manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(raw)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write candidate manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(root, ManifestName)); err != nil {
		return fmt.Errorf("activate candidate manifest: %w", err)
	}
	return nil
}

func LoadAndVerify(root string) (Manifest, error) {
	raw, err := readBounded(filepath.Join(root, ManifestName), maxManifestBytes)
	if err != nil {
		return Manifest{}, fmt.Errorf("read candidate manifest: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode candidate manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	actual, err := collectFiles(root)
	if err != nil {
		return Manifest{}, err
	}
	expectedByPath := make(map[string]File, len(manifest.Files))
	for _, file := range manifest.Files {
		expectedByPath[file.Path] = file
	}
	actualByPath := make(map[string]File, len(actual))
	for _, file := range actual {
		actualByPath[file.Path] = file
		if _, ok := expectedByPath[file.Path]; !ok {
			return Manifest{}, fmt.Errorf("unexpected candidate file %q", file.Path)
		}
	}
	for _, expected := range manifest.Files {
		observed, ok := actualByPath[expected.Path]
		if !ok {
			return Manifest{}, fmt.Errorf("candidate file %q is missing", expected.Path)
		}
		if observed.Size != expected.Size {
			return Manifest{}, fmt.Errorf("candidate file %q size mismatch", expected.Path)
		}
		if observed.SHA256 != expected.SHA256 {
			return Manifest{}, fmt.Errorf("candidate file %q digest mismatch", expected.Path)
		}
	}
	return manifest, nil
}

func validateIdentity(manifest Manifest) error {
	if manifest.Schema != manifestSchema {
		return errors.New("unsupported candidate manifest schema")
	}
	if !versionPattern.MatchString(manifest.Version) {
		return errors.New("invalid candidate version")
	}
	if !commitPattern.MatchString(manifest.Commit) {
		return errors.New("invalid candidate commit")
	}
	if manifest.Platform != PlatformWindowsX64 {
		return errors.New("invalid candidate platform")
	}
	if manifest.CarrierPolicy != CarrierPolicyHTTP3Only {
		return errors.New("invalid candidate carrier policy")
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if err := validateIdentity(manifest); err != nil {
		return err
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > maxCandidateFiles {
		return errors.New("invalid candidate file count")
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	var total int64
	previous := ""
	for _, file := range manifest.Files {
		if err := validatePath(file.Path); err != nil {
			return err
		}
		if file.Path == ManifestName {
			return errors.New("candidate manifest cannot list itself")
		}
		if previous != "" && file.Path <= previous {
			return errors.New("candidate file list is not strictly ordered")
		}
		previous = file.Path
		folded := strings.ToLower(file.Path)
		if _, ok := seen[folded]; ok {
			return errors.New("duplicate candidate path")
		}
		seen[folded] = struct{}{}
		if file.Size <= 0 || file.Size > maxCandidateFile || total > maxCandidateBytes-file.Size {
			return errors.New("invalid candidate file size")
		}
		total += file.Size
		if !digestPattern.MatchString(file.SHA256) {
			return errors.New("invalid candidate file digest")
		}
	}
	return nil
}

func collectFiles(root string) ([]File, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect candidate root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("candidate root is not a regular directory")
	}
	files := make([]File, 0, 64)
	seen := make(map[string]struct{}, 64)
	var total int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("candidate contains link %q", entry.Name())
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("candidate contains non-regular file %q", entry.Name())
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ManifestName {
			return nil
		}
		if err := validatePath(relative); err != nil {
			return err
		}
		folded := strings.ToLower(relative)
		if _, ok := seen[folded]; ok {
			return errors.New("duplicate candidate path")
		}
		seen[folded] = struct{}{}
		if len(files) >= maxCandidateFiles || info.Size() <= 0 || info.Size() > maxCandidateFile || total > maxCandidateBytes-info.Size() {
			return errors.New("candidate payload exceeds bounds")
		}
		digest, err := hashFile(path, info.Size())
		if err != nil {
			return err
		}
		total += info.Size()
		files = append(files, File{Path: relative, Size: info.Size(), SHA256: digest})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect candidate files: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func validatePath(path string) error {
	if path == "" || len(path) > maxCandidatePath || strings.ContainsAny(path, "\\:") || !fs.ValidPath(path) || filepath.IsAbs(path) {
		return fmt.Errorf("invalid candidate path %q", path)
	}
	return nil
}

func hashFile(path string, expectedSize int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxCandidateFile+1))
	if err != nil {
		return "", err
	}
	if written != expectedSize {
		return "", errors.New("candidate file changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maximum {
		return nil, errors.New("file exceeds size limit")
	}
	return raw, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("candidate manifest contains trailing JSON")
		}
		return fmt.Errorf("decode candidate manifest trailer: %w", err)
	}
	return nil
}

package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxArchiveEntries = 100_000
	maxExpandedBytes  = int64(2 << 30)
)

func ExtractBundle(reader io.Reader, destination, tag string) (string, error) {
	if _, err := ParseVersion(tag); err != nil {
		return "", err
	}
	expectedRoot := strings.TrimSuffix(ArchiveName(tag), ".tar.gz")
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return "", fmt.Errorf("open release archive: %w", err)
	}
	defer gzipReader.Close()

	archive := tar.NewReader(gzipReader)
	entries := 0
	var expanded int64
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", fmt.Errorf("read release archive: %w", nextErr)
		}
		entries++
		if entries > maxArchiveEntries || header.Size < 0 || header.Size > maxExpandedBytes-expanded {
			return "", errors.New("release archive exceeds extraction limits")
		}
		expanded += header.Size

		cleanName := path.Clean(header.Name)
		if cleanName == "." || path.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "\\") {
			return "", fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if cleanName != expectedRoot && !strings.HasPrefix(cleanName, expectedRoot+"/") {
			return "", fmt.Errorf("unexpected archive root %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(cleanName))
		if !pathWithin(destination, target) {
			return "", fmt.Errorf("archive path escapes destination %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			mode := os.FileMode(header.Mode) & 0o755
			if mode&0o111 == 0 {
				mode = 0o644
			}
			file, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if openErr != nil {
				return "", openErr
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		default:
			return "", fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
	root := filepath.Join(destination, expectedRoot)
	installer := filepath.Join(root, "install.sh")
	info, err := os.Lstat(installer)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("release archive has no executable installer")
	}
	if err := os.Chmod(installer, 0o755); err != nil {
		return "", fmt.Errorf("secure installer mode: %w", err)
	}
	return root, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

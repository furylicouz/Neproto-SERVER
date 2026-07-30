package selfupdate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	repositoryURL       = "https://github.com/furylicouz/Neproto-SERVER"
	latestReleaseAPIURL = "https://api.github.com/repos/furylicouz/Neproto-SERVER/releases/latest"
	maxReleaseBytes     = 64 << 10
	maxChecksumBytes    = 4 << 10
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Release struct {
	Tag         string
	Available   bool
	ArchiveURL  string
	ChecksumURL string
}

func ParseLatestRelease(reader io.Reader, currentTag string) (Release, error) {
	current, err := ParseVersion(currentTag)
	if err != nil {
		return Release{}, err
	}
	limited := &io.LimitedReader{R: reader, N: maxReleaseBytes + 1}
	var response struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(&response); err != nil {
		return Release{}, fmt.Errorf("decode release metadata: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Release{}, errors.New("release metadata contains trailing data")
	}
	if limited.N <= 0 {
		return Release{}, errors.New("release metadata exceeds size limit")
	}
	if response.Draft || response.Prerelease {
		return Release{}, errors.New("latest release is not stable")
	}
	available, err := ParseVersion(response.Tag)
	if err != nil {
		return Release{}, err
	}
	return releaseForSource(response.Tag, available.Compare(current) > 0, repositoryURL), nil
}

func releaseForSource(tag string, available bool, sourceRepositoryURL string) Release {
	archiveName := ArchiveName(tag)
	archiveURL := strings.TrimSuffix(sourceRepositoryURL, "/") + "/releases/download/" + tag + "/" + archiveName
	return Release{
		Tag:         tag,
		Available:   available,
		ArchiveURL:  archiveURL,
		ChecksumURL: archiveURL + ".sha256",
	}
}

func ArchiveName(tag string) string {
	return "neproto-server-bundle-" + tag + ".tar.gz"
}

func ParseChecksum(reader io.Reader, expectedArchive string) (string, error) {
	limited := io.LimitReader(reader, maxChecksumBytes+1)
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 1024), maxChecksumBytes+1)
	var nonEmpty []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read checksum: %w", err)
	}
	if len(nonEmpty) != 1 {
		return "", errors.New("checksum must contain exactly one entry")
	}
	fields := strings.Fields(nonEmpty[0])
	if len(fields) != 2 || !digestPattern.MatchString(fields[0]) || fields[1] != expectedArchive {
		return "", errors.New("invalid release checksum entry")
	}
	return fields[0], nil
}

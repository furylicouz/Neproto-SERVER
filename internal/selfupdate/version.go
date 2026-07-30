package selfupdate

import (
	"errors"
	"strconv"
	"strings"
)

var ErrInvalidVersion = errors.New("invalid NeProto release version")

type Version struct {
	Major int
	Minor int
	Patch int
}

func ParseVersion(value string) (Version, error) {
	if !strings.HasPrefix(value, "np2-") || len(value) > 32 {
		return Version{}, ErrInvalidVersion
	}
	parts := strings.Split(strings.TrimPrefix(value, "np2-"), ".")
	if len(parts) != 3 {
		return Version{}, ErrInvalidVersion
	}
	values := make([]int, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, ErrInvalidVersion
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return Version{}, ErrInvalidVersion
			}
		}
		parsed, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return Version{}, ErrInvalidVersion
		}
		values[index] = int(parsed)
	}
	return Version{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

func (version Version) Compare(other Version) int {
	left := [...]int{version.Major, version.Minor, version.Patch}
	right := [...]int{other.Major, other.Minor, other.Patch}
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

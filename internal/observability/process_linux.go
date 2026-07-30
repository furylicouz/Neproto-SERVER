//go:build linux

package observability

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func residentMemoryBytes() uint64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(syscall.Getpagesize())
}

func openFileDescriptors() uint64 {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return uint64(len(entries))
}

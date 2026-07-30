//go:build !linux

package observability

import "runtime"

func residentMemoryBytes() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}

func openFileDescriptors() uint64 { return 0 }

//go:build !windows

package admin

import (
	"errors"
	"os"
	"syscall"
)

func preserveFileOwnership(source os.FileInfo, destination string) error {
	metadata, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot read file ownership")
	}
	return os.Chown(destination, int(metadata.Uid), int(metadata.Gid))
}

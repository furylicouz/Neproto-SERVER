//go:build unix

package selfupdate

import (
	"errors"
	"os"
	"syscall"
)

type fileOwner struct {
	uid int
	gid int
}

func ownerFromFileInfo(info os.FileInfo) fileOwner {
	statistics, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileOwner{uid: -1, gid: -1}
	}
	return fileOwner{uid: int(statistics.Uid), gid: int(statistics.Gid)}
}

func applyFileOwner(path string, owner fileOwner) error {
	if owner.uid < 0 || owner.gid < 0 {
		return errors.New("administrator secret ownership is unavailable")
	}
	return os.Chown(path, owner.uid, owner.gid)
}

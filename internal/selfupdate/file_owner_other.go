//go:build !unix

package selfupdate

import "os"

type fileOwner struct{}

func ownerFromFileInfo(os.FileInfo) fileOwner {
	return fileOwner{}
}

func applyFileOwner(string, fileOwner) error {
	return nil
}

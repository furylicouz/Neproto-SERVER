//go:build !windows

package windowsclient

import "os"

func replaceFile(source, destination string) error { return os.Rename(source, destination) }

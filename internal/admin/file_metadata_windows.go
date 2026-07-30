//go:build windows

package admin

import "os"

func preserveFileOwnership(os.FileInfo, string) error { return nil }

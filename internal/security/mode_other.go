//go:build !windows

package security

import (
	"io/fs"
	"os"
)

func ApplySafeFileMode(file *os.File, mode fs.FileMode) error {
	return file.Chmod(SafeFileMode(mode))
}

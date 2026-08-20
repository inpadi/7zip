//go:build windows

package security

import (
	"io/fs"
	"os"
)

func ApplySafeFileMode(_ *os.File, _ fs.FileMode) error {
	// Windows Chmod only controls the read-only attribute. Extraction files are
	// created writable, and their access control is inherited from the parent.
	return nil
}

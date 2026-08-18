package archivefmt

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/inpadi/7zip/internal/archive7z"
)

func extractEntry(root, name string, mode fs.FileMode, modified time.Time, reader io.Reader, options ExtractOptions, seen map[string]string) (int64, bool, error) {
	target, relative, err := archive7z.SafeDestination(root, name, options.Flatten)
	if err != nil {
		return 0, false, err
	}
	if mode.IsDir() && options.Flatten {
		return 0, false, nil
	}
	key := nameKey(relative)
	if previous, exists := seen[key]; exists {
		return 0, false, fmt.Errorf("archive entries %q and %q map to the same output path", previous, name)
	}
	seen[key] = name
	if mode.IsDir() {
		if err := archive7z.SecureMkdirAll(root, relative, 0o755); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	if mode.Type() != 0 {
		return 0, false, fmt.Errorf("refusing unsupported special entry %q (%s)", name, mode.Type())
	}
	if err := archive7z.SecureMkdirAll(root, filepath.Dir(relative), 0o755); err != nil {
		return 0, false, err
	}
	skip, err := archive7z.PrepareTarget(target, options.Overwrite)
	if err != nil || skip {
		return 0, false, err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".7zip-extract-*.tmp")
	if err != nil {
		return 0, false, err
	}
	tempName := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempName)
		}
	}()
	n, err := io.Copy(temp, reader)
	if err != nil {
		return n, false, err
	}
	if err := temp.Close(); err != nil {
		return n, false, err
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o600
	}
	if err := os.Chmod(tempName, perm); err != nil {
		return n, false, err
	}
	if !modified.IsZero() {
		if err := os.Chtimes(tempName, modified, modified); err != nil {
			return n, false, err
		}
	}
	if options.Overwrite == OverwriteAll {
		if err := archive7z.RemoveExistingRegular(target); err != nil {
			return n, false, err
		}
	}
	if err := os.Rename(tempName, target); err != nil {
		return n, false, err
	}
	removeTemp = false
	return n, true, nil
}

func extractionRoot(output string) (string, error) {
	if output == "" {
		output = "."
	}
	if err := archive7z.SecureMkdirAll(output, "", 0o755); err != nil {
		return "", err
	}
	return output, nil
}

func publish(tempName, archive string, existed bool) error {
	if !existed {
		return os.Rename(tempName, archive)
	}
	if err := os.Rename(tempName, archive); err == nil {
		return nil
	}
	backupFile, err := os.CreateTemp(filepath.Dir(archive), ".archive-backup-*.tmp")
	if err != nil {
		return err
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(archive, backup); err != nil {
		return err
	}
	if err := os.Rename(tempName, archive); err != nil {
		_ = os.Rename(backup, archive)
		return err
	}
	return os.Remove(backup)
}

func archiveExists(name string) (bool, error) {
	_, err := os.Stat(name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

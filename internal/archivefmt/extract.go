package archivefmt

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/inpadi/7zip/internal/security"
)

func extractEntry(root *security.Root, name string, mode fs.FileMode, declared uint64, reader io.Reader, options ExtractOptions, seen map[string]string, budget *security.Budget) (int64, bool, error) {
	_, relative, err := archive7z.SafeDestination(".", name, options.Flatten)
	if err != nil {
		return 0, false, err
	}
	if err := budget.AddEntry(name, declared); err != nil {
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
		directory, err := root.MkdirRoot(relative, 0o755)
		if err != nil {
			return 0, false, err
		}
		if err := directory.Close(); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	if mode.Type() != 0 {
		return 0, false, fmt.Errorf("refusing unsupported special entry %q (%s)", name, mode.Type())
	}
	parent, err := root.MkdirRoot(filepath.Dir(relative), 0o755)
	if err != nil {
		return 0, false, err
	}
	defer parent.Close()
	target := filepath.Base(relative)
	existing, statErr := parent.Lstat(target)
	if statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			return 0, false, fmt.Errorf("refusing to replace non-regular path %q", relative)
		}
		switch options.Overwrite {
		case OverwriteSkip:
			return 0, false, nil
		case OverwriteAll:
		default:
			return 0, false, fmt.Errorf("output file %q already exists; use -y, -aoa, or -aos", relative)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return 0, false, statErr
	}
	tempName, temp, err := parent.CreateTemp()
	if err != nil {
		return 0, false, err
	}
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = parent.Remove(tempName)
		}
	}()
	n, err := budget.Copy(temp, reader, name)
	if err != nil {
		return n, false, err
	}
	if err := temp.Chmod(security.SafeFileMode(mode)); err != nil {
		return n, false, err
	}
	if err := temp.Sync(); err != nil {
		return n, false, err
	}
	if err := temp.Close(); err != nil {
		return n, false, err
	}
	if options.Overwrite == OverwriteAll {
		if statErr == nil {
			if err := parent.Remove(target); err != nil {
				return n, false, err
			}
		}
		if err := parent.Rename(tempName, target); err != nil {
			return n, false, err
		}
	} else if err := parent.Link(tempName, target); err != nil {
		if options.Overwrite == OverwriteSkip && errors.Is(err, fs.ErrExist) {
			return 0, false, nil
		}
		if errors.Is(err, fs.ErrExist) {
			return n, false, fmt.Errorf("output file %q already exists; use -y, -aoa, or -aos", relative)
		}
		return n, false, fmt.Errorf("publish %q without replacement: %w", relative, err)
	} else if err := parent.Remove(tempName); err != nil {
		return n, false, err
	}
	removeTemp = false
	return n, true, nil
}

func extractionRoot(output string) (*security.Root, error) {
	if output == "" {
		output = "."
	}
	return security.OpenExtractionRoot(output)
}

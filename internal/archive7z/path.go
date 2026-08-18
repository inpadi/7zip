package archive7z

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func cleanArchiveName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	if volume := filepath.VolumeName(filepath.FromSlash(clean)); volume != "" {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	if err := validatePlatformPath(clean); err != nil {
		return "", fmt.Errorf("unsafe archive path %q: %w", name, err)
	}
	return clean, nil
}

func validatePlatformPath(name string) error {
	if filepath.Separator != '\\' {
		return nil
	}
	for _, component := range strings.Split(name, "/") {
		if strings.Contains(component, ":") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
			return errors.New("invalid Windows path component")
		}
		base := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return errors.New("reserved Windows path component")
		}
	}
	return nil
}

func destination(root, name string, flatten bool) (string, string, error) {
	clean, err := cleanArchiveName(name)
	if err != nil {
		return "", "", err
	}
	if flatten {
		clean = path.Base(clean)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(rootAbs, filepath.FromSlash(clean))
	rel, err := filepath.Rel(rootAbs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, filepath.FromSlash(clean), nil
}

// SafeDestination maps an untrusted archive name below root.
func SafeDestination(root, name string, flatten bool) (string, string, error) {
	return destination(root, name, flatten)
}

// secureMkdirAll rejects existing symlinks in an extraction path. It cannot
// eliminate filesystem races portably, but prevents normal link traversal.
func secureMkdirAll(root, relativeDir string, perm fs.FileMode) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(rootAbs, perm); err != nil {
		return err
	}
	if err := rejectLink(rootAbs); err != nil {
		return err
	}

	current := rootAbs
	for _, part := range strings.FieldsFunc(relativeDir, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return errors.New("parent path is not allowed")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, perm); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe extraction directory %q", current)
		}
	}
	return nil
}

// SecureMkdirAll creates an extraction path without traversing existing links.
func SecureMkdirAll(root, relativeDir string, perm fs.FileMode) error {
	return secureMkdirAll(root, relativeDir, perm)
}

func rejectLink(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to traverse symbolic link %q", name)
	}
	return nil
}

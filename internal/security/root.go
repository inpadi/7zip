package security

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Root pins a directory handle so later path changes cannot redirect access.
type Root struct {
	root *os.Root
	name string
}

// ParentCache keeps one extraction directory pinned between consecutive files.
// When revalidation is enabled, Directory checks the handle identity before
// every reuse. Returned roots are borrowed and must not be closed by the caller.
type ParentCache struct {
	base       *Root
	revalidate bool
	path       string
	directory  *Root
	identity   fs.FileInfo
}

func NewParentCache(base *Root, revalidate bool) *ParentCache {
	return &ParentCache{base: base, revalidate: revalidate}
}

func (c *ParentCache) Directory(name string, perm fs.FileMode) (*Root, error) {
	clean := filepath.Clean(name)
	if c.directory != nil && clean == c.path {
		if !c.revalidate {
			return c.directory, nil
		}
		current, err := c.base.Lstat(clean)
		if err == nil && current.Mode()&os.ModeSymlink == 0 && current.IsDir() && os.SameFile(current, c.identity) {
			return c.directory, nil
		}
		if closeErr := c.closeDirectory(); closeErr != nil {
			return nil, closeErr
		}
	}
	if c.directory != nil {
		if err := c.closeDirectory(); err != nil {
			return nil, err
		}
	}
	directory, err := c.base.MkdirRoot(clean, perm)
	if err != nil {
		return nil, err
	}
	var identity fs.FileInfo
	if c.revalidate {
		identity, err = directory.Lstat(".")
		if err != nil {
			directory.Close()
			return nil, err
		}
	}
	c.path = clean
	c.directory = directory
	c.identity = identity
	return directory, nil
}

func (c *ParentCache) Close() error {
	return c.closeDirectory()
}

func (c *ParentCache) closeDirectory() error {
	if c.directory == nil {
		return nil
	}
	directory := c.directory
	c.path = ""
	c.directory = nil
	c.identity = nil
	return directory.Close()
}

func OpenExistingRoot(name string) (*Root, error) {
	return openRoot(name, false, true)
}

func OpenExtractionRoot(name string) (*Root, error) {
	return openRoot(name, true, false)
}

// OpenRegularFile opens a non-link file through a pinned parent directory.
func OpenRegularFile(name string) (*os.File, fs.FileInfo, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, nil, err
	}
	root, err := OpenExistingRoot(filepath.Dir(abs))
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	base := filepath.Base(abs)
	before, err := root.Lstat(base)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("refusing non-regular file %q", name)
	}
	file, err := root.Open(base)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("file %q changed while it was opened", name)
	}
	return file, after, nil
}

func openRoot(name string, create, allowFilesystemRoot bool) (*Root, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(abs)
	volumeRoot := volume + string(filepath.Separator)
	if volume == "" {
		volumeRoot = string(filepath.Separator)
	}
	relative, err := filepath.Rel(volumeRoot, abs)
	if err != nil {
		return nil, err
	}
	if relative == "." && !allowFilesystemRoot {
		return nil, errors.New("refusing to use a filesystem root as the extraction directory")
	}

	current, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, err
	}
	currentName := volumeRoot
	for _, component := range splitPath(relative) {
		info, statErr := current.Lstat(component)
		if errors.Is(statErr, fs.ErrNotExist) && create {
			if mkdirErr := current.Mkdir(component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
				current.Close()
				return nil, mkdirErr
			}
			info, statErr = current.Lstat(component)
		}
		if statErr != nil {
			current.Close()
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			current.Close()
			return nil, fmt.Errorf("refusing unsafe directory component %q", filepath.Join(currentName, component))
		}
		next, openErr := current.OpenRoot(component)
		if openErr != nil {
			current.Close()
			return nil, openErr
		}
		openedInfo, openErr := next.Stat(".")
		if openErr != nil || !os.SameFile(info, openedInfo) {
			next.Close()
			current.Close()
			if openErr != nil {
				return nil, openErr
			}
			return nil, fmt.Errorf("directory component %q changed while opening it", filepath.Join(currentName, component))
		}
		current.Close()
		current = next
		currentName = filepath.Join(currentName, component)
	}
	return &Root{root: current, name: abs}, nil
}

func splitPath(name string) []string {
	if name == "" || name == "." {
		return nil
	}
	return strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
}

func (r *Root) Close() error { return r.root.Close() }

func (r *Root) FS() fs.FS { return r.root.FS() }

func (r *Root) Lstat(name string) (fs.FileInfo, error) { return r.root.Lstat(filepath.ToSlash(name)) }

func (r *Root) Open(name string) (*os.File, error) { return r.root.Open(filepath.ToSlash(name)) }

func (r *Root) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	return r.root.OpenFile(filepath.ToSlash(name), flag, perm)
}

func (r *Root) OpenRoot(name string) (*Root, error) {
	clean := filepath.ToSlash(name)
	before, err := r.root.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("refusing unsafe directory %q", name)
	}
	child, err := r.root.OpenRoot(clean)
	if err != nil {
		return nil, err
	}
	after, err := child.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		child.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("directory %q changed while opening it", name)
	}
	return &Root{root: child, name: filepath.Join(r.name, name)}, nil
}

func (r *Root) MkdirRoot(name string, perm fs.FileMode) (*Root, error) {
	current, err := r.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, component := range splitPath(filepath.Clean(name)) {
		info, statErr := current.root.Lstat(component)
		if errors.Is(statErr, fs.ErrNotExist) {
			if mkdirErr := current.root.Mkdir(component, perm); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
				current.Close()
				return nil, mkdirErr
			}
			info, statErr = current.root.Lstat(component)
		}
		if statErr != nil {
			current.Close()
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			current.Close()
			return nil, fmt.Errorf("refusing unsafe extraction directory %q", name)
		}
		next, openErr := current.OpenRoot(component)
		current.Close()
		if openErr != nil {
			return nil, openErr
		}
		current = next
	}
	return current, nil
}

func (r *Root) CreateTemp() (string, *os.File, error) {
	for range 100 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := fmt.Sprintf(".i7z-%x.tmp", random[:])
		file, err := r.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("unable to allocate a unique extraction temporary file")
}

func (r *Root) Remove(name string) error { return r.root.Remove(filepath.ToSlash(name)) }

func (r *Root) Rename(oldName, newName string) error {
	return r.root.Rename(filepath.ToSlash(oldName), filepath.ToSlash(newName))
}

func (r *Root) Link(oldName, newName string) error {
	return r.root.Link(filepath.ToSlash(oldName), filepath.ToSlash(newName))
}

func SafeFileMode(mode fs.FileMode) fs.FileMode {
	return (mode.Perm() | 0o600) &^ 0o022
}

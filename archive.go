// Package sevenzip exposes archives as read-write Go filesystems.
//
// Archive updates are staged in a temporary directory. Close rebuilds and
// transactionally publishes a changed archive, while Discard rolls changes
// back. Callers should close files opened from an Archive when finished; Close
// also closes any handles that remain open.
package sevenzip

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/inpadi/7zip/internal/archivefmt"
	"github.com/inpadi/7zip/internal/security"
)

// Format identifies an archive format.
type Format string

const (
	Format7z       Format = "7z"
	FormatZip      Format = "zip"
	FormatTar      Format = "tar"
	FormatTarGzip  Format = "tar.gzip"
	FormatTarBzip2 Format = "tar.bzip2"
	FormatTarXZ    Format = "tar.xz"
	FormatTarZstd  Format = "tar.zstd"
	FormatGzip     Format = "gzip"
	FormatBzip2    Format = "bzip2"
	FormatXZ       Format = "xz"
	FormatZstd     Format = "zstd"
	FormatISO      Format = "iso"
	FormatWIM      Format = "wim"
	FormatVHD      Format = "vhd"
	FormatVHDX     Format = "vhdx"
)

var (
	// ErrClosed indicates that an operation was attempted after Close or Discard.
	ErrClosed = os.ErrClosed
	// ErrReadOnly indicates that the archive was opened read-only or uses a
	// format for which archive creation is not implemented.
	ErrReadOnly = errors.New("archive filesystem is read-only")
	// ErrArchiveChanged indicates that the archive was replaced or modified
	// after it was opened. The staged changes are not published in this case.
	ErrArchiveChanged = errors.New("archive changed while open")
)

// Options configures archive reading and the archive produced by Close.
// Zero values select the format from the filename, use the writer's default
// compression level, and enable solid compression for 7z archives.
type Options struct {
	// Format overrides filename and signature based format detection.
	Format Format
	// Password decrypts input and encrypts rebuilt 7z output.
	Password string
	// ReadOnly rejects all filesystem mutation methods.
	ReadOnly bool

	// CompressionLevel is 1 through 9. Zero selects the format default.
	CompressionLevel int
	// CompressionMethod accepts the same format-specific method names as the
	// CLI, including "copy", "lzma", "lzma2", "deflate", and "zstd".
	CompressionMethod string
	// NonSolid disables the default solid mode for 7z output.
	NonSolid bool
	// HeaderEncryption encrypts 7z metadata and requires Password.
	HeaderEncryption bool
}

// Archive is a staged archive filesystem. Archive paths use forward slashes
// and follow the io/fs path rules: the root is "." and paths must be relative.
type Archive struct {
	mu       sync.RWMutex
	name     string
	dir      string
	root     *os.Root
	options  Options
	format   Format
	initial  fs.FileInfo
	readOnly bool
	dirty    bool
	closed   bool
	files    map[*File]struct{}
}

var (
	_ fs.FS         = (*Archive)(nil)
	_ fs.ReadFileFS = (*Archive)(nil)
	_ fs.ReadDirFS  = (*Archive)(nil)
	_ fs.StatFS     = (*Archive)(nil)
)

// Open opens an existing archive and exposes its contents as a filesystem.
func Open(name string, options *Options) (*Archive, error) {
	opts := copyOptions(options)
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	format, err := archivefmt.ResolveInput(string(opts.Format), abs)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	initial, err := archiveInfo(abs)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	dir, err := os.MkdirTemp("", "sevenzip-fs-*")
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	if _, err := archivefmt.Extract(abs, archivefmt.ExtractOptions{
		Format:    string(format),
		OutputDir: dir,
		Password:  opts.Password,
		Overwrite: archivefmt.OverwriteNever,
	}); err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	current, err := archiveInfo(abs)
	if err != nil || !sameArchive(initial, current) {
		return nil, errors.Join(ErrArchiveChanged, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	cleanup = false
	return &Archive{
		name:     abs,
		dir:      dir,
		root:     root,
		options:  opts,
		format:   Format(format),
		initial:  initial,
		readOnly: opts.ReadOnly || !writableFormat(Format(format)),
		files:    make(map[*File]struct{}),
	}, nil
}

// Create creates an empty staged filesystem. The archive is written on Close.
// An existing destination is replaced only if it remains unchanged meanwhile.
func Create(name string, options *Options) (*Archive, error) {
	opts := copyOptions(options)
	if opts.ReadOnly {
		return nil, ErrReadOnly
	}
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	format, err := archivefmt.Resolve(string(opts.Format), abs)
	if err != nil {
		return nil, fmt.Errorf("create archive: %w", err)
	}
	if !writableFormat(Format(format)) {
		return nil, fmt.Errorf("%w: %s", ErrReadOnly, format)
	}
	initial, err := archiveInfoIfExists(abs)
	if err != nil {
		return nil, fmt.Errorf("create archive: %w", err)
	}
	dir, err := os.MkdirTemp("", "sevenzip-fs-*")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &Archive{
		name:    abs,
		dir:     dir,
		root:    root,
		options: opts,
		format:  Format(format),
		initial: initial,
		dirty:   true,
		files:   make(map[*File]struct{}),
	}, nil
}

func copyOptions(options *Options) Options {
	if options == nil {
		return Options{}
	}
	return *options
}

func validateOptions(options Options) error {
	if options.CompressionLevel < 0 || options.CompressionLevel > 9 {
		return fmt.Errorf("compression level %d is outside 1 through 9", options.CompressionLevel)
	}
	if options.HeaderEncryption && options.Password == "" {
		return errors.New("header encryption requires a password")
	}
	return nil
}

func writableFormat(format Format) bool {
	switch format {
	case FormatISO, FormatWIM, FormatVHD, FormatVHDX:
		return false
	default:
		return true
	}
}

// Name returns the absolute path of the backing archive.
func (a *Archive) Name() string { return a.name }

// Format returns the detected or configured archive format.
func (a *Archive) Format() Format { return a.format }

// ReadOnly reports whether mutation methods are disabled.
func (a *Archive) ReadOnly() bool { return a.readOnly }

// Open implements fs.FS. It opens name for reading.
func (a *Archive) Open(name string) (fs.File, error) {
	return a.openFile(name, os.O_RDONLY, 0, false)
}

// OpenFile opens a file using os.OpenFile flags. Write-capable handles mark the
// archive as changed and cause Close to rebuild it.
func (a *Archive) OpenFile(name string, flag int, perm fs.FileMode) (*File, error) {
	writable := flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0
	return a.openFile(name, flag, perm, writable)
}

func (a *Archive) openFile(name string, flag int, perm fs.FileMode, writable bool) (*File, error) {
	if err := validName(name); err != nil {
		return nil, pathError("open", name, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usableLocked(writable); err != nil {
		return nil, pathError("open", name, err)
	}
	file, err := a.root.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	handle := &File{archive: a, file: file, name: name, writable: writable}
	a.files[handle] = struct{}{}
	if writable {
		a.dirty = true
	}
	return handle, nil
}

// Create creates or truncates a file for reading and writing.
func (a *Archive) Create(name string) (*File, error) {
	return a.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

// CreateFile is an alias for Create.
func (a *Archive) CreateFile(name string) (*File, error) { return a.Create(name) }

// ReadFile implements fs.ReadFileFS.
func (a *Archive) ReadFile(name string) ([]byte, error) {
	if err := validName(name); err != nil {
		return nil, pathError("read", name, err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.usableLocked(false); err != nil {
		return nil, pathError("read", name, err)
	}
	return a.root.ReadFile(name)
}

// WriteFile writes data to a file, creating it if necessary.
func (a *Archive) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if err := validName(name); err != nil {
		return pathError("write", name, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usableLocked(true); err != nil {
		return pathError("write", name, err)
	}
	if err := a.root.WriteFile(name, data, perm); err != nil {
		return err
	}
	a.dirty = true
	return nil
}

// ReadDir implements fs.ReadDirFS.
func (a *Archive) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := validName(name); err != nil {
		return nil, pathError("readdir", name, err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.usableLocked(false); err != nil {
		return nil, pathError("readdir", name, err)
	}
	return fs.ReadDir(a.root.FS(), name)
}

// Stat implements fs.StatFS.
func (a *Archive) Stat(name string) (fs.FileInfo, error) {
	if err := validName(name); err != nil {
		return nil, pathError("stat", name, err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.usableLocked(false); err != nil {
		return nil, pathError("stat", name, err)
	}
	return a.root.Stat(name)
}

// Mkdir creates a directory.
func (a *Archive) Mkdir(name string, perm fs.FileMode) error {
	return a.mutate("mkdir", name, func() error { return a.root.Mkdir(name, perm) })
}

// MkdirAll creates a directory and any missing parents.
func (a *Archive) MkdirAll(name string, perm fs.FileMode) error {
	return a.mutate("mkdir", name, func() error { return a.root.MkdirAll(name, perm) })
}

// Remove removes a file or empty directory.
func (a *Archive) Remove(name string) error {
	if name == "." {
		return pathError("remove", name, fs.ErrInvalid)
	}
	return a.mutate("remove", name, func() error { return a.root.Remove(name) })
}

// RemoveAll removes a path and its children. Removing "." is rejected.
func (a *Archive) RemoveAll(name string) error {
	if name == "." {
		return pathError("removeall", name, fs.ErrInvalid)
	}
	return a.mutate("removeall", name, func() error { return a.root.RemoveAll(name) })
}

// Rename renames a file or directory within the archive.
func (a *Archive) Rename(oldName, newName string) error {
	if err := validName(oldName); err != nil {
		return pathError("rename", oldName, err)
	}
	if err := validName(newName); err != nil {
		return pathError("rename", newName, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usableLocked(true); err != nil {
		return pathError("rename", oldName, err)
	}
	if err := a.root.Rename(oldName, newName); err != nil {
		return err
	}
	a.dirty = true
	return nil
}

// Chmod changes a file's permission bits.
func (a *Archive) Chmod(name string, mode fs.FileMode) error {
	return a.mutate("chmod", name, func() error { return a.root.Chmod(name, mode) })
}

// Chtimes changes a file's access and modification times. Formats that do not
// store access times preserve only the modification time.
func (a *Archive) Chtimes(name string, atime, mtime time.Time) error {
	return a.mutate("chtimes", name, func() error { return a.root.Chtimes(name, atime, mtime) })
}

func (a *Archive) mutate(op, name string, fn func() error) error {
	if err := validName(name); err != nil {
		return pathError(op, name, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.usableLocked(true); err != nil {
		return pathError(op, name, err)
	}
	if err := fn(); err != nil {
		return err
	}
	a.dirty = true
	return nil
}

func (a *Archive) usableLocked(write bool) error {
	if a.closed {
		return ErrClosed
	}
	if write && a.readOnly {
		return ErrReadOnly
	}
	return nil
}

func validName(name string) error {
	if !fs.ValidPath(name) || strings.ContainsRune(name, '\\') {
		return fs.ErrInvalid
	}
	return nil
}

func pathError(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// Close closes open file handles and publishes staged changes. If publication
// fails, the original archive remains in place and the Archive is still closed.
func (a *Archive) Close() error {
	return a.finish(true)
}

// Discard closes the filesystem without publishing staged changes.
func (a *Archive) Discard() error {
	return a.finish(false)
}

func (a *Archive) finish(commit bool) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	a.closed = true
	var closeErr error
	for file := range a.files {
		closeErr = errors.Join(closeErr, file.closeLocked())
	}
	closeErr = errors.Join(closeErr, a.root.Close())
	dirty := a.dirty
	readOnly := a.readOnly
	dir, name, format := a.dir, a.name, a.format
	options, initial := a.options, a.initial
	a.root = nil
	a.mu.Unlock()

	var commitErr error
	if closeErr == nil && commit && dirty && !readOnly {
		commitErr = commitArchive(name, dir, format, options, initial)
	}
	cleanupErr := os.RemoveAll(dir)
	return errors.Join(closeErr, commitErr, cleanupErr)
}

func commitArchive(name, dir string, format Format, options Options, initial fs.FileInfo) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "sevenzip-commit-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	staged := filepath.Join(work, "archive")
	addOptions := archivefmt.AddOptions{
		Format:           string(format),
		Solid:            !options.NonSolid,
		SolidDefined:     format == Format7z,
		Password:         options.Password,
		HeaderEncryption: options.HeaderEncryption,
		Level:            options.CompressionLevel,
		LevelDefined:     options.CompressionLevel != 0,
		Method:           options.CompressionMethod,
	}
	if len(entries) == 0 {
		err = archivefmt.CreateEmpty(staged, addOptions)
	} else {
		sources := make([]string, 0, len(entries))
		for _, entry := range entries {
			sources = append(sources, filepath.Join(dir, entry.Name()))
		}
		_, err = archivefmt.Add(staged, sources, addOptions)
	}
	if err != nil {
		return fmt.Errorf("rebuild archive: %w", err)
	}
	input, _, err := security.OpenRegularFile(staged)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := security.CreateOutput(name)
	if err != nil {
		return err
	}
	defer output.Cleanup()
	if err := output.ValidateExisting(initial); err != nil {
		return errors.Join(ErrArchiveChanged, err)
	}
	if _, err := io.Copy(output.File(), input); err != nil {
		return err
	}
	if err := output.File().Sync(); err != nil {
		return err
	}
	if err := input.Close(); err != nil {
		return err
	}
	if err := output.CloseFile(); err != nil {
		return err
	}
	if err := output.Publish(); err != nil {
		return err
	}
	return nil
}

func archiveInfo(name string) (fs.FileInfo, error) {
	file, info, err := security.OpenRegularFile(name)
	if err != nil {
		return nil, err
	}
	return info, file.Close()
}

func archiveInfoIfExists(name string) (fs.FileInfo, error) {
	info, err := archiveInfo(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return info, err
}

func sameArchive(first, second fs.FileInfo) bool {
	return first != nil && second != nil && os.SameFile(first, second) &&
		first.Size() == second.Size() && first.ModTime().Equal(second.ModTime())
}

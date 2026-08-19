package sevenzip

import (
	"io"
	"io/fs"
	"os"
)

// File is a file or directory opened from an Archive. Its methods are safe
// against a concurrent Archive.Close.
type File struct {
	archive  *Archive
	file     *os.File
	name     string
	writable bool
	closed   bool
}

var _ fs.ReadDirFile = (*File)(nil)

// Name returns the archive-relative name used to open the file.
func (f *File) Name() string { return f.name }

func (f *File) Read(p []byte) (int, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return 0, err
	}
	return f.file.Read(p)
}

func (f *File) ReadAt(p []byte, off int64) (int, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return 0, err
	}
	return f.file.ReadAt(p, off)
}

func (f *File) Write(p []byte) (int, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(true); err != nil {
		return 0, err
	}
	return f.file.Write(p)
}

func (f *File) WriteAt(p []byte, off int64) (int, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(true); err != nil {
		return 0, err
	}
	return f.file.WriteAt(p, off)
}

func (f *File) WriteString(value string) (int, error) {
	return f.Write([]byte(value))
}

func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return 0, err
	}
	return f.file.Seek(offset, whence)
}

func (f *File) Stat() (fs.FileInfo, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return nil, err
	}
	return f.file.Stat()
}

func (f *File) ReadDir(n int) ([]fs.DirEntry, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return nil, err
	}
	return f.file.ReadDir(n)
}

func (f *File) Truncate(size int64) error {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(true); err != nil {
		return err
	}
	return f.file.Truncate(size)
}

func (f *File) Sync() error {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return err
	}
	return f.file.Sync()
}

func (f *File) Chmod(mode fs.FileMode) error {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(true); err != nil {
		return err
	}
	return f.file.Chmod(mode)
}

func (f *File) ReadFrom(reader io.Reader) (int64, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(true); err != nil {
		return 0, err
	}
	return io.Copy(f.file, reader)
}

func (f *File) WriteTo(writer io.Writer) (int64, error) {
	f.archive.mu.RLock()
	defer f.archive.mu.RUnlock()
	if err := f.usableLocked(false); err != nil {
		return 0, err
	}
	return io.Copy(writer, f.file)
}

// Close closes the file handle. Archive.Close also closes handles left open.
func (f *File) Close() error {
	f.archive.mu.Lock()
	defer f.archive.mu.Unlock()
	return f.closeLocked()
}

func (f *File) closeLocked() error {
	if f.closed {
		return ErrClosed
	}
	f.closed = true
	delete(f.archive.files, f)
	return f.file.Close()
}

func (f *File) usableLocked(write bool) error {
	if f.closed || f.archive.closed {
		return ErrClosed
	}
	if write && (!f.writable || f.archive.readOnly) {
		if f.archive.readOnly {
			return ErrReadOnly
		}
		return fs.ErrPermission
	}
	return nil
}

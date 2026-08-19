package security

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Output is a temporary file and pinned destination parent used for atomic publication.
type Output struct {
	root     *Root
	target   string
	temp     string
	file     *os.File
	existing fs.FileInfo
}

func CreateOutput(name string) (*Output, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	root, err := openRoot(filepath.Dir(abs), true, true)
	if err != nil {
		return nil, err
	}
	target := filepath.Base(abs)
	var existing fs.FileInfo
	info, statErr := root.Lstat(target)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			root.Close()
			return nil, fmt.Errorf("refusing non-regular archive output %q", name)
		}
		existing = info
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		root.Close()
		return nil, statErr
	}
	tempName, file, err := root.CreateTemp()
	if err != nil {
		root.Close()
		return nil, err
	}
	return &Output{root: root, target: target, temp: tempName, file: file, existing: existing}, nil
}

func (o *Output) File() *os.File { return o.file }

func (o *Output) Existed() bool { return o.existing != nil }

func (o *Output) CloseFile() error {
	if o.file == nil {
		return nil
	}
	err := o.file.Close()
	o.file = nil
	return err
}

func (o *Output) Publish() error {
	if err := o.CloseFile(); err != nil {
		return err
	}
	if o.existing == nil {
		if err := o.root.Link(o.temp, o.target); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("archive output %q appeared while it was being created", o.target)
			}
			return fmt.Errorf("publish archive without replacement: %w", err)
		}
		if err := o.root.Remove(o.temp); err != nil {
			return err
		}
		o.temp = ""
		return o.closeRoot()
	}

	current, err := o.root.Lstat(o.target)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(o.existing, current) {
		if err != nil {
			return err
		}
		return fmt.Errorf("archive output %q changed while it was being written", o.target)
	}
	backup, placeholder, err := o.root.CreateTemp()
	if err != nil {
		return err
	}
	if err := placeholder.Close(); err != nil {
		return err
	}
	if err := o.root.Remove(backup); err != nil {
		return err
	}
	if err := o.root.Rename(o.target, backup); err != nil {
		return err
	}
	if err := o.root.Rename(o.temp, o.target); err != nil {
		_ = o.root.Rename(backup, o.target)
		return err
	}
	o.temp = ""
	if err := o.root.Remove(backup); err != nil {
		return err
	}
	return o.closeRoot()
}

func (o *Output) Cleanup() {
	_ = o.CloseFile()
	if o.root != nil {
		if o.temp != "" {
			_ = o.root.Remove(o.temp)
		}
		_ = o.closeRoot()
	}
}

func (o *Output) closeRoot() error {
	if o.root == nil {
		return nil
	}
	err := o.root.Close()
	o.root = nil
	return err
}

package archivefmt

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/inpadi/7zip/internal/security"
	"github.com/inpadi/7zip/internal/vdisk/vhd"
	"github.com/inpadi/7zip/internal/vdisk/vhdx"
)

type virtualDisk interface {
	io.ReaderAt
	virtualSize() int64
}

type vhdDisk struct{ *vhd.VHD }

func (d vhdDisk) virtualSize() int64 { return d.Size() }

type vhdxDisk struct{ *vhdx.VHDX }

func (d vhdxDisk) virtualSize() int64 {
	size := d.Size()
	if size > uint64(^uint64(0)>>1) {
		return -1
	}
	return int64(size)
}

type diskArchive struct {
	file     *os.File
	disk     virtualDisk
	modified int64
}

func openVirtualDisk(name string, format Format) (*diskArchive, error) {
	file, info, err := security.OpenRegularFile(name)
	if err != nil {
		return nil, err
	}
	var disk virtualDisk
	switch format {
	case FormatVHD:
		reader, parseErr := vhd.NewVHD(file)
		if parseErr != nil {
			_ = file.Close()
			return nil, parseErr
		}
		disk = vhdDisk{reader}
	case FormatVHDX:
		reader, parseErr := vhdx.NewVHDX(file)
		if parseErr != nil {
			_ = file.Close()
			return nil, parseErr
		}
		disk = vhdxDisk{reader}
	default:
		_ = file.Close()
		return nil, fmt.Errorf("unsupported virtual disk format %s", format)
	}
	if disk.virtualSize() <= 0 {
		_ = file.Close()
		return nil, fmt.Errorf("invalid %s virtual disk size", format)
	}
	return &diskArchive{file: file, disk: disk, modified: info.ModTime().UnixNano()}, nil
}

func (a *diskArchive) Close() error { return a.file.Close() }

func (a *diskArchive) entry() Entry {
	entry := Entry{Name: "0.img", Size: uint64(a.disk.virtualSize()), Mode: 0o644}
	if a.modified != 0 {
		entry.Modified = time.Unix(0, a.modified)
	}
	return entry
}

func listVirtualDisk(archive string, patterns []string, format Format) ([]Entry, error) {
	input, err := openVirtualDisk(archive, format)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	entry := input.entry()
	selected, err := archive7z.Matches(entry.Name, patterns)
	if err != nil || !selected {
		return nil, err
	}
	var budget security.Budget
	if err := budget.AddEntry(entry.Name, entry.Size); err != nil {
		return nil, err
	}
	return []Entry{entry}, nil
}

func processVirtualDisk(archive string, patterns []string, format Format, dst io.Writer, extract *ExtractOptions) (Result, error) {
	input, err := openVirtualDisk(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.Close()
	entry := input.entry()
	selected, err := archive7z.Matches(entry.Name, patterns)
	if err != nil || !selected {
		return Result{}, err
	}
	var budget security.Budget
	reader := io.NewSectionReader(input.disk, 0, int64(entry.Size))
	if extract == nil {
		if err := budget.AddEntry(entry.Name, entry.Size); err != nil {
			return Result{}, err
		}
		n, err := budget.Copy(dst, reader, entry.Name)
		if err != nil {
			return Result{}, err
		}
		return Result{Files: 1, Bytes: uint64(n)}, nil
	}
	root, err := extractionRoot(extract.OutputDir)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	parents := extractionParents(root, *extract)
	defer parents.Close()
	n, wrote, err := extractEntry(parents, entry.Name, entry.Mode, entry.Size, reader, *extract, make(map[string]string), &budget)
	if err != nil {
		return Result{}, err
	}
	if !wrote {
		return Result{}, nil
	}
	return Result{Files: 1, Bytes: uint64(n)}, nil
}

package archivefmt

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/inpadi/7zip/internal/wim"
)

type wimEntry struct {
	entry Entry
	file  *wim.File
}

type wimArchive struct {
	file    *os.File
	reader  *wim.Reader
	entries []wimEntry
}

func openWIM(name string) (*wimArchive, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	reader, err := wim.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	archive := &wimArchive{file: file, reader: reader}
	multiple := len(reader.Image) > 1
	for i, image := range reader.Image {
		root, err := image.Open()
		if err != nil {
			archive.Close()
			return nil, fmt.Errorf("open WIM image %d: %w", i+1, err)
		}
		prefix := ""
		if multiple {
			prefix = strconv.Itoa(i + 1)
			archive.entries = append(archive.entries, wimEntry{entry: Entry{Name: prefix + "/", Mode: fs.ModeDir | 0o755}})
		}
		if err := archive.walk(root, prefix, 0); err != nil {
			archive.Close()
			return nil, fmt.Errorf("read WIM image %d: %w", i+1, err)
		}
	}
	return archive, nil
}

func (a *wimArchive) Close() error {
	readerErr := a.reader.Close()
	fileErr := a.file.Close()
	if readerErr != nil {
		return readerErr
	}
	return fileErr
}

func (a *wimArchive) walk(directory *wim.File, prefix string, depth int) error {
	if depth > 256 {
		return errors.New("WIM directory nesting exceeds 256 levels")
	}
	children, err := directory.Readdir()
	if err != nil {
		return err
	}
	for _, child := range children {
		name, err := safeName(path.Join(prefix, child.Name))
		if err != nil {
			return err
		}
		if child.Size < 0 {
			return fmt.Errorf("negative size for WIM entry %q", name)
		}
		mode := fs.FileMode(0o644)
		if child.Attributes&wim.FILE_ATTRIBUTE_READONLY != 0 {
			mode = 0o444
		}
		if child.Attributes&wim.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			mode |= fs.ModeSymlink
		} else if child.IsDir() {
			mode = fs.ModeDir | 0o755
			name = strings.TrimSuffix(name, "/") + "/"
		}
		a.entries = append(a.entries, wimEntry{entry: Entry{
			Name:       name,
			Size:       uint64(child.Size),
			Modified:   child.LastWriteTime.Time(),
			Mode:       mode,
			Attributes: child.Attributes,
		}, file: child})
		if child.IsDir() {
			if err := a.walk(child, strings.TrimSuffix(name, "/"), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func listWIM(archive string, patterns []string) ([]Entry, error) {
	input, err := openWIM(archive)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	var entries []Entry
	for _, item := range input.entries {
		selected, err := archive7z.Matches(item.entry.Name, patterns)
		if err != nil {
			return nil, err
		}
		if selected {
			entries = append(entries, item.entry)
		}
	}
	return entries, nil
}

func processWIM(archive string, patterns []string, dst io.Writer, extract *ExtractOptions) (Result, error) {
	input, err := openWIM(archive)
	if err != nil {
		return Result{}, err
	}
	defer input.Close()
	root := ""
	seen := make(map[string]string)
	if extract != nil {
		root, err = extractionRoot(extract.OutputDir)
		if err != nil {
			return Result{}, err
		}
	}
	var result Result
	for _, item := range input.entries {
		selected, matchErr := archive7z.Matches(item.entry.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if item.entry.Mode&fs.ModeSymlink != 0 {
			return result, fmt.Errorf("WIM reparse point %q is not supported", item.entry.Name)
		}
		var reader io.ReadCloser
		if !item.entry.Mode.IsDir() {
			reader, err = item.file.Open()
			if err != nil {
				return result, fmt.Errorf("open WIM entry %q: %w", item.entry.Name, err)
			}
		}
		stream := io.Reader(strings.NewReader(""))
		if reader != nil {
			stream = reader
		}
		if extract != nil {
			n, wrote, extractErr := extractEntry(root, item.entry.Name, item.entry.Mode, item.entry.Modified, stream, *extract, seen)
			if reader != nil {
				closeErr := reader.Close()
				if extractErr == nil {
					extractErr = closeErr
				}
			}
			if extractErr != nil {
				return result, extractErr
			}
			if wrote {
				result.Files++
				result.Bytes += uint64(n)
			}
			continue
		}
		if item.entry.Mode.IsDir() {
			continue
		}
		n, copyErr := io.Copy(dst, stream)
		closeErr := reader.Close()
		if copyErr != nil {
			return result, copyErr
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

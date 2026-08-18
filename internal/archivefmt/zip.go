package archivefmt

import (
	"archive/zip"
	"compress/flate"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/inpadi/7zip/internal/archive7z"
)

func addZip(archive string, sourceNames []string, level int, method string, recursive bool, excludes []string) (result Result, err error) {
	existed, err := archiveExists(archive)
	if err != nil {
		return result, err
	}
	sources, err := collectSources(sourceNames, archive, recursive)
	if err != nil {
		return result, err
	}
	sources, err = filterSources(sources, excludes, recursive)
	if err != nil {
		return result, err
	}
	if err := ensureSources(sources); err != nil {
		return result, err
	}
	replacements := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		replacements[nameKey(source.name)] = struct{}{}
	}

	var old *zip.ReadCloser
	if existed {
		old, err = zip.OpenReader(archive)
		if err != nil {
			return result, fmt.Errorf("open ZIP archive: %w", err)
		}
		defer old.Close()
	}

	absolute, err := filepath.Abs(archive)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".zip-go-*.tmp")
	if err != nil {
		return result, err
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = os.Remove(tempName)
		}
	}()
	zw := zip.NewWriter(temp)
	entryMethod := uint16(zip.Deflate)
	if level == 0 || method == "copy" || method == "store" {
		entryMethod = zip.Store
	} else if level > 0 {
		zw.RegisterCompressor(zip.Deflate, func(dst io.Writer) (io.WriteCloser, error) {
			return flate.NewWriter(dst, level)
		})
	}

	if old != nil {
		for _, file := range old.File {
			name, cleanErr := safeName(file.Name)
			if cleanErr != nil {
				return result, fmt.Errorf("preserve ZIP entry: %w", cleanErr)
			}
			if _, replaced := replacements[nameKey(name)]; replaced {
				continue
			}
			header := file.FileHeader
			header.Name = name
			if file.FileInfo().IsDir() {
				header.Name = strings.TrimSuffix(name, "/") + "/"
				header.Method = zip.Store
			} else {
				header.Method = entryMethod
			}
			dst, createErr := zw.CreateHeader(&header)
			if createErr != nil {
				return result, createErr
			}
			if file.FileInfo().IsDir() {
				continue
			}
			src, openErr := file.Open()
			if openErr != nil {
				return result, openErr
			}
			_, copyErr := io.Copy(dst, src)
			closeErr := src.Close()
			if copyErr != nil {
				return result, copyErr
			}
			if closeErr != nil {
				return result, closeErr
			}
		}
	}

	for _, source := range sources {
		header, headerErr := zip.FileInfoHeader(source.info)
		if headerErr != nil {
			return result, headerErr
		}
		header.Name = source.name
		if source.info.IsDir() {
			header.Name = strings.TrimSuffix(source.name, "/") + "/"
			header.Method = zip.Store
		} else {
			header.Method = entryMethod
		}
		dst, createErr := zw.CreateHeader(header)
		if createErr != nil {
			return result, createErr
		}
		if source.info.IsDir() {
			continue
		}
		src, openErr := os.Open(source.path)
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := io.Copy(dst, src)
		closeErr := src.Close()
		if copyErr != nil {
			return result, copyErr
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	if err = zw.Close(); err != nil {
		return result, err
	}
	if err = temp.Sync(); err != nil {
		return result, err
	}
	if err = temp.Close(); err != nil {
		return result, err
	}
	if old != nil {
		if err = old.Close(); err != nil {
			return result, err
		}
		old = nil
	}
	if err = publish(tempName, absolute, existed); err != nil {
		return result, err
	}
	return result, nil
}

func listZip(archive string, patterns []string) ([]Entry, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	entries := make([]Entry, 0, len(zr.File))
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, patterns)
		if matchErr != nil {
			return nil, matchErr
		}
		if !selected {
			continue
		}
		entries = append(entries, Entry{
			Name:              file.Name,
			Size:              file.UncompressedSize64,
			PackedSize:        file.CompressedSize64,
			PackedSizeDefined: true,
			Modified:          file.Modified,
			Mode:              file.Mode(),
			CRC32:             file.CRC32,
		})
	}
	return entries, nil
}

func testZip(archive string, patterns []string) (Result, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	var result Result
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected || file.FileInfo().IsDir() {
			continue
		}
		src, openErr := file.Open()
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := io.Copy(io.Discard, src)
		closeErr := src.Close()
		if copyErr != nil {
			return result, fmt.Errorf("test %q: %w", file.Name, copyErr)
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

func writeZip(archive string, patterns []string, dst io.Writer) (Result, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	var result Result
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected || file.FileInfo().IsDir() {
			continue
		}
		src, openErr := file.Open()
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := io.Copy(dst, src)
		closeErr := src.Close()
		if copyErr != nil {
			return result, fmt.Errorf("write %q: %w", file.Name, copyErr)
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

func extractZip(archive string, options ExtractOptions) (Result, error) {
	root, err := extractionRoot(options.OutputDir)
	if err != nil {
		return Result{}, err
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	seen := make(map[string]string)
	var result Result
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, options.Patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		var src io.ReadCloser
		if !file.FileInfo().IsDir() {
			src, err = file.Open()
			if err != nil {
				return result, err
			}
		}
		reader := io.Reader(strings.NewReader(""))
		if src != nil {
			reader = src
		}
		n, wrote, extractErr := extractEntry(root, file.Name, file.Mode(), file.Modified, reader, options, seen)
		if src != nil {
			closeErr := src.Close()
			if extractErr == nil {
				extractErr = closeErr
			}
		}
		if extractErr != nil {
			return result, fmt.Errorf("extract %q: %w", file.Name, extractErr)
		}
		if wrote {
			result.Files++
			result.Bytes += uint64(n)
		}
	}
	return result, nil
}

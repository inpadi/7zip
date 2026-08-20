package archivefmt

import (
	"archive/zip"
	"compress/flate"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/inpadi/7zip/internal/security"
	kpflate "github.com/klauspost/compress/flate"
)

type zipInput struct {
	*zip.Reader
	file *os.File
}

func openZip(name string) (*zipInput, error) {
	file, info, err := security.OpenRegularFile(name)
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		file.Close()
		return nil, err
	}
	reader.RegisterDecompressor(zip.Deflate, func(src io.Reader) io.ReadCloser {
		return kpflate.NewReader(src)
	})
	return &zipInput{Reader: reader, file: file}, nil
}

func (z *zipInput) Close() error { return z.file.Close() }

func addZip(archive string, sourceNames []string, level int, method string, recursive bool, excludes []string) (result Result, err error) {
	sources, roots, err := collectSources(sourceNames, archive, recursive)
	if err != nil {
		return result, err
	}
	defer closeSourceRoots(roots)
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
	output, err := security.CreateOutput(archive)
	if err != nil {
		return result, err
	}
	defer output.Cleanup()
	existed := output.Existed()

	var old *zipInput
	if existed {
		old, err = openZip(archive)
		if err != nil {
			return result, fmt.Errorf("open ZIP archive: %w", err)
		}
		defer old.Close()
	}

	temp := output.File()
	zw := zip.NewWriter(temp)
	var budget security.Budget
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
			if err := budget.AddEntry(name, file.UncompressedSize64); err != nil {
				return result, err
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
			_, copyErr := budget.Copy(dst, src, name)
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
		if err := budget.AddEntry(source.name, uint64(max(source.info.Size(), 0))); err != nil {
			return result, err
		}
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
		src, openErr := source.open()
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := budget.Copy(dst, src, source.name)
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
	if err = output.CloseFile(); err != nil {
		return result, err
	}
	if old != nil {
		if err = old.Close(); err != nil {
			return result, err
		}
		old = nil
	}
	if err = output.Publish(); err != nil {
		return result, err
	}
	return result, nil
}

func listZip(archive string, patterns []string) ([]Entry, error) {
	zr, err := openZip(archive)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	entries := make([]Entry, 0, len(zr.File))
	var budget security.Budget
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, patterns)
		if matchErr != nil {
			return nil, matchErr
		}
		if !selected {
			continue
		}
		if err := security.CheckCompressionRatio(file.Name, file.UncompressedSize64, file.CompressedSize64); err != nil {
			return nil, err
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize64); err != nil {
			return nil, err
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
	zr, err := openZip(archive)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	var result Result
	var budget security.Budget
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if err := security.CheckCompressionRatio(file.Name, file.UncompressedSize64, file.CompressedSize64); err != nil {
			return result, err
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize64); err != nil {
			return result, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		src, openErr := file.Open()
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := budget.Copy(io.Discard, src, file.Name)
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
	zr, err := openZip(archive)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	var result Result
	var budget security.Budget
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if err := security.CheckCompressionRatio(file.Name, file.UncompressedSize64, file.CompressedSize64); err != nil {
			return result, err
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize64); err != nil {
			return result, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		src, openErr := file.Open()
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := budget.Copy(dst, src, file.Name)
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
	defer root.Close()
	parents := extractionParents(root, options)
	defer parents.Close()
	zr, err := openZip(archive)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	seen := make(map[string]string)
	var result Result
	var budget security.Budget
	for _, file := range zr.File {
		selected, matchErr := archive7z.Matches(file.Name, options.Patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if err := security.CheckCompressionRatio(file.Name, file.UncompressedSize64, file.CompressedSize64); err != nil {
			return result, err
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
		n, wrote, extractErr := extractEntry(parents, file.Name, file.Mode(), file.UncompressedSize64, reader, options, seen, &budget)
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

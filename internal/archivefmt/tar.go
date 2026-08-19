package archivefmt

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

type tarInput struct {
	file   *os.File
	reader *tar.Reader
	close  func() error
}

func openTar(archive string, format Format) (*tarInput, error) {
	file, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	reader := io.Reader(file)
	closeCompression := func() error { return nil }
	switch format {
	case FormatTarGzip:
		compressed, gzipErr := gzip.NewReader(file)
		if gzipErr != nil {
			_ = file.Close()
			return nil, gzipErr
		}
		reader = compressed
		closeCompression = compressed.Close
	case FormatTarBzip2:
		reader = bzip2.NewReader(file)
	case FormatTarXZ:
		compressed, xzErr := newXZReader(file)
		if xzErr != nil {
			_ = file.Close()
			return nil, xzErr
		}
		reader = compressed
		closeCompression = compressed.Close
	case FormatTarZstd:
		compressed, zstdErr := zstd.NewReader(file)
		if zstdErr != nil {
			_ = file.Close()
			return nil, zstdErr
		}
		reader = compressed
		closeCompression = func() error {
			compressed.Close()
			return nil
		}
	}
	return &tarInput{
		file:   file,
		reader: tar.NewReader(reader),
		close: func() error {
			compressionErr := closeCompression()
			fileErr := file.Close()
			if compressionErr != nil {
				return compressionErr
			}
			return fileErr
		},
	}, nil
}

type tarOutput struct {
	writer *tar.Writer
	close  func() error
}

func newTarOutput(dst io.Writer, format Format, level int) (*tarOutput, error) {
	archiveWriter := dst
	closeCompression := func() error { return nil }
	switch format {
	case FormatTarGzip:
		compressed, err := gzip.NewWriterLevel(dst, level)
		if err != nil {
			return nil, err
		}
		archiveWriter = compressed
		closeCompression = compressed.Close
	case FormatTarBzip2:
		config := &dsnetbzip2.WriterConfig{}
		if level >= 0 {
			config.Level = max(level, 1)
		}
		compressed, err := dsnetbzip2.NewWriter(dst, config)
		if err != nil {
			return nil, err
		}
		archiveWriter = compressed
		closeCompression = compressed.Close
	case FormatTarXZ:
		config := xz.WriterConfig{}
		if level >= 0 {
			config.DictCap = dictionaryForCompressionLevel(level)
		}
		compressed, err := config.NewWriter(dst)
		if err != nil {
			return nil, err
		}
		archiveWriter = compressed
		closeCompression = compressed.Close
	case FormatTarZstd:
		var options []zstd.EOption
		if level >= 0 {
			options = append(options, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(zstdLevel(level))))
		}
		compressed, err := zstd.NewWriter(dst, options...)
		if err != nil {
			return nil, err
		}
		archiveWriter = compressed
		closeCompression = compressed.Close
	}
	tarWriter := tar.NewWriter(archiveWriter)
	return &tarOutput{
		writer: tarWriter,
		close: func() error {
			tarErr := tarWriter.Close()
			compressionErr := closeCompression()
			if tarErr != nil {
				return tarErr
			}
			return compressionErr
		},
	}, nil
}

func addTar(archive string, sourceNames []string, format Format, level int, recursive bool, excludes []string) (result Result, err error) {
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

	var old *tarInput
	if existed {
		old, err = openTar(archive, format)
		if err != nil {
			return result, err
		}
		defer func() {
			if old != nil {
				_ = old.close()
			}
		}()
	}
	absolute, err := filepath.Abs(archive)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".tar-go-*.tmp")
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
	out, err := newTarOutput(temp, format, level)
	if err != nil {
		return result, err
	}

	if old != nil {
		for {
			header, nextErr := old.reader.Next()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return result, nextErr
			}
			name, cleanErr := safeName(header.Name)
			if cleanErr != nil {
				return result, cleanErr
			}
			if _, replaced := replacements[nameKey(name)]; replaced {
				continue
			}
			copyHeader := *header
			copyHeader.Name = name
			if header.FileInfo().IsDir() {
				copyHeader.Name = strings.TrimSuffix(name, "/") + "/"
			}
			if writeErr := out.writer.WriteHeader(&copyHeader); writeErr != nil {
				return result, writeErr
			}
			if header.Size > 0 {
				if _, copyErr := io.CopyN(out.writer, old.reader, header.Size); copyErr != nil {
					return result, copyErr
				}
			}
		}
	}

	for _, source := range sources {
		header, headerErr := tar.FileInfoHeader(source.info, "")
		if headerErr != nil {
			return result, headerErr
		}
		header.Name = source.name
		if source.info.IsDir() {
			header.Name = strings.TrimSuffix(source.name, "/") + "/"
		}
		if writeErr := out.writer.WriteHeader(header); writeErr != nil {
			return result, writeErr
		}
		if source.info.IsDir() {
			continue
		}
		src, openErr := os.Open(source.path)
		if openErr != nil {
			return result, openErr
		}
		n, copyErr := io.Copy(out.writer, src)
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
	if err = out.close(); err != nil {
		return result, err
	}
	if err = temp.Sync(); err != nil {
		return result, err
	}
	if err = temp.Close(); err != nil {
		return result, err
	}
	if old != nil {
		if err = old.close(); err != nil {
			return result, err
		}
		old = nil
	}
	if err = publish(tempName, absolute, existed); err != nil {
		return result, err
	}
	return result, nil
}

func listTar(archive string, patterns []string, format Format) ([]Entry, error) {
	input, err := openTar(archive, format)
	if err != nil {
		return nil, err
	}
	defer input.close()
	var entries []Entry
	for {
		header, nextErr := input.reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		selected, matchErr := archive7z.Matches(header.Name, patterns)
		if matchErr != nil {
			return nil, matchErr
		}
		if !selected {
			continue
		}
		entries = append(entries, Entry{
			Name:     header.Name,
			Size:     uint64(max(header.Size, 0)),
			Modified: header.ModTime,
			Mode:     header.FileInfo().Mode(),
		})
	}
	return entries, nil
}

func testTar(archive string, patterns []string, format Format) (Result, error) {
	input, err := openTar(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	var result Result
	for {
		header, nextErr := input.reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return result, nextErr
		}
		selected, matchErr := archive7z.Matches(header.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected || header.FileInfo().IsDir() {
			continue
		}
		n, copyErr := io.Copy(io.Discard, input.reader)
		if copyErr != nil {
			return result, copyErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

func writeTar(archive string, patterns []string, format Format, dst io.Writer) (Result, error) {
	input, err := openTar(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	var result Result
	for {
		header, nextErr := input.reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return result, nextErr
		}
		selected, matchErr := archive7z.Matches(header.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected || header.FileInfo().IsDir() {
			continue
		}
		if header.FileInfo().Mode().Type() != 0 {
			return result, fmt.Errorf("refusing unsupported special entry %q", header.Name)
		}
		n, copyErr := io.Copy(dst, input.reader)
		if copyErr != nil {
			return result, copyErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

func extractTar(archive string, options ExtractOptions, format Format) (Result, error) {
	root, err := extractionRoot(options.OutputDir)
	if err != nil {
		return Result{}, err
	}
	input, err := openTar(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	seen := make(map[string]string)
	var result Result
	for {
		header, nextErr := input.reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return result, nextErr
		}
		selected, matchErr := archive7z.Matches(header.Name, options.Patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		n, wrote, extractErr := extractEntry(
			root,
			header.Name,
			header.FileInfo().Mode(),
			header.ModTime,
			input.reader,
			options,
			seen,
		)
		if extractErr != nil {
			return result, fmt.Errorf("extract %q: %w", header.Name, extractErr)
		}
		if wrote {
			result.Files++
			result.Bytes += uint64(n)
		}
	}
	return result, nil
}

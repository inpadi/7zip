package archivefmt

import (
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func isSingleStream(format Format) bool {
	switch format {
	case FormatGzip, FormatBzip2, FormatXZ, FormatZstd:
		return true
	default:
		return false
	}
}

func addStream(archive string, sourceNames []string, format Format, level int, recursive bool, excludes []string) (result Result, err error) {
	sources, err := collectSources(sourceNames, archive, recursive)
	if err != nil {
		return result, err
	}
	sources, err = filterSources(sources, excludes, recursive)
	if err != nil {
		return result, err
	}
	regular := make([]sourceFile, 0, len(sources))
	for _, source := range sources {
		if !source.info.IsDir() {
			regular = append(regular, source)
		}
	}
	if len(regular) != 1 {
		return result, fmt.Errorf("%s is a single-stream format and requires exactly one regular input file", format)
	}
	source := regular[0]
	existed, err := archiveExists(archive)
	if err != nil {
		return result, err
	}
	absolute, err := filepath.Abs(archive)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".stream-go-*.tmp")
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

	var compressed io.WriteCloser
	switch format {
	case FormatGzip:
		writer, writerErr := gzip.NewWriterLevel(temp, level)
		if writerErr != nil {
			return result, writerErr
		}
		writer.Name = source.name
		writer.ModTime = source.info.ModTime()
		compressed = writer
	case FormatBzip2:
		config := &dsnetbzip2.WriterConfig{}
		if level >= 0 {
			config.Level = max(level, 1)
		}
		compressed, err = dsnetbzip2.NewWriter(temp, config)
	case FormatXZ:
		config := xz.WriterConfig{}
		if level >= 0 {
			config.DictCap = dictionaryForCompressionLevel(level)
		}
		compressed, err = config.NewWriter(temp)
	case FormatZstd:
		var options []zstd.EOption
		if level >= 0 {
			options = append(options, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(zstdLevel(level))))
		}
		compressed, err = zstd.NewWriter(temp, options...)
	}
	if err != nil {
		return result, err
	}
	src, err := os.Open(source.path)
	if err != nil {
		return result, err
	}
	n, copyErr := io.Copy(compressed, src)
	closeSourceErr := src.Close()
	closeCompressionErr := compressed.Close()
	if copyErr != nil {
		return result, copyErr
	}
	if closeSourceErr != nil {
		return result, closeSourceErr
	}
	if closeCompressionErr != nil {
		return result, closeCompressionErr
	}
	if err = temp.Sync(); err != nil {
		return result, err
	}
	if err = temp.Close(); err != nil {
		return result, err
	}
	if err = publish(tempName, absolute, existed); err != nil {
		return result, err
	}
	return Result{Files: 1, Bytes: uint64(n)}, nil
}

func dictionaryForCompressionLevel(level int) int {
	switch {
	case level <= 2:
		return 1 << 20
	case level <= 4:
		return 4 << 20
	case level <= 5:
		return 16 << 20
	case level <= 7:
		return 32 << 20
	default:
		return 64 << 20
	}
}

func zstdLevel(level int) int {
	if level <= 0 {
		return 1
	}
	return 1 + (level-1)*18/8
}

type streamInput struct {
	reader   io.Reader
	close    func() error
	name     string
	modified time.Time
}

func openStream(archive string, format Format) (*streamInput, error) {
	file, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	name := streamEntryName(archive, format)
	input := &streamInput{reader: file, name: name, close: file.Close}
	switch format {
	case FormatGzip:
		reader, gzipErr := gzip.NewReader(file)
		if gzipErr != nil {
			_ = file.Close()
			return nil, gzipErr
		}
		input.reader = reader
		if reader.Name != "" {
			if clean, cleanErr := safeName(reader.Name); cleanErr == nil {
				input.name = clean
			} else {
				_ = reader.Close()
				_ = file.Close()
				return nil, cleanErr
			}
		}
		input.modified = reader.ModTime
		input.close = func() error {
			readerErr := reader.Close()
			fileErr := file.Close()
			if readerErr != nil {
				return readerErr
			}
			return fileErr
		}
	case FormatBzip2:
		input.reader = bzip2.NewReader(file)
	case FormatXZ:
		reader, xzErr := xz.NewReader(file)
		if xzErr != nil {
			_ = file.Close()
			return nil, xzErr
		}
		input.reader = reader
	case FormatZstd:
		reader, zstdErr := zstd.NewReader(file)
		if zstdErr != nil {
			_ = file.Close()
			return nil, zstdErr
		}
		input.reader = reader
		input.close = func() error {
			reader.Close()
			return file.Close()
		}
	}
	return input, nil
}

func streamEntryName(archive string, format Format) string {
	name := filepath.Base(archive)
	suffix := map[Format]string{
		FormatGzip:  ".gz",
		FormatBzip2: ".bz2",
		FormatXZ:    ".xz",
		FormatZstd:  ".zst",
	}[format]
	if format == FormatZstd && strings.HasSuffix(strings.ToLower(name), ".zstd") {
		suffix = ".zstd"
	}
	if strings.HasSuffix(strings.ToLower(name), suffix) {
		name = name[:len(name)-len(suffix)]
	}
	return name
}

func listStream(archive string, patterns []string, format Format) ([]Entry, error) {
	input, err := openStream(archive, format)
	if err != nil {
		return nil, err
	}
	defer input.close()
	selected, err := archive7z.Matches(input.name, patterns)
	if err != nil || !selected {
		return nil, err
	}
	n, err := io.Copy(io.Discard, input.reader)
	if err != nil {
		return nil, err
	}
	return []Entry{{Name: input.name, Size: uint64(n), Modified: input.modified, Mode: 0o644}}, nil
}

func testStream(archive string, patterns []string, format Format) (Result, error) {
	input, err := openStream(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	selected, err := archive7z.Matches(input.name, patterns)
	if err != nil || !selected {
		return Result{}, err
	}
	n, err := io.Copy(io.Discard, input.reader)
	if err != nil {
		return Result{}, err
	}
	return Result{Files: 1, Bytes: uint64(n)}, nil
}

func writeStream(archive string, patterns []string, format Format, dst io.Writer) (Result, error) {
	input, err := openStream(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	selected, err := archive7z.Matches(input.name, patterns)
	if err != nil || !selected {
		return Result{}, err
	}
	n, err := io.Copy(dst, input.reader)
	if err != nil {
		return Result{}, err
	}
	return Result{Files: 1, Bytes: uint64(n)}, nil
}

func extractStream(archive string, options ExtractOptions, format Format) (Result, error) {
	root, err := extractionRoot(options.OutputDir)
	if err != nil {
		return Result{}, err
	}
	input, err := openStream(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	selected, err := archive7z.Matches(input.name, options.Patterns)
	if err != nil || !selected {
		return Result{}, err
	}
	n, wrote, err := extractEntry(root, input.name, 0o644, input.modified, input.reader, options, make(map[string]string))
	if err != nil {
		return Result{}, err
	}
	if !wrote {
		return Result{}, nil
	}
	return Result{Files: 1, Bytes: uint64(n)}, nil
}

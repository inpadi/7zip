package archivefmt

import (
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/inpadi/7zip/internal/archive7z"
	"github.com/inpadi/7zip/internal/bzip2reader"
	"github.com/inpadi/7zip/internal/security"
	"github.com/inpadi/7zip/internal/xz"
	"github.com/klauspost/compress/zstd"
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
	sources, roots, err := collectSources(sourceNames, archive, recursive)
	if err != nil {
		return result, err
	}
	defer closeSourceRoots(roots)
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
	output, err := security.CreateOutput(archive)
	if err != nil {
		return result, err
	}
	defer output.Cleanup()
	temp := output.File()

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
		compressed, err = newBzip2Writer(temp, bzip2BlockLevel(level))
	case FormatXZ:
		config := xz.WriterConfig{}
		if level >= 0 {
			config.DictCap = dictionaryForCompressionLevel(level)
		}
		compressed, err = newXZWriter(temp, config)
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
	src, err := source.open()
	if err != nil {
		return result, err
	}
	var budget security.Budget
	if err := budget.AddEntry(source.name, uint64(max(source.info.Size(), 0))); err != nil {
		_ = src.Close()
		_ = compressed.Close()
		return result, err
	}
	n, copyErr := budget.Copy(compressed, src, source.name)
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
	if err = output.CloseFile(); err != nil {
		return result, err
	}
	if err = output.Publish(); err != nil {
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

func bzip2BlockLevel(level int) int {
	if level < 0 {
		level = 5
	}
	switch {
	case level <= 1:
		return 1
	case level < 5:
		return level*2 - 1
	default:
		return 9
	}
}

func zstdLevel(level int) int {
	if level <= 0 {
		return 1
	}
	return 1 + (level-1)*18/8
}

type streamInput struct {
	reader     io.Reader
	close      func() error
	name       string
	modified   time.Time
	compressed int64
}

func openStream(archive string, format Format) (*streamInput, error) {
	file, info, err := security.OpenRegularFile(archive)
	if err != nil {
		return nil, err
	}
	name := streamEntryName(archive, format)
	input := &streamInput{reader: file, name: name, close: file.Close, compressed: info.Size()}
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
		reader, bzipErr := bzip2reader.NewReader(file)
		if bzipErr != nil {
			_ = file.Close()
			return nil, bzipErr
		}
		input.reader = reader
		input.close = reader.Close
	case FormatXZ:
		reader, xzErr := newXZReader(file)
		if xzErr != nil {
			_ = file.Close()
			return nil, xzErr
		}
		input.reader = reader
		input.close = func() error {
			readerErr := reader.Close()
			fileErr := file.Close()
			if readerErr != nil {
				return readerErr
			}
			return fileErr
		}
	case FormatZstd:
		reader, zstdErr := zstd.NewReader(file,
			zstd.WithDecoderMaxMemory(security.MaxDecoderMemory),
			zstd.WithDecoderMaxWindow(security.MaxDecoderMemory),
			zstd.WithDecoderLowmem(false),
		)
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
	var budget security.Budget
	budget.SetCompressedBytes(input.compressed)
	if err := budget.AddEntry(input.name, 0); err != nil {
		return nil, err
	}
	n, err := budget.Copy(io.Discard, input.reader, input.name)
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
	var budget security.Budget
	budget.SetCompressedBytes(input.compressed)
	if err := budget.AddEntry(input.name, 0); err != nil {
		return Result{}, err
	}
	n, err := budget.Copy(io.Discard, input.reader, input.name)
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
	var budget security.Budget
	budget.SetCompressedBytes(input.compressed)
	if err := budget.AddEntry(input.name, 0); err != nil {
		return Result{}, err
	}
	n, err := budget.Copy(dst, input.reader, input.name)
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
	defer root.Close()
	parents := extractionParents(root, options)
	defer parents.Close()
	input, err := openStream(archive, format)
	if err != nil {
		return Result{}, err
	}
	defer input.close()
	selected, err := archive7z.Matches(input.name, options.Patterns)
	if err != nil || !selected {
		return Result{}, err
	}
	var budget security.Budget
	budget.SetCompressedBytes(input.compressed)
	n, wrote, err := extractEntry(parents, input.name, 0o644, 0, input.reader, options, make(map[string]string), &budget)
	if err != nil {
		return Result{}, err
	}
	if !wrote {
		return Result{}, nil
	}
	return Result{Files: 1, Bytes: uint64(n)}, nil
}

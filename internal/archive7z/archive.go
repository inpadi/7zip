// Package archive7z implements the first native 7z compatibility slice.
package archive7z

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inpadi/7zip/internal/security"
	"github.com/inpadi/7zip/internal/sevenzip"
)

// Entry is portable archive metadata used by the command layer.
type Entry struct {
	Name              string
	Size              uint64
	PackedSize        uint64
	PackedSizeDefined bool
	Modified          time.Time
	Mode              fs.FileMode
	Attributes        uint32
	CRC32             uint32
}

// Result summarizes an archive operation.
type Result struct {
	Files int
	Bytes uint64
}

// OverwritePolicy controls extraction collisions.
type OverwritePolicy uint8

const (
	OverwriteNever OverwritePolicy = iota
	OverwriteAll
	OverwriteSkip
)

// PublicationMode controls how newly extracted files become visible.
type PublicationMode uint8

const (
	PublicationDirect PublicationMode = iota
	PublicationAtomic
)

// ExtractOptions configures extraction without exposing CLI internals.
type ExtractOptions struct {
	OutputDir   string
	Patterns    []string
	Password    string
	Flatten     bool
	Overwrite   OverwritePolicy
	Publication PublicationMode
}

// AddOptions configures native 7z creation and updates.
type AddOptions struct {
	Solid            bool
	Password         string
	HeaderEncryption bool
	DisableFilters   bool
	Level            int
	LevelDefined     bool
	Method           string
	Recursive        bool
	Excludes         []string
}

type inputFile struct {
	path     string
	name     string
	info     fs.FileInfo
	root     *security.Root
	relative string
}

func (i inputFile) open() (*os.File, error) {
	// Root.Open contains any link resolution; the descriptor identity check
	// prevents a post-enumeration path change from redirecting the input.
	file, err := i.root.Open(i.relative)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(i.info, after) {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("input %q changed after it was enumerated", i.path)
	}
	return file, nil
}

// Add creates or updates a solid LZMA2-compressed 7z archive.
func Add(archive string, sources []string) (result Result, err error) {
	return AddWithOptions(archive, sources, AddOptions{Solid: true})
}

// CreateEmptyWithOptions creates a valid 7z archive containing no entries.
func CreateEmptyWithOptions(archive string, options AddOptions) (err error) {
	output, err := security.CreateOutput(archive)
	if err != nil {
		return err
	}
	defer output.Cleanup()

	level := -1
	if options.LevelDefined {
		level = options.Level
	}
	writer, err := newWriter(output.File(), writerOptions{
		solid:            options.Solid,
		password:         options.Password,
		headerEncryption: options.HeaderEncryption,
		level:            level,
		method:           options.Method,
	})
	if err != nil {
		return fmt.Errorf("create 7z writer: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish archive: %w", err)
	}
	if err := output.File().Sync(); err != nil {
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := output.CloseFile(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	return output.Publish()
}

// AddWithOptions creates a new archive or transactionally rewrites an existing
// archive while replacing entries supplied by sources.
func AddWithOptions(archive string, sources []string, options AddOptions) (result Result, err error) {
	archiveAbs, err := filepath.Abs(archive)
	if err != nil {
		return result, err
	}
	inputs, roots, err := collectInputs(sources, archiveAbs, options.Recursive, options.Excludes)
	if err != nil {
		return result, err
	}
	defer closeInputRoots(roots)
	if len(inputs) == 0 {
		return result, errors.New("no files to process")
	}
	level := -1
	if options.LevelDefined {
		level = options.Level
	}
	directories, groups, err := planInputGroups(inputs, executableFiltersEnabled(options, level))
	if err != nil {
		return result, err
	}
	output, err := security.CreateOutput(archiveAbs)
	if err != nil {
		return result, err
	}
	defer output.Cleanup()
	exists := output.Existed()

	var old *sevenzip.ReadCloser
	if exists {
		old, err = openReader(archiveAbs, options.Password)
		if err != nil {
			return result, err
		}
		defer func() {
			if old != nil {
				_ = old.Close()
			}
		}()
	}

	temp := output.File()

	zw, err := newWriter(temp, writerOptions{
		solid:            options.Solid,
		password:         options.Password,
		headerEncryption: options.HeaderEncryption,
		level:            level,
		method:           options.Method,
	})
	if err != nil {
		return result, fmt.Errorf("create 7z writer: %w", err)
	}
	defer func() {
		_ = zw.Close()
	}()

	replacements := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		replacements[collisionKey(input.name)] = struct{}{}
	}
	var budget security.Budget
	if old != nil {
		for _, file := range old.File {
			clean, cleanErr := cleanArchiveName(file.Name)
			if cleanErr != nil {
				return result, fmt.Errorf("preserve existing entry: %w", cleanErr)
			}
			if _, replaced := replacements[collisionKey(clean)]; replaced {
				continue
			}
			if err := checkFileCompressionRatio(file); err != nil {
				return result, err
			}
			if err := budget.AddEntry(clean, file.UncompressedSize); err != nil {
				return result, err
			}
			header := writerFileHeader{
				Name:       clean,
				Modified:   file.Modified,
				Attributes: file.Attributes,
				IsDir:      file.FileInfo().IsDir(),
			}
			if header.IsDir {
				if addErr := zw.addDirectory(header); addErr != nil {
					return result, fmt.Errorf("preserve directory %q: %w", clean, addErr)
				}
				continue
			}
			if file.Mode().Type() != 0 {
				return result, fmt.Errorf("updating archives containing special entry %q is not supported", clean)
			}
			dst, createErr := zw.create(header)
			if createErr != nil {
				return result, fmt.Errorf("preserve %q: %w", clean, createErr)
			}
			_, checksum, copyErr := readFile(file, dst, &budget)
			closeErr := dst.Close()
			if copyErr != nil {
				return result, fmt.Errorf("preserve %q: %w", clean, copyErr)
			}
			if closeErr != nil {
				return result, fmt.Errorf("finish preserved entry %q: %w", clean, closeErr)
			}
			if file.CRC32 != 0 && checksum != file.CRC32 {
				return result, fmt.Errorf("preserve %q: CRC mismatch", clean)
			}
		}
	}

	for _, input := range directories {
		if err := budget.AddEntry(input.name, uint64(max(input.info.Size(), 0))); err != nil {
			return result, err
		}
		header := writerFileHeader{
			Name:       input.name,
			Modified:   input.info.ModTime(),
			Attributes: fileAttributes(input.info.Mode()),
			IsDir:      input.info.IsDir(),
		}
		if addErr := zw.addDirectory(header); addErr != nil {
			return result, fmt.Errorf("add directory %q: %w", input.name, addErr)
		}
	}

	for _, group := range groups {
		if options.Solid {
			if beginErr := zw.beginFolder(group.filter, group.expectedSize, true); beginErr != nil {
				return result, fmt.Errorf("start executable filter group: %w", beginErr)
			}
		}
		for _, input := range group.files {
			if err := budget.AddEntry(input.name, uint64(max(input.info.Size(), 0))); err != nil {
				return result, err
			}
			if input.info.IsDir() {
				header := writerFileHeader{
					Name:       input.name,
					Modified:   input.info.ModTime(),
					Attributes: fileAttributes(input.info.Mode()),
					IsDir:      true,
				}
				if addErr := zw.addDirectory(header); addErr != nil {
					return result, fmt.Errorf("add directory %q: %w", input.name, addErr)
				}
				continue
			}
			if !options.Solid {
				if beginErr := zw.beginFolder(group.filter, uint64(max(input.info.Size(), 0)), true); beginErr != nil {
					return result, fmt.Errorf("start executable filter for %q: %w", input.name, beginErr)
				}
			}
			header := writerFileHeader{
				Name:       input.name,
				Modified:   input.info.ModTime(),
				Attributes: fileAttributes(input.info.Mode()),
			}
			dst, createErr := zw.create(header)
			if createErr != nil {
				return result, fmt.Errorf("add %q: %w", input.name, createErr)
			}
			src, openErr := input.open()
			if openErr != nil {
				_ = dst.Close()
				return result, fmt.Errorf("open %q: %w", input.path, openErr)
			}
			n, copyErr := budget.Copy(dst, src, input.name)
			closeSrcErr := src.Close()
			closeDstErr := dst.Close()
			if copyErr != nil {
				return result, fmt.Errorf("add %q: %w", input.name, copyErr)
			}
			if closeSrcErr != nil {
				return result, fmt.Errorf("close %q: %w", input.path, closeSrcErr)
			}
			if closeDstErr != nil {
				return result, fmt.Errorf("finish %q: %w", input.name, closeDstErr)
			}
			result.Files++
			result.Bytes += uint64(n)
		}
	}
	if err = zw.Close(); err != nil {
		return result, fmt.Errorf("finish archive: %w", err)
	}
	if err = temp.Sync(); err != nil {
		return result, fmt.Errorf("sync archive: %w", err)
	}
	if err = output.CloseFile(); err != nil {
		return result, fmt.Errorf("close archive: %w", err)
	}
	if old != nil {
		if closeErr := old.Close(); closeErr != nil {
			return result, fmt.Errorf("close existing archive: %w", closeErr)
		}
		old = nil
	}
	if err = output.Publish(); err != nil {
		return result, fmt.Errorf("publish archive: %w", err)
	}
	return result, nil
}

// List returns metadata for selected entries.
func List(archive, password string, patterns []string) ([]Entry, error) {
	zr, err := openReader(archive, password)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	compressedSize, err := zr.CompressedSize()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(zr.File))
	var budget security.Budget
	budget.SetCompressedBytes(compressedSize)
	for fileIndex, file := range zr.File {
		selected, matchErr := matches(file.Name, patterns)
		if matchErr != nil {
			return nil, matchErr
		}
		if !selected {
			continue
		}
		if err := checkFileCompressionRatio(file); err != nil {
			return nil, err
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize); err != nil {
			return nil, err
		}
		packedSize, packedSizeDefined := file.PackedSize()
		if fileIndex == 0 && !packedSizeDefined {
			packedSizeDefined = true
		}
		entries = append(entries, Entry{
			Name:              file.Name,
			Size:              file.UncompressedSize,
			PackedSize:        packedSize,
			PackedSizeDefined: packedSizeDefined,
			Modified:          file.Modified,
			Mode:              file.Mode(),
			Attributes:        file.Attributes,
			CRC32:             file.CRC32,
		})
	}
	return entries, nil
}

// Test reads selected streams completely and validates per-file CRC values.
func Test(archive, password string, patterns []string) (Result, error) {
	zr, err := openReader(archive, password)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	compressedSize, err := zr.CompressedSize()
	if err != nil {
		return Result{}, err
	}

	var result Result
	var budget security.Budget
	budget.SetCompressedBytes(compressedSize)
	for _, file := range zr.File {
		selected, matchErr := matches(file.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if err := checkFileCompressionRatio(file); err != nil {
			return result, err
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize); err != nil {
			return result, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		n, checksum, readErr := readFile(file, io.Discard, &budget)
		if readErr != nil {
			return result, fmt.Errorf("test %q: %w", file.Name, readErr)
		}
		if file.CRC32 != 0 && checksum != file.CRC32 {
			return result, fmt.Errorf("test %q: CRC mismatch: got %08X, want %08X", file.Name, checksum, file.CRC32)
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

// WriteContents decodes selected regular-file streams to dst in archive order.
func WriteContents(archive, password string, patterns []string, dst io.Writer) (Result, error) {
	zr, err := openReader(archive, password)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	compressedSize, err := zr.CompressedSize()
	if err != nil {
		return Result{}, err
	}

	var result Result
	var budget security.Budget
	budget.SetCompressedBytes(compressedSize)
	for _, file := range zr.File {
		selected, matchErr := matches(file.Name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if err := checkFileCompressionRatio(file); err != nil {
			return result, err
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize); err != nil {
			return result, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		n, checksum, readErr := readFile(file, dst, &budget)
		if readErr != nil {
			return result, fmt.Errorf("write %q: %w", file.Name, readErr)
		}
		if file.CRC32 != 0 && checksum != file.CRC32 {
			return result, fmt.Errorf("write %q: CRC mismatch: got %08X, want %08X", file.Name, checksum, file.CRC32)
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

// Extract safely writes selected regular files and directories.
func Extract(archive string, opts ExtractOptions) (Result, error) {
	output := opts.OutputDir
	if output == "" {
		output = "."
	}
	root, err := security.OpenExtractionRoot(output)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	parents := security.NewParentCache(root, opts.Publication == PublicationAtomic)
	defer parents.Close()

	zr, err := openReader(archive, opts.Password)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	compressedSize, err := zr.CompressedSize()
	if err != nil {
		return Result{}, err
	}

	seen := make(map[string]string)
	var result Result
	var budget security.Budget
	budget.SetCompressedBytes(compressedSize)
	for _, file := range zr.File {
		selected, matchErr := matches(file.Name, opts.Patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		if err := checkFileCompressionRatio(file); err != nil {
			return result, err
		}
		_, relative, pathErr := destination(".", file.Name, opts.Flatten)
		if pathErr != nil {
			return result, pathErr
		}
		if err := budget.AddEntry(file.Name, file.UncompressedSize); err != nil {
			return result, err
		}
		mode := file.Mode()
		if mode.IsDir() && opts.Flatten {
			continue
		}
		key := collisionKey(relative)
		if previous, ok := seen[key]; ok {
			return result, fmt.Errorf("archive entries %q and %q map to the same output path", previous, file.Name)
		}
		seen[key] = file.Name

		if mode.IsDir() {
			_, err := parents.Directory(relative, 0o755)
			if err != nil {
				return result, fmt.Errorf("create directory %q: %w", relative, err)
			}
			continue
		}
		if mode.Type() != 0 {
			return result, fmt.Errorf("refusing unsupported special entry %q (%s)", file.Name, mode.Type())
		}
		parent, err := parents.Directory(filepath.Dir(relative), 0o755)
		if err != nil {
			return result, fmt.Errorf("create parent for %q: %w", relative, err)
		}
		target := filepath.Base(relative)
		var existing fs.FileInfo
		var statErr error
		direct := false
		var tempName string
		var temp *os.File
		var tempErr error
		if opts.Publication == PublicationDirect {
			tempName = target
			temp, tempErr = parent.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if tempErr == nil {
				direct = true
			} else if !errors.Is(tempErr, fs.ErrExist) {
				return result, fmt.Errorf("create %q: %w", relative, tempErr)
			}
		}
		if !direct {
			existing, statErr = parent.Lstat(target)
			if statErr == nil {
				if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
					return result, fmt.Errorf("refusing to replace non-regular path %q", relative)
				}
				switch opts.Overwrite {
				case OverwriteSkip:
					continue
				case OverwriteAll:
				default:
					return result, fmt.Errorf("output file %q already exists; use -y, -aoa, or -aos", relative)
				}
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				return result, statErr
			}
			tempName, temp, tempErr = parent.CreateTemp()
		}
		if tempErr != nil {
			return result, fmt.Errorf("create %q: %w", relative, tempErr)
		}
		n, checksum, readErr := readFile(file, temp, &budget)
		if readErr == nil && file.CRC32 != 0 && checksum != file.CRC32 {
			readErr = fmt.Errorf("CRC mismatch: got %08X, want %08X", checksum, file.CRC32)
		}
		if readErr == nil {
			readErr = security.ApplySafeFileMode(temp, mode)
		}
		if readErr == nil && !direct {
			readErr = temp.Sync()
		}
		closeErr := temp.Close()
		if readErr != nil {
			_ = parent.Remove(tempName)
			return result, fmt.Errorf("extract %q: %w", file.Name, readErr)
		}
		if closeErr != nil {
			_ = parent.Remove(tempName)
			return result, fmt.Errorf("close %q: %w", relative, closeErr)
		}
		if direct {
			result.Files++
			result.Bytes += uint64(n)
			continue
		}
		if opts.Overwrite == OverwriteAll {
			if renameErr := parent.Rename(tempName, target); renameErr != nil {
				_ = parent.Remove(tempName)
				return result, fmt.Errorf("publish %q: %w", relative, renameErr)
			}
		} else if linkErr := parent.Link(tempName, target); linkErr != nil {
			_ = parent.Remove(tempName)
			if opts.Overwrite == OverwriteSkip && errors.Is(linkErr, fs.ErrExist) {
				continue
			}
			if errors.Is(linkErr, fs.ErrExist) {
				return result, fmt.Errorf("output file %q already exists; use -y, -aoa, or -aos", relative)
			}
			return result, fmt.Errorf("publish %q without replacement: %w", relative, linkErr)
		} else if removeErr := parent.Remove(tempName); removeErr != nil {
			return result, removeErr
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

func openReader(archive, password string) (*sevenzip.ReadCloser, error) {
	var (
		reader *sevenzip.ReadCloser
		err    error
	)
	if password == "" {
		reader, err = sevenzip.OpenReader(archive)
	} else {
		reader, err = sevenzip.OpenReaderWithPassword(archive, password)
	}
	if err != nil {
		return nil, fmt.Errorf("open archive %q: %w", archive, err)
	}
	return reader, nil
}

func readFile(file *sevenzip.File, dst io.Writer, budget *security.Budget) (int64, uint32, error) {
	src, err := file.Open()
	if err != nil {
		return 0, 0, err
	}
	reader := &checksumReader{reader: src}
	n, copyErr := budget.Copy(dst, reader, file.Name)
	closeErr := src.Close()
	if copyErr != nil {
		return n, reader.checksum, copyErr
	}
	if closeErr != nil {
		return n, reader.checksum, closeErr
	}
	return n, reader.checksum, nil
}

func checkFileCompressionRatio(file *sevenzip.File) error {
	packed, defined := file.PackedSize()
	if !defined {
		return nil
	}
	return security.CheckCompressionRatio(file.Name, file.UncompressedSize, packed)
}

type checksumReader struct {
	reader   io.Reader
	checksum uint32
}

const copyBufferSize = 64 << 10

//nolint:gochecknoglobals
var copyBufferPool = sync.Pool{New: func() any {
	buffer := make([]byte, copyBufferSize)
	return &buffer
}}

func (r *checksumReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.checksum = crc32.Update(r.checksum, crc32.IEEETable, p[:n])
	}
	return n, err
}

func (r *checksumReader) WriteTo(dst io.Writer) (written int64, err error) {
	buffer := copyBufferPool.Get().(*[]byte)
	defer copyBufferPool.Put(buffer)

	for {
		read, readErr := r.Read(*buffer)
		if read > 0 {
			n, writeErr := dst.Write((*buffer)[:read])
			written += int64(n)
			if writeErr != nil {
				return written, writeErr
			}
			if n != read {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func collectInputs(sources []string, archiveAbs string, recursive bool, excludes []string) ([]inputFile, []*security.Root, error) {
	inputs := make(map[string]inputFile)
	rootCache := make(map[string]*security.Root)
	var roots []*security.Root
	closeError := func(err error) ([]inputFile, []*security.Root, error) {
		closeInputRoots(roots)
		return nil, nil, err
	}
	for _, source := range sources {
		matches, err := expandSource(source, recursive)
		if err != nil {
			return closeError(err)
		}
		for _, matched := range matches {
			if err := collectInput(matched, archiveAbs, inputs, rootCache, &roots); err != nil {
				return closeError(err)
			}
		}
	}
	result := make([]inputFile, 0, len(inputs))
	excludePatterns := BuildPatterns(nil, excludes, recursive)
	for _, input := range inputs {
		selected, err := matches(input.name, excludePatterns)
		if err != nil {
			return closeError(err)
		}
		if !selected {
			continue
		}
		result = append(result, input)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, roots, nil
}

func closeInputRoots(roots []*security.Root) {
	for _, root := range roots {
		_ = root.Close()
	}
}

func expandSource(source string, recursive bool) ([]string, error) {
	if !strings.ContainsAny(source, "*?") {
		return []string{source}, nil
	}
	return wildcardSourceMatches(source, recursive)
}

func wildcardSourceMatches(pattern string, recursive bool) ([]string, error) {
	absPattern, err := filepath.Abs(pattern)
	if err != nil {
		return nil, err
	}
	base := absPattern
	for strings.ContainsAny(base, "*?") {
		parent := filepath.Dir(base)
		if parent == base {
			break
		}
		base = parent
	}
	relPattern, err := filepath.Rel(base, absPattern)
	if err != nil {
		return nil, err
	}
	var matches []string
	err = filepath.WalkDir(base, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(base, name)
		if relErr != nil {
			return relErr
		}
		matched := wildcardPathMatch(filepath.ToSlash(relPattern), filepath.ToSlash(rel), recursive)
		if matched {
			if len(matches) >= security.MaxArchiveEntries {
				return fmt.Errorf("input wildcard matches more than %d paths", security.MaxArchiveEntries)
			}
			matches = append(matches, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no input files match %q", pattern)
	}
	return matches, nil
}

func wildcardPathMatch(pattern, name string, recursive bool) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	nameParts := strings.Split(strings.Trim(name, "/"), "/")
	if recursive {
		if len(patternParts) > len(nameParts) {
			return false
		}
		nameParts = nameParts[len(nameParts)-len(patternParts):]
	}
	if len(patternParts) != len(nameParts) {
		return false
	}
	for i := range patternParts {
		if !wildcardMatch(patternParts[i], nameParts[i]) {
			return false
		}
	}
	return true
}

func collectInput(source, archiveAbs string, inputs map[string]inputFile, rootCache map[string]*security.Root, roots *[]*security.Root) error {
	abs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic-link input %q is not supported yet", source)
	}
	baseName := inputBaseName(source, abs)
	if !info.IsDir() {
		root, err := cachedInputRoot(filepath.Dir(abs), rootCache, roots)
		if err != nil {
			return fmt.Errorf("open input parent %q: %w", filepath.Dir(abs), err)
		}
		anchored, err := root.Lstat(filepath.Base(abs))
		if err != nil || !os.SameFile(info, anchored) {
			if err != nil {
				return err
			}
			return fmt.Errorf("input %q changed while it was opened", source)
		}
		return addInput(abs, baseName, anchored, archiveAbs, inputs, root, filepath.Base(abs))
	}
	root, err := cachedInputRoot(abs, rootCache, roots)
	if err != nil {
		return fmt.Errorf("open input directory %q: %w", source, err)
	}
	anchored, err := root.Lstat(".")
	if err != nil || !os.SameFile(info, anchored) {
		if err != nil {
			return err
		}
		return fmt.Errorf("input directory %q changed while it was opened", source)
	}
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic-link input %q is not supported yet", name)
		}
		archiveName := baseName
		if name != "." {
			archiveName = path.Join(baseName, name)
		}
		if archiveName == "." {
			return nil
		}
		display := abs
		if name != "." {
			display = filepath.Join(abs, filepath.FromSlash(name))
		}
		return addInput(display, archiveName, entryInfo, archiveAbs, inputs, root, name)
	})
}

func cachedInputRoot(name string, cache map[string]*security.Root, roots *[]*security.Root) (*security.Root, error) {
	key := filepath.Clean(name)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if root := cache[key]; root != nil {
		return root, nil
	}
	root, err := security.OpenExistingRoot(name)
	if err != nil {
		return nil, err
	}
	cache[key] = root
	*roots = append(*roots, root)
	return root, nil
}

func addInput(name, archiveName string, info fs.FileInfo, archiveAbs string, inputs map[string]inputFile, root *security.Root, relative string) error {
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("special input %q (%s) is not supported", name, info.Mode().Type())
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if samePath(abs, archiveAbs) {
		return nil
	}
	clean, err := cleanArchiveName(archiveName)
	if err != nil {
		return err
	}
	key := collisionKey(clean)
	previous, exists := inputs[key]
	if exists && !samePath(previous.path, abs) {
		return fmt.Errorf("inputs %q and %q have the same archive name %q", previous.path, abs, clean)
	}
	if !exists && len(inputs) >= security.MaxArchiveEntries {
		return fmt.Errorf("input contains more than %d entries", security.MaxArchiveEntries)
	}
	inputs[key] = inputFile{path: abs, name: clean, info: info, root: root, relative: relative}
	return nil
}

func inputBaseName(source, abs string) string {
	if filepath.IsAbs(source) {
		return filepath.Base(abs)
	}
	clean := filepath.Clean(source)
	for clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		clean = filepath.Base(clean)
	}
	clean = strings.TrimPrefix(clean, "."+string(filepath.Separator))
	return filepath.ToSlash(clean)
}

func fileAttributes(mode fs.FileMode) uint32 {
	typeBits := uint32(0x8000)
	dosBits := uint32(0x20)
	if mode.IsDir() {
		typeBits = 0x4000
		dosBits = 0x10
	}
	unixMode := typeBits | uint32(mode.Perm())
	return unixMode<<16 | dosBits
}

func matches(name string, patterns []string) (bool, error) {
	const excludePatternPrefix = "\x00X"
	var includes, excludes []string
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, excludePatternPrefix) {
			excludes = append(excludes, strings.TrimPrefix(pattern, excludePatternPrefix))
		} else {
			includes = append(includes, pattern)
		}
	}
	included := len(includes) == 0
	for _, pattern := range includes {
		if matchArchivePattern(name, pattern) {
			included = true
			break
		}
	}
	if !included {
		return false, nil
	}
	for _, pattern := range excludes {
		if matchArchivePattern(name, pattern) {
			return false, nil
		}
	}
	return true, nil
}

func matchArchivePattern(name, pattern string) bool {
	const recursivePatternPrefix = "\x00R"
	recursive := strings.HasPrefix(pattern, recursivePatternPrefix)
	pattern = strings.TrimPrefix(pattern, recursivePatternPrefix)
	name = strings.ReplaceAll(name, "\\", "/")
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	candidateName := strings.TrimSuffix(name, "/")
	candidatePattern := strings.TrimSuffix(pattern, "/")
	if !strings.ContainsAny(candidatePattern, "*?") &&
		(equalFileName(candidateName, candidatePattern) || hasFileNamePrefix(candidateName, candidatePattern+"/")) {
		return true
	}
	if candidatePattern == "*" {
		return true
	}
	return wildcardPathMatch(candidatePattern, candidateName, recursive)
}

// BuildPatterns encodes include, exclude, and recursion rules for archive operations.
func BuildPatterns(includes, excludes []string, recursive bool) []string {
	const (
		excludePatternPrefix   = "\x00X"
		recursivePatternPrefix = "\x00R"
	)
	patterns := make([]string, 0, len(includes)+len(excludes))
	decorate := func(pattern string) string {
		pattern = strings.ReplaceAll(pattern, "\\", "/")
		if recursive && strings.ContainsAny(pattern, "*?") && pattern != "*" {
			return recursivePatternPrefix + strings.TrimPrefix(pattern, "./")
		}
		return pattern
	}
	for _, pattern := range includes {
		patterns = append(patterns, decorate(pattern))
	}
	for _, pattern := range excludes {
		patterns = append(patterns, excludePatternPrefix+decorate(pattern))
	}
	return patterns
}

func wildcardMatch(mask, name string) bool {
	maskRunes := []rune(mask)
	nameRunes := []rune(name)
	memo := make(map[[2]int]bool)
	seen := make(map[[2]int]bool)
	var match func(int, int) bool
	match = func(mi, ni int) bool {
		key := [2]int{mi, ni}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var result bool
		switch {
		case mi == len(maskRunes):
			result = ni == len(nameRunes)
		case maskRunes[mi] == '*':
			result = match(mi+1, ni) || ni < len(nameRunes) && match(mi, ni+1)
		case ni < len(nameRunes) && (maskRunes[mi] == '?' || equalFileRune(maskRunes[mi], nameRunes[ni])):
			result = match(mi+1, ni+1)
		}
		memo[key] = result
		return result
	}
	return match(0, 0)
}

func equalFileRune(a, b rune) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(string(a), string(b))
	}
	return a == b
}

func equalFileName(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func hasFileNamePrefix(name, prefix string) bool {
	if len(name) < len(prefix) {
		return false
	}
	return equalFileName(name[:len(prefix)], prefix)
}

// Matches applies archive wildcard and directory-prefix selection.
func Matches(name string, patterns []string) (bool, error) {
	return matches(name, patterns)
}

func collisionKey(name string) string {
	name = filepath.Clean(name)
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

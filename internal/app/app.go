// Package app coordinates CLI parsing, archive operations, and exit codes.
package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inpadi/7zip/internal/archivefmt"
	"github.com/inpadi/7zip/internal/cli"
	"github.com/inpadi/7zip/internal/security"
)

const (
	ExitSuccess    = 0
	ExitWarning    = 1
	ExitFatalError = 2
	ExitUserError  = 7
	ExitMemory     = 8
	ExitUserBreak  = 255
)

const (
	version      = "26.02-go.3"
	versionDate  = "2026-06-25"
	publisher    = "inpadi ApS"
	supportEmail = "support@inpadi.com"
)

// Run executes one command and returns a 7-Zip-compatible process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWithIO(args, strings.NewReader(""), stdout, stderr)
}

// RunWithIO executes one command with explicit standard streams.
func RunWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	textOut := consoleTextWriter(stdout)
	textErr := consoleTextWriter(stderr)
	opts, err := cli.Parse(args)
	if errors.Is(err, cli.ErrHelp) {
		printBanner(textOut)
		printUsage(textOut)
		return ExitSuccess
	}
	if err != nil {
		printBanner(textErr)
		fmt.Fprintf(textErr, "ERROR: %v\n", err)
		return ExitUserError
	}
	if opts.Stdin {
		var cleanup func()
		opts, cleanup, err = materializeStdin(opts, stdin)
		if err != nil {
			if !opts.Stdout {
				printBanner(textErr)
			}
			return operationError(textErr, err)
		}
		defer cleanup()
	}
	if opts.Stdout {
		switch opts.Command {
		case cli.CommandAdd:
			return runAddToWriter(opts, stdout, textErr)
		case cli.CommandExtract, cli.CommandExtractFlat:
			_, err := archivefmt.WriteContents(opts.Archive, opts.Format, opts.Password, selectionPatterns(opts), stdout)
			if err != nil {
				return operationError(textErr, err)
			}
			return ExitSuccess
		}
	}
	if !opts.Bare {
		printBanner(textOut)
	}
	switch opts.Command {
	case cli.CommandAdd, cli.CommandUpdate:
		return runAdd(opts, textOut, textErr)
	case cli.CommandList:
		return runList(opts, textOut, textErr)
	case cli.CommandTest:
		return runTest(opts, textOut, textErr)
	case cli.CommandExtract, cli.CommandExtractFlat:
		return runExtract(opts, textOut, textErr)
	default:
		fmt.Fprintln(textErr, "ERROR: internal command dispatch failure")
		return ExitFatalError
	}
}

type crlfWriter struct {
	w      io.Writer
	lastCR bool
}

func consoleTextWriter(w io.Writer) io.Writer {
	if runtime.GOOS != "windows" {
		return w
	}
	return &crlfWriter{w: w}
}

func (w *crlfWriter) Write(p []byte) (int, error) {
	converted := make([]byte, 0, len(p)+8)
	previousCR := w.lastCR
	for _, b := range p {
		if b == '\n' && !previousCR {
			converted = append(converted, '\r')
		}
		converted = append(converted, b)
		previousCR = b == '\r'
	}
	w.lastCR = previousCR
	_, err := w.w.Write(converted)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func materializeStdin(opts cli.Options, stdin io.Reader) (cli.Options, func(), error) {
	root, err := os.MkdirTemp("", "7zip-stdin-*")
	if err != nil {
		return opts, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	rootHandle, err := security.OpenExistingRoot(root)
	if err != nil {
		cleanup()
		return opts, func() {}, err
	}
	defer rootHandle.Close()
	name := archiveSuffix(opts.Archive, opts.Format)
	if opts.Command == cli.CommandAdd || opts.Command == cli.CommandUpdate {
		name = opts.StdinName
		if name == "" {
			name = "stdin"
		}
		name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
		if name == "." || name == "" {
			cleanup()
			return opts, func() {}, errors.New("invalid stdin entry name")
		}
	} else {
		name = "archive" + name
	}
	target := filepath.Join(root, name)
	file, err := rootHandle.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return opts, func() {}, err
	}
	limit := security.MaxArchiveBytes
	if opts.Command == cli.CommandAdd || opts.Command == cli.CommandUpdate {
		limit = security.MaxFileBytes
	}
	limited := &io.LimitedReader{R: stdin, N: limit + 1}
	n, copyErr := io.Copy(file, limited)
	if copyErr == nil && n > limit {
		copyErr = fmt.Errorf("stdin exceeds the %d-byte input limit", limit)
	}
	closeErr := file.Close()
	if copyErr != nil {
		cleanup()
		return opts, func() {}, copyErr
	}
	if closeErr != nil {
		cleanup()
		return opts, func() {}, closeErr
	}
	if opts.Command == cli.CommandAdd || opts.Command == cli.CommandUpdate {
		opts.Files = append(opts.Files, target)
	} else {
		opts.Archive = target
	}
	return opts, cleanup, nil
}

func runAddToWriter(opts cli.Options, stdout, stderr io.Writer) int {
	root, err := os.MkdirTemp("", "7zip-stdout-*")
	if err != nil {
		return operationError(stderr, err)
	}
	defer os.RemoveAll(root)
	archive := filepath.Join(root, "archive"+archiveSuffix(opts.Archive, opts.Format))
	if _, err := archivefmt.Add(archive, addFiles(opts), addOptions(opts)); err != nil {
		return operationError(stderr, err)
	}
	file, _, err := security.OpenRegularFile(archive)
	if err != nil {
		return operationError(stderr, err)
	}
	_, copyErr := io.Copy(stdout, file)
	closeErr := file.Close()
	if copyErr != nil {
		return operationError(stderr, copyErr)
	}
	if closeErr != nil {
		return operationError(stderr, closeErr)
	}
	return ExitSuccess
}

func archiveSuffix(archive, format string) string {
	lower := strings.ToLower(archive)
	for _, suffix := range []string{".tar.zstd", ".tar.bz2", ".tar.zst", ".tar.gz", ".tar.xz", ".zstd", ".7z", ".zip", ".tar", ".tgz", ".tbz2", ".txz", ".tzst", ".gzip", ".bzip2", ".gz", ".bz2", ".xz", ".zst"} {
		if strings.HasSuffix(lower, suffix) {
			return suffix
		}
	}
	switch strings.ToLower(format) {
	case "7z":
		return ".7z"
	case "zip":
		return ".zip"
	case "tar":
		return ".tar"
	case "gzip", "gz":
		return ".gz"
	case "bzip2", "bz2":
		return ".bz2"
	case "xz":
		return ".xz"
	case "zstd", "zst":
		return ".zst"
	default:
		return ".archive"
	}
}

func runAdd(opts cli.Options, stdout, stderr io.Writer) int {
	action := "Creating"
	if opts.Command == cli.CommandUpdate {
		action = "Updating"
	}
	fmt.Fprintf(stdout, "%s archive: %s\n\n", action, opts.Archive)
	result, err := archivefmt.Add(opts.Archive, addFiles(opts), addOptions(opts))
	if err != nil {
		return operationError(stderr, err)
	}
	fmt.Fprintf(stdout, "Files read from disk: %d\nArchive size input: %d bytes\n\nEverything is Ok\n", result.Files, result.Bytes)
	return ExitSuccess
}

func addOptions(opts cli.Options) archivefmt.AddOptions {
	return archivefmt.AddOptions{
		Format:           opts.Format,
		Solid:            opts.Solid,
		SolidDefined:     opts.SolidDefined,
		Password:         opts.Password,
		HeaderEncryption: opts.HeaderEncryption,
		Level:            opts.CompressionLevel,
		LevelDefined:     opts.CompressionLevelDefined,
		Method:           opts.Method,
		Recursive:        opts.Recursive,
		Excludes:         opts.ExcludePatterns,
	}
}

func addFiles(opts cli.Options) []string {
	files := append([]string(nil), opts.Files...)
	return append(files, opts.IncludePatterns...)
}

func runList(opts cli.Options, stdout, stderr io.Writer) int {
	if !opts.Bare {
		fmt.Fprintf(stdout, "Listing archive: %s\n\n", opts.Archive)
	}
	entries, err := archivefmt.List(opts.Archive, opts.Format, opts.Password, selectionPatterns(opts))
	if err != nil {
		return operationError(stderr, err)
	}
	if opts.Technical {
		printTechnicalList(stdout, entries)
		return ExitSuccess
	}
	printList(stdout, entries, opts.Bare)
	return ExitSuccess
}

func printList(w io.Writer, entries []archivefmt.Entry, bare bool) {
	const separator = "------------------- ----- ------------ ------------  ------------------------"
	if !bare {
		fmt.Fprintln(w, "   Date      Time    Attr         Size   Compressed  Name")
		fmt.Fprintln(w, separator)
	}
	var total, packed uint64
	files, folders := 0, 0
	latest := ""
	for _, entry := range entries {
		dateTime := ""
		if !entry.Modified.IsZero() {
			dateTime = entry.Modified.Local().Format("2006-01-02 15:04:05")
			if dateTime > latest {
				latest = dateTime
			}
		}
		packedText := ""
		if entry.PackedSizeDefined {
			packedText = fmt.Sprintf("%d", entry.PackedSize)
			packed += entry.PackedSize
		}
		fmt.Fprintf(w, "%19s %5s %12d %12s  %s\n",
			dateTime, attributeText(entry), entry.Size, packedText, displayEntryName(entry))
		total += entry.Size
		if entry.Mode.IsDir() {
			folders++
		} else {
			files++
		}
	}
	if bare {
		return
	}
	fmt.Fprintln(w, separator)
	packedText := ""
	if hasPackedSizes(entries) {
		packedText = fmt.Sprintf("%d", packed)
	}
	fmt.Fprintf(w, "%19s %5s %12d %12s  %s\n", latest, "", total, packedText, countText(files, folders))
}

func hasPackedSizes(entries []archivefmt.Entry) bool {
	for _, entry := range entries {
		if entry.PackedSizeDefined {
			return true
		}
	}
	return false
}

func countText(files, folders int) string {
	fileWord, folderWord := "files", "folders"
	if files == 1 {
		fileWord = "file"
	}
	if folders == 1 {
		folderWord = "folder"
	}
	if folders == 0 {
		return fmt.Sprintf("%d %s", files, fileWord)
	}
	return fmt.Sprintf("%d %s, %d %s", files, fileWord, folders, folderWord)
}

func displayEntryName(entry archivefmt.Entry) string {
	name := strings.TrimSuffix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(name, "/", "\\")
	}
	return name
}

func runTest(opts cli.Options, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "Testing archive: %s\n\n", opts.Archive)
	result, err := archivefmt.Test(opts.Archive, opts.Format, opts.Password, selectionPatterns(opts))
	if err != nil {
		return operationError(stderr, err)
	}
	fmt.Fprintf(stdout, "Files: %d\nSize: %d\n\nEverything is Ok\n", result.Files, result.Bytes)
	return ExitSuccess
}

func runExtract(opts cli.Options, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "Extracting archive: %s\n\n", opts.Archive)
	result, err := archivefmt.Extract(opts.Archive, archivefmt.ExtractOptions{
		Format:    opts.Format,
		OutputDir: opts.OutputDir,
		Patterns:  selectionPatterns(opts),
		Password:  opts.Password,
		Flatten:   opts.Command == cli.CommandExtractFlat,
		Overwrite: overwritePolicy(opts.Overwrite),
	})
	if err != nil {
		return operationError(stderr, err)
	}
	fmt.Fprintf(stdout, "Files: %d\nSize: %d\n\nEverything is Ok\n", result.Files, result.Bytes)
	return ExitSuccess
}

func selectionPatterns(opts cli.Options) []string {
	includes := append([]string(nil), opts.Files...)
	includes = append(includes, opts.IncludePatterns...)
	return archivefmt.BuildPatterns(includes, opts.ExcludePatterns, opts.Recursive)
}

func printTechnicalList(w io.Writer, entries []archivefmt.Entry) {
	for _, entry := range entries {
		fmt.Fprintf(w, "Path = %s\n", entry.Name)
		fmt.Fprintf(w, "Size = %d\n", entry.Size)
		if entry.PackedSizeDefined {
			fmt.Fprintf(w, "Packed Size = %d\n", entry.PackedSize)
		} else {
			fmt.Fprintln(w, "Packed Size =")
		}
		if !entry.Modified.IsZero() {
			fmt.Fprintf(w, "Modified = %s\n", entry.Modified.Local().Format("2006-01-02 15:04:05"))
		}
		fmt.Fprintf(w, "Attributes = %s\n", attributeText(entry))
		fmt.Fprintf(w, "CRC = %08X\n\n", entry.CRC32)
	}
}

func attributeText(entry archivefmt.Entry) string {
	attributes := []byte(".....")
	if entry.Mode.IsDir() {
		attributes[0] = 'D'
	}
	if entry.Attributes&0x01 != 0 || entry.Mode.Perm()&0o200 == 0 {
		attributes[1] = 'R'
	}
	if entry.Attributes&0x02 != 0 {
		attributes[2] = 'H'
	}
	if entry.Attributes&0x04 != 0 {
		attributes[3] = 'S'
	}
	if entry.Attributes&0x20 != 0 || !entry.Mode.IsDir() {
		attributes[4] = 'A'
	}
	return string(attributes)
}

func overwritePolicy(mode cli.OverwriteMode) archivefmt.OverwritePolicy {
	switch mode {
	case cli.OverwriteAll:
		return archivefmt.OverwriteAll
	case cli.OverwriteSkip:
		return archivefmt.OverwriteSkip
	default:
		return archivefmt.OverwriteNever
	}
}

func operationError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "ERROR: %v\n", err)
	return ExitFatalError
}

func printBanner(w io.Writer) {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "x86"
	}
	fmt.Fprintf(w, "\n7-Zip Go %s (%s) : 7-Zip compatibility target 26.02 : %s\n", version, arch, versionDate)
	fmt.Fprintf(w, "This build is from %s - %s for support / questions\n\n", publisher, supportEmail)
}

func printUsage(w io.Writer) {
	usage := `Usage: 7zip <command> [<switches>...] <archive_name> [<file_names>...]

<Commands>
  a : Add files to a new or existing 7z archive
  e : Extract files without directory names
  l : List archive contents
  t : Test archive integrity
  u : Update files in an archive
  x : Extract files with full paths

<Supported switches>
  --         : Stop switch parsing
  -aoa       : Overwrite all existing files
  -aos       : Skip existing files
  -ba        : Disable the banner; list only entry rows with l
  -bb[0-3]   : Set output log level (accepted for compatibility)
  -bd        : Disable progress indicator
  -o{dir}    : Set output directory
  -mhe=on|off: Encrypt or expose 7z archive headers
  -m0={name} : Set compression method
  -mx[=0-9]  : Set compression level
  -ms=on|off : Enable or disable solid 7z blocks
  -p{value}  : Set creation or extraction password
  -r[-|0]    : Recurse wildcard matches
  -scs{name} : Set UTF-8 or UTF-16 list-file encoding
  -si{name}  : Read an archive or one input file from stdin
  -slt       : Show technical information for list command
  -so        : Write an archive or extracted file data to stdout
  -t{type}   : Set type: 7z, zip, tar, gzip, bzip2, xz, zstd, iso, wim, vhd, vhdx
  -i!{mask}  : Include names matching a wildcard
  -x!{mask}  : Exclude names matching a wildcard
  -y         : Assume yes and overwrite existing files
`
	fmt.Fprint(w, strings.TrimSpace(usage), "\n")
}

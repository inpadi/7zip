// Package cli parses the portable subset of the 7-Zip command line.
package cli

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/inpadi/7zip/internal/security"
)

// Command identifies an archive operation.
type Command byte

const (
	CommandAdd         Command = 'a'
	CommandExtract     Command = 'x'
	CommandExtractFlat Command = 'e'
	CommandList        Command = 'l'
	CommandTest        Command = 't'
	CommandUpdate      Command = 'u'
)

// OverwriteMode controls extraction when the destination already exists.
type OverwriteMode uint8

const (
	OverwriteNever OverwriteMode = iota
	OverwriteAll
	OverwriteSkip
)

// ExtractionPublication controls how newly extracted files become visible.
type ExtractionPublication uint8

const (
	PublicationDirect ExtractionPublication = iota
	PublicationAtomic
)

// Options is the supported command-line surface.
type Options struct {
	Command                 Command
	Archive                 string
	Files                   []string
	OutputDir               string
	Password                string
	Overwrite               OverwriteMode
	Publication             ExtractionPublication
	PublicationDefined      bool
	Technical               bool
	Bare                    bool
	Solid                   bool
	SolidDefined            bool
	HeaderEncryption        bool
	HeaderEncryptionDefined bool
	DisableFilters          bool
	FiltersDefined          bool
	Format                  string
	Stdin                   bool
	StdinName               string
	Stdout                  bool
	ListCharset             string
	CompressionLevel        int
	CompressionLevelDefined bool
	Method                  string
	Recursive               bool
	IncludePatterns         []string
	ExcludePatterns         []string
}

// ErrHelp indicates that usage should be printed without an error.
var ErrHelp = errors.New("help requested")

// Parse parses 7-Zip-style arguments. Unsupported switches are rejected so
// callers never get a successful command with silently different semantics.
func Parse(args []string) (Options, error) {
	opts := Options{Solid: true, CompressionLevel: -1, ListCharset: "utf-8"}
	if len(args) == 0 {
		return opts, ErrHelp
	}

	command := strings.ToLower(args[0])
	switch command {
	case "a":
		opts.Command = CommandAdd
	case "e":
		opts.Command = CommandExtractFlat
	case "l":
		opts.Command = CommandList
	case "t":
		opts.Command = CommandTest
	case "u":
		opts.Command = CommandUpdate
	case "x":
		opts.Command = CommandExtract
	case "-h", "--help", "/?":
		return opts, ErrHelp
	default:
		return opts, fmt.Errorf("unsupported command %q", args[0])
	}

	parseSwitches := true
	for _, arg := range args[1:] {
		if parseSwitches && arg == "--" {
			parseSwitches = false
			continue
		}
		if parseSwitches && isSwitch(arg) {
			if err := parseSwitch(&opts, arg); err != nil {
				return Options{}, err
			}
			continue
		}
		if opts.Archive == "" {
			opts.Archive = arg
		} else {
			opts.Files = append(opts.Files, arg)
		}
	}

	if opts.Archive == "" && opts.Stdin && opts.Command != CommandAdd && opts.Command != CommandUpdate {
		opts.Archive = "-"
	}
	if opts.Archive == "" {
		return Options{}, errors.New("archive name is required")
	}
	files, err := expandListFiles(opts.Files, opts.ListCharset)
	if err != nil {
		return Options{}, err
	}
	opts.Files = files
	opts.IncludePatterns, err = expandListFiles(opts.IncludePatterns, opts.ListCharset)
	if err != nil {
		return Options{}, err
	}
	opts.ExcludePatterns, err = expandListFiles(opts.ExcludePatterns, opts.ListCharset)
	if err != nil {
		return Options{}, err
	}
	if (opts.Command == CommandAdd || opts.Command == CommandUpdate) && len(opts.Files) == 0 && len(opts.IncludePatterns) == 0 && !opts.Stdin {
		return Options{}, errors.New("at least one input path is required")
	}
	if opts.OutputDir != "" && opts.Command != CommandExtract && opts.Command != CommandExtractFlat {
		return Options{}, errors.New("-o is only supported for extract commands")
	}
	if opts.Technical && opts.Command != CommandList {
		return Options{}, errors.New("-slt is only supported for the list command")
	}
	if opts.SolidDefined && opts.Command != CommandAdd && opts.Command != CommandUpdate {
		return Options{}, errors.New("-ms is only supported for archive creation and updates")
	}
	if opts.HeaderEncryptionDefined && opts.Command != CommandAdd && opts.Command != CommandUpdate {
		return Options{}, errors.New("-mhe is only supported for archive creation and updates")
	}
	if opts.FiltersDefined && opts.Command != CommandAdd && opts.Command != CommandUpdate {
		return Options{}, errors.New("-mf is only supported for archive creation and updates")
	}
	if opts.PublicationDefined && opts.Command != CommandExtract && opts.Command != CommandExtractFlat {
		return Options{}, errors.New("-mep is only supported for extract commands")
	}
	if opts.HeaderEncryption && opts.Password == "" {
		return Options{}, errors.New("-mhe=on requires -p{password}")
	}
	if opts.Stdout && opts.Command != CommandAdd && opts.Command != CommandExtract && opts.Command != CommandExtractFlat {
		return Options{}, errors.New("-so is only supported for add and extract commands")
	}
	if opts.Stdin && opts.Command != CommandAdd && opts.Command != CommandUpdate && opts.Format == "" {
		return Options{}, errors.New("reading an archive from stdin requires -t{type}")
	}

	return opts, nil
}

func isSwitch(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	// A single dash can be a filename. Absolute POSIX paths also remain names.
	return arg != "-" && !strings.HasPrefix(arg, "-/")
}

func parseSwitch(opts *Options, arg string) error {
	s := strings.ToLower(strings.TrimPrefix(arg, "-"))
	switch {
	case s == "y" || s == "aoa":
		opts.Overwrite = OverwriteAll
	case s == "aos":
		opts.Overwrite = OverwriteSkip
	case s == "bd", s == "bb", s == "bb0", s == "bb1", s == "bb2", s == "bb3":
		// Accepted output-only switches do not change archive semantics.
	case s == "ba":
		opts.Bare = true
	case s == "so":
		opts.Stdout = true
	case s == "r" || s == "r0":
		opts.Recursive = true
	case s == "r-":
		opts.Recursive = false
	case strings.HasPrefix(s, "i!"):
		if len(arg) <= 3 {
			return errors.New("-i! requires a wildcard")
		}
		opts.IncludePatterns = append(opts.IncludePatterns, arg[3:])
	case strings.HasPrefix(s, "i@"):
		if len(arg) <= 3 {
			return errors.New("-i@ requires a list file")
		}
		opts.IncludePatterns = append(opts.IncludePatterns, arg[2:])
	case strings.HasPrefix(s, "x!"):
		if len(arg) <= 3 {
			return errors.New("-x! requires a wildcard")
		}
		opts.ExcludePatterns = append(opts.ExcludePatterns, arg[3:])
	case strings.HasPrefix(s, "x@"):
		if len(arg) <= 3 {
			return errors.New("-x@ requires a list file")
		}
		opts.ExcludePatterns = append(opts.ExcludePatterns, arg[2:])
	case strings.HasPrefix(s, "si"):
		opts.Stdin = true
		opts.StdinName = arg[3:]
	case strings.HasPrefix(s, "scs"):
		charset := strings.ToLower(strings.ReplaceAll(arg[4:], "_", "-"))
		switch charset {
		case "utf-8", "utf8":
			opts.ListCharset = "utf-8"
		case "utf-16le", "utf16le", "unicode":
			opts.ListCharset = "utf-16le"
		case "utf-16be", "utf16be":
			opts.ListCharset = "utf-16be"
		default:
			return fmt.Errorf("unsupported list-file charset %q", arg[4:])
		}
	case strings.HasPrefix(s, "mx"):
		value := strings.TrimPrefix(s, "mx")
		value = strings.TrimPrefix(value, "=")
		if value == "" {
			value = "9"
		}
		level, err := strconv.Atoi(value)
		if err != nil || level < 0 || level > 9 {
			return fmt.Errorf("unsupported compression level %q; use -mx=0 through -mx=9", value)
		}
		opts.CompressionLevel = level
		opts.CompressionLevelDefined = true
	case strings.HasPrefix(s, "m0="):
		method := strings.ToLower(arg[4:])
		switch method {
		case "copy", "store", "lzma", "lzma2", "deflate", "bzip2", "xz", "zstd":
			opts.Method = method
		default:
			return fmt.Errorf("unsupported compression method %q", arg[4:])
		}
	case strings.HasPrefix(s, "mep="):
		value := strings.TrimPrefix(s, "mep=")
		switch value {
		case "direct":
			opts.Publication = PublicationDirect
		case "atomic":
			opts.Publication = PublicationAtomic
		default:
			return fmt.Errorf("unsupported extraction publication mode %q; use direct or atomic", value)
		}
		opts.PublicationDefined = true
	case strings.HasPrefix(s, "mf="):
		value := strings.TrimPrefix(s, "mf=")
		switch value {
		case "on":
			opts.DisableFilters = false
		case "off":
			opts.DisableFilters = true
		default:
			return fmt.Errorf("unsupported executable filter mode %q; use -mf=on or -mf=off", value)
		}
		opts.FiltersDefined = true
	case s == "slt":
		opts.Technical = true
	case strings.HasPrefix(s, "ms="):
		value := strings.TrimPrefix(s, "ms=")
		switch value {
		case "on":
			opts.Solid = true
		case "off":
			opts.Solid = false
		default:
			return fmt.Errorf("unsupported solid mode %q; use -ms=on or -ms=off", value)
		}
		opts.SolidDefined = true
	case strings.HasPrefix(s, "mhe="):
		value := strings.TrimPrefix(s, "mhe=")
		switch value {
		case "on":
			opts.HeaderEncryption = true
		case "off":
			opts.HeaderEncryption = false
		default:
			return fmt.Errorf("unsupported header encryption mode %q; use -mhe=on or -mhe=off", value)
		}
		opts.HeaderEncryptionDefined = true
	case strings.HasPrefix(s, "o"):
		if len(arg) == 2 {
			return errors.New("-o requires a directory in the same argument")
		}
		opts.OutputDir = arg[2:]
	case strings.HasPrefix(s, "p"):
		if len(arg) == 2 {
			return errors.New("interactive password input is not implemented; use -p{password}")
		}
		opts.Password = arg[2:]
	case strings.HasPrefix(s, "t"):
		archiveType := strings.ToLower(arg[2:])
		switch archiveType {
		case "7z", "zip", "tar", "gzip", "gz", "bzip2", "bz2", "xz", "zstd", "zst", "iso", "wim", "vhd", "vhdx":
			opts.Format = archiveType
		default:
			return fmt.Errorf("unsupported archive type %q", arg[2:])
		}
	default:
		return fmt.Errorf("unsupported switch %q", arg)
	}
	return nil
}

func expandListFiles(values []string, charset string) ([]string, error) {
	var expanded []string
	for _, value := range values {
		if strings.HasPrefix(value, "@@") {
			if len(expanded) >= security.MaxArchiveEntries {
				return nil, fmt.Errorf("command contains more than %d path entries", security.MaxArchiveEntries)
			}
			expanded = append(expanded, value[1:])
			continue
		}
		if !strings.HasPrefix(value, "@") || len(value) == 1 {
			if len(expanded) >= security.MaxArchiveEntries {
				return nil, fmt.Errorf("command contains more than %d path entries", security.MaxArchiveEntries)
			}
			expanded = append(expanded, value)
			continue
		}
		file, _, err := security.OpenRegularFile(value[1:])
		if err != nil {
			return nil, fmt.Errorf("read list file %q: %w", value[1:], err)
		}
		content, readErr := io.ReadAll(io.LimitReader(file, security.MaxListFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read list file %q: %w", value[1:], readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close list file %q: %w", value[1:], closeErr)
		}
		if len(content) > security.MaxListFileBytes {
			return nil, fmt.Errorf("list file %q exceeds the %d-byte limit", value[1:], security.MaxListFileBytes)
		}
		text, err := decodeListFile(content, charset)
		if err != nil {
			return nil, fmt.Errorf("decode list file %q: %w", value[1:], err)
		}
		for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line != "" {
				if len(expanded) >= security.MaxArchiveEntries {
					return nil, fmt.Errorf("list files contain more than %d path entries", security.MaxArchiveEntries)
				}
				expanded = append(expanded, line)
			}
		}
	}
	return expanded, nil
}

func decodeListFile(content []byte, charset string) (string, error) {
	if len(content) >= 3 && string(content[:3]) == "\xef\xbb\xbf" {
		return string(content[3:]), nil
	}
	if len(content) >= 2 {
		switch {
		case content[0] == 0xff && content[1] == 0xfe:
			charset, content = "utf-16le", content[2:]
		case content[0] == 0xfe && content[1] == 0xff:
			charset, content = "utf-16be", content[2:]
		}
	}
	if charset == "utf-8" {
		return string(content), nil
	}
	if len(content)%2 != 0 {
		return "", errors.New("UTF-16 input has an odd byte length")
	}
	values := make([]uint16, len(content)/2)
	for i := range values {
		if charset == "utf-16be" {
			values[i] = binary.BigEndian.Uint16(content[i*2:])
		} else {
			values[i] = binary.LittleEndian.Uint16(content[i*2:])
		}
	}
	return string(utf16.Decode(values)), nil
}

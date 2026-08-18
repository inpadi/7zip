// Package archivefmt dispatches portable CLI operations by archive format.
package archivefmt

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/inpadi/7zip/internal/archive7z"
)

type Entry = archive7z.Entry
type Result = archive7z.Result
type OverwritePolicy = archive7z.OverwritePolicy

const (
	OverwriteNever = archive7z.OverwriteNever
	OverwriteAll   = archive7z.OverwriteAll
	OverwriteSkip  = archive7z.OverwriteSkip
)

type Format string

const (
	Format7z       Format = "7z"
	FormatZip      Format = "zip"
	FormatTar      Format = "tar"
	FormatTarGzip  Format = "tar.gzip"
	FormatTarBzip2 Format = "tar.bzip2"
	FormatTarXZ    Format = "tar.xz"
	FormatGzip     Format = "gzip"
	FormatBzip2    Format = "bzip2"
	FormatXZ       Format = "xz"
	FormatZstd     Format = "zstd"
	FormatTarZstd  Format = "tar.zstd"
	FormatISO      Format = "iso"
	FormatWIM      Format = "wim"
	FormatVHD      Format = "vhd"
	FormatVHDX     Format = "vhdx"
)

type AddOptions struct {
	Format           string
	Solid            bool
	SolidDefined     bool
	Password         string
	HeaderEncryption bool
	Level            int
	LevelDefined     bool
	Method           string
	Recursive        bool
	Excludes         []string
}

type ExtractOptions struct {
	Format    string
	OutputDir string
	Patterns  []string
	Password  string
	Flatten   bool
	Overwrite OverwritePolicy
}

func Resolve(explicit, archive string) (Format, error) {
	name := strings.ToLower(filepath.ToSlash(archive))
	if explicit != "" {
		switch strings.ToLower(explicit) {
		case "7z":
			return Format7z, nil
		case "zip":
			return FormatZip, nil
		case "tar":
			return FormatTar, nil
		case "gzip", "gz":
			return FormatGzip, nil
		case "bzip2", "bz2":
			return FormatBzip2, nil
		case "xz":
			return FormatXZ, nil
		case "zstd", "zst":
			return FormatZstd, nil
		case "iso", "udf":
			return FormatISO, nil
		case "wim":
			return FormatWIM, nil
		case "vhd":
			return FormatVHD, nil
		case "vhdx":
			return FormatVHDX, nil
		default:
			return "", fmt.Errorf("unsupported archive type %q", explicit)
		}
	}
	return formatFromName(name)
}

func formatFromName(name string) (Format, error) {
	switch {
	case strings.HasSuffix(name, ".7z"):
		return Format7z, nil
	case strings.HasSuffix(name, ".zip"):
		return FormatZip, nil
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return FormatTarGzip, nil
	case strings.HasSuffix(name, ".tar.bz2"), strings.HasSuffix(name, ".tbz2"):
		return FormatTarBzip2, nil
	case strings.HasSuffix(name, ".tar.xz"), strings.HasSuffix(name, ".txz"):
		return FormatTarXZ, nil
	case strings.HasSuffix(name, ".tar"):
		return FormatTar, nil
	case strings.HasSuffix(name, ".tar.zst"), strings.HasSuffix(name, ".tar.zstd"), strings.HasSuffix(name, ".tzst"):
		return FormatTarZstd, nil
	case strings.HasSuffix(name, ".gz"):
		return FormatGzip, nil
	case strings.HasSuffix(name, ".bz2"):
		return FormatBzip2, nil
	case strings.HasSuffix(name, ".xz"):
		return FormatXZ, nil
	case strings.HasSuffix(name, ".zst"), strings.HasSuffix(name, ".zstd"):
		return FormatZstd, nil
	case strings.HasSuffix(name, ".iso"), strings.HasSuffix(name, ".udf"):
		return FormatISO, nil
	case strings.HasSuffix(name, ".wim"):
		return FormatWIM, nil
	case strings.HasSuffix(name, ".vhd"):
		return FormatVHD, nil
	case strings.HasSuffix(name, ".vhdx"):
		return FormatVHDX, nil
	default:
		return "", errors.New("cannot determine archive type; use -t{type} or a supported extension")
	}
}

func Add(archive string, sources []string, options AddOptions) (Result, error) {
	format, err := Resolve(options.Format, archive)
	if err != nil {
		return Result{}, err
	}
	if format != Format7z && (options.Password != "" || options.HeaderEncryption) {
		return Result{}, fmt.Errorf("password encryption is not implemented for %s archives", format)
	}
	if format != Format7z && options.SolidDefined {
		return Result{}, fmt.Errorf("solid mode is only supported for 7z archives")
	}
	if err := validateCompression(format, options.Method); err != nil {
		return Result{}, err
	}
	level := -1
	if options.LevelDefined {
		level = options.Level
	}
	switch format {
	case FormatISO, FormatWIM, FormatVHD, FormatVHDX:
		return Result{}, fmt.Errorf("%s creation is not implemented; this format is currently read-only", format)
	case Format7z:
		return archive7z.AddWithOptions(archive, sources, archive7z.AddOptions{
			Solid:            options.Solid,
			Password:         options.Password,
			HeaderEncryption: options.HeaderEncryption,
			Level:            level,
			LevelDefined:     true,
			Method:           options.Method,
			Recursive:        options.Recursive,
			Excludes:         options.Excludes,
		})
	case FormatZip:
		return addZip(archive, sources, level, options.Method, options.Recursive, options.Excludes)
	case FormatGzip, FormatBzip2, FormatXZ, FormatZstd:
		return addStream(archive, sources, format, level, options.Recursive, options.Excludes)
	default:
		return addTar(archive, sources, format, level, options.Recursive, options.Excludes)
	}
}

func BuildPatterns(includes, excludes []string, recursive bool) []string {
	return archive7z.BuildPatterns(includes, excludes, recursive)
}

func validateCompression(format Format, method string) error {
	if method == "" {
		return nil
	}
	allowed := map[Format]map[string]bool{
		Format7z:      {"copy": true, "store": true, "lzma": true, "lzma2": true},
		FormatZip:     {"copy": true, "store": true, "deflate": true},
		FormatTar:     {"copy": true, "store": true},
		FormatTarGzip: {"deflate": true}, FormatGzip: {"deflate": true},
		FormatTarBzip2: {"bzip2": true}, FormatBzip2: {"bzip2": true},
		FormatTarXZ: {"xz": true, "lzma2": true}, FormatXZ: {"xz": true, "lzma2": true},
		FormatTarZstd: {"zstd": true}, FormatZstd: {"zstd": true},
	}
	if !allowed[format][method] {
		return fmt.Errorf("compression method %q is not supported for %s archives", method, format)
	}
	return nil
}

func List(archive, explicit, password string, patterns []string) ([]Entry, error) {
	format, err := resolveInput(explicit, archive)
	if err != nil {
		return nil, err
	}
	if format != Format7z && password != "" {
		return nil, fmt.Errorf("password decryption is not implemented for %s archives", format)
	}
	switch format {
	case Format7z:
		return archive7z.List(archive, password, patterns)
	case FormatZip:
		return listZip(archive, patterns)
	case FormatISO:
		return listISO(archive, patterns)
	case FormatWIM:
		return listWIM(archive, patterns)
	case FormatVHD, FormatVHDX:
		return listVirtualDisk(archive, patterns, format)
	case FormatGzip, FormatBzip2, FormatXZ, FormatZstd:
		return listStream(archive, patterns, format)
	default:
		return listTar(archive, patterns, format)
	}
}

func Test(archive, explicit, password string, patterns []string) (Result, error) {
	format, err := resolveInput(explicit, archive)
	if err != nil {
		return Result{}, err
	}
	if format != Format7z && password != "" {
		return Result{}, fmt.Errorf("password decryption is not implemented for %s archives", format)
	}
	switch format {
	case Format7z:
		return archive7z.Test(archive, password, patterns)
	case FormatZip:
		return testZip(archive, patterns)
	case FormatISO:
		return processISO(archive, patterns, io.Discard, nil)
	case FormatWIM:
		return processWIM(archive, patterns, io.Discard, nil)
	case FormatVHD, FormatVHDX:
		return processVirtualDisk(archive, patterns, format, io.Discard, nil)
	case FormatGzip, FormatBzip2, FormatXZ, FormatZstd:
		return testStream(archive, patterns, format)
	default:
		return testTar(archive, patterns, format)
	}
}

// WriteContents decodes selected regular files to dst in archive order.
func WriteContents(archive, explicit, password string, patterns []string, dst io.Writer) (Result, error) {
	format, err := resolveInput(explicit, archive)
	if err != nil {
		return Result{}, err
	}
	if format != Format7z && password != "" {
		return Result{}, fmt.Errorf("password decryption is not implemented for %s archives", format)
	}
	switch format {
	case Format7z:
		return archive7z.WriteContents(archive, password, patterns, dst)
	case FormatZip:
		return writeZip(archive, patterns, dst)
	case FormatISO:
		return processISO(archive, patterns, dst, nil)
	case FormatWIM:
		return processWIM(archive, patterns, dst, nil)
	case FormatVHD, FormatVHDX:
		return processVirtualDisk(archive, patterns, format, dst, nil)
	case FormatGzip, FormatBzip2, FormatXZ, FormatZstd:
		return writeStream(archive, patterns, format, dst)
	default:
		return writeTar(archive, patterns, format, dst)
	}
}

func Extract(archive string, options ExtractOptions) (Result, error) {
	format, err := resolveInput(options.Format, archive)
	if err != nil {
		return Result{}, err
	}
	if format != Format7z && options.Password != "" {
		return Result{}, fmt.Errorf("password decryption is not implemented for %s archives", format)
	}
	if format == Format7z {
		return archive7z.Extract(archive, archive7z.ExtractOptions{
			OutputDir: options.OutputDir,
			Patterns:  options.Patterns,
			Password:  options.Password,
			Flatten:   options.Flatten,
			Overwrite: options.Overwrite,
		})
	}
	if format == FormatZip {
		return extractZip(archive, options)
	}
	if format == FormatISO {
		return processISO(archive, options.Patterns, io.Discard, &options)
	}
	if format == FormatWIM {
		return processWIM(archive, options.Patterns, io.Discard, &options)
	}
	if format == FormatVHD || format == FormatVHDX {
		return processVirtualDisk(archive, options.Patterns, format, io.Discard, &options)
	}
	if isSingleStream(format) {
		return extractStream(archive, options, format)
	}
	return extractTar(archive, options, format)
}

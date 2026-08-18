package archivefmt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/carbon-os/diskiso/udf"
	"github.com/inpadi/7zip/internal/archive7z"
)

const isoSectorSize = 2048

type isoEntry struct {
	name     string
	source   string
	offset   int64
	size     int64
	mode     fs.FileMode
	modified time.Time
}

type isoArchive struct {
	file    *os.File
	udf     *udf.Volume
	entries []isoEntry
}

func openISO(name string) (*isoArchive, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	closeError := func(err error) (*isoArchive, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return closeError(err)
	}
	if hasUDF, probeErr := udf.Probe(file); probeErr == nil && hasUDF {
		volume, err := udf.NewVolume(file)
		if err != nil {
			return closeError(fmt.Errorf("open UDF filesystem: %w", err))
		}
		archive := &isoArchive{file: file, udf: volume}
		if err := archive.walkUDF(volume, "/", "", info.Size(), make(map[string]bool), 0); err != nil {
			return closeError(err)
		}
		return archive, nil
	}
	var primary, joliet []byte
	for sector := int64(16); sector < 16+256; sector++ {
		descriptor := make([]byte, isoSectorSize)
		if _, err := file.ReadAt(descriptor, sector*isoSectorSize); err != nil {
			return closeError(fmt.Errorf("read ISO volume descriptor: %w", err))
		}
		if string(descriptor[1:6]) != "CD001" || descriptor[6] != 1 {
			return closeError(errors.New("invalid ISO9660 volume descriptor"))
		}
		switch descriptor[0] {
		case 1:
			primary = append([]byte(nil), descriptor...)
		case 2:
			if string(descriptor[88:91]) == "%/E" || string(descriptor[88:91]) == "%/C" || string(descriptor[88:91]) == "%/@" {
				joliet = append([]byte(nil), descriptor...)
			}
		case 255:
			sector = 16 + 256
		}
	}
	descriptor := joliet
	unicodeNames := true
	if len(descriptor) == 0 {
		descriptor = primary
		unicodeNames = false
	}
	if len(descriptor) == 0 {
		return closeError(errors.New("ISO9660 primary volume descriptor is missing"))
	}
	root, err := parseISORecord(descriptor[156:], unicodeNames, "")
	if err != nil {
		return closeError(fmt.Errorf("parse ISO root directory: %w", err))
	}
	archive := &isoArchive{file: file}
	visited := make(map[[2]int64]bool)
	if err := archive.walk(root, "", unicodeNames, info.Size(), visited, 0); err != nil {
		return closeError(err)
	}
	return archive, nil
}

func (a *isoArchive) Close() error { return a.file.Close() }

func (a *isoArchive) open(item isoEntry) (io.ReadCloser, error) {
	if a.udf != nil {
		file, err := a.udf.Open(item.source)
		if err != nil {
			return nil, err
		}
		return file, nil
	}
	return io.NopCloser(io.NewSectionReader(a.file, item.offset, item.size)), nil
}

func (a *isoArchive) walkUDF(volume *udf.Volume, directory, prefix string, imageSize int64, visited map[string]bool, depth int) error {
	if depth > 128 {
		return errors.New("UDF directory nesting exceeds 128 levels")
	}
	if visited[directory] {
		return fmt.Errorf("UDF directory cycle at %q", directory)
	}
	visited[directory] = true
	defer delete(visited, directory)

	children, err := volume.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read UDF directory %q: %w", directory, err)
	}
	for _, child := range children {
		rawName := child.Name()
		if rawName == "" || rawName == "." || rawName == ".." || strings.ContainsAny(rawName, "/\\") {
			return fmt.Errorf("invalid UDF file name %q", rawName)
		}
		info, err := child.Info()
		if err != nil {
			return fmt.Errorf("read UDF metadata for %q: %w", rawName, err)
		}
		if info.Size() < 0 || info.Size() > imageSize {
			return fmt.Errorf("invalid UDF size for %q", rawName)
		}
		name, err := safeName(path.Join(prefix, rawName))
		if err != nil {
			return fmt.Errorf("unsafe UDF entry: %w", err)
		}
		mode := info.Mode()
		if mode.IsDir() {
			mode = fs.ModeDir | 0o755
			name = strings.TrimSuffix(name, "/") + "/"
		} else if mode.Type() != 0 {
			return fmt.Errorf("unsupported special UDF entry %q (%s)", name, mode.Type())
		} else {
			mode = 0o644
		}
		size := info.Size()
		if mode.IsDir() {
			size = 0
		}
		source := path.Join(directory, rawName)
		entry := isoEntry{
			name:     name,
			source:   source,
			size:     size,
			mode:     mode,
			modified: info.ModTime(),
		}
		a.entries = append(a.entries, entry)
		if len(a.entries) > 1_000_000 {
			return errors.New("UDF archive contains too many entries")
		}
		if mode.IsDir() {
			if err := a.walkUDF(volume, source, strings.TrimSuffix(name, "/"), imageSize, visited, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *isoArchive) walk(directory isoEntry, prefix string, unicodeNames bool, imageSize int64, visited map[[2]int64]bool, depth int) error {
	if depth > 128 {
		return errors.New("ISO directory nesting exceeds 128 levels")
	}
	key := [2]int64{directory.offset, directory.size}
	if visited[key] {
		return nil
	}
	visited[key] = true
	if directory.offset < 0 || directory.size < 0 || directory.offset > imageSize-directory.size {
		return errors.New("ISO directory extent is outside the image")
	}
	data := make([]byte, directory.size)
	if _, err := a.file.ReadAt(data, directory.offset); err != nil {
		return fmt.Errorf("read ISO directory: %w", err)
	}
	for offset := 0; offset < len(data); {
		length := int(data[offset])
		if length == 0 {
			offset = (offset/isoSectorSize + 1) * isoSectorSize
			continue
		}
		if offset+length > len(data) {
			return errors.New("truncated ISO directory record")
		}
		record, err := parseISORecord(data[offset:offset+length], unicodeNames, prefix)
		if err != nil {
			return err
		}
		offset += length
		if record.name == "" {
			continue
		}
		if record.offset < 0 || record.size < 0 || record.offset > imageSize-record.size {
			return fmt.Errorf("ISO extent for %q is outside the image", record.name)
		}
		if record.mode.IsDir() {
			record.name = strings.TrimSuffix(record.name, "/") + "/"
		}
		a.entries = append(a.entries, record)
		if record.mode.IsDir() {
			if err := a.walk(record, strings.TrimSuffix(record.name, "/"), unicodeNames, imageSize, visited, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseISORecord(record []byte, unicodeNames bool, prefix string) (isoEntry, error) {
	if len(record) < 34 || int(record[0]) > len(record) {
		return isoEntry{}, errors.New("invalid ISO directory record")
	}
	nameLength := int(record[32])
	if 33+nameLength > len(record) {
		return isoEntry{}, errors.New("truncated ISO file identifier")
	}
	rawName := record[33 : 33+nameLength]
	special := nameLength == 1 && (rawName[0] == 0 || rawName[0] == 1)
	clean := ""
	if !special {
		name := decodeISOName(rawName, unicodeNames)
		rrName, link, hasRRName := rockRidgeName(record, nameLength)
		if hasRRName {
			name = rrName
		}
		if link {
			return isoEntry{}, fmt.Errorf("ISO symbolic link %q is not supported", name)
		}
		name = strings.TrimSuffix(name, ";1")
		name = strings.TrimSuffix(name, ".")
		var err error
		clean, err = safeName(path.Join(prefix, name))
		if err != nil {
			return isoEntry{}, fmt.Errorf("unsafe ISO entry: %w", err)
		}
	}
	mode := fs.FileMode(0o444)
	if record[25]&0x02 != 0 {
		mode = fs.ModeDir | 0o555
	}
	extent := int64(binary.LittleEndian.Uint32(record[2:6])+uint32(record[1])) * isoSectorSize
	size := int64(binary.LittleEndian.Uint32(record[10:14]))
	return isoEntry{name: clean, offset: extent, size: size, mode: mode, modified: isoRecordTime(record[18:25])}, nil
}

func decodeISOName(value []byte, unicodeNames bool) string {
	if !unicodeNames {
		return string(value)
	}
	values := make([]uint16, len(value)/2)
	for i := range values {
		values[i] = binary.BigEndian.Uint16(value[i*2:])
	}
	return string(utf16.Decode(values))
}

func rockRidgeName(record []byte, nameLength int) (string, bool, bool) {
	offset := 33 + nameLength
	if nameLength%2 == 0 {
		offset++
	}
	var name strings.Builder
	found := false
	link := false
	for offset+4 <= len(record) {
		length := int(record[offset+2])
		if length < 4 || offset+length > len(record) {
			break
		}
		signature := string(record[offset : offset+2])
		if signature == "NM" && length >= 5 {
			flags := record[offset+4]
			if flags&0x06 == 0 {
				name.Write(record[offset+5 : offset+length])
				found = true
			}
		}
		if signature == "SL" {
			link = true
		}
		offset += length
	}
	return name.String(), link, found
}

func isoRecordTime(value []byte) time.Time {
	if len(value) != 7 || value[0] == 0 {
		return time.Time{}
	}
	year := int(value[0]) + 1900
	offset := int(int8(value[6])) * 15 * 60
	zone := time.FixedZone("ISO9660", offset)
	return time.Date(year, time.Month(value[1]), int(value[2]), int(value[3]), int(value[4]), int(value[5]), 0, zone)
}

func listISO(archive string, patterns []string) ([]Entry, error) {
	iso, err := openISO(archive)
	if err != nil {
		return nil, err
	}
	defer iso.Close()
	var entries []Entry
	for _, item := range iso.entries {
		selected, err := archive7z.Matches(item.name, patterns)
		if err != nil {
			return nil, err
		}
		if selected {
			entries = append(entries, Entry{Name: item.name, Size: uint64(item.size), Modified: item.modified, Mode: item.mode})
		}
	}
	return entries, nil
}

func processISO(archive string, patterns []string, dst io.Writer, extract *ExtractOptions) (Result, error) {
	iso, err := openISO(archive)
	if err != nil {
		return Result{}, err
	}
	defer iso.Close()
	root := ""
	seen := make(map[string]string)
	if extract != nil {
		root, err = extractionRoot(extract.OutputDir)
		if err != nil {
			return Result{}, err
		}
	}
	var result Result
	for _, item := range iso.entries {
		selected, matchErr := archive7z.Matches(item.name, patterns)
		if matchErr != nil {
			return result, matchErr
		}
		if !selected {
			continue
		}
		var reader io.ReadCloser
		if !item.mode.IsDir() {
			reader, err = iso.open(item)
			if err != nil {
				return result, fmt.Errorf("open ISO entry %q: %w", item.name, err)
			}
		}
		stream := io.Reader(strings.NewReader(""))
		if reader != nil {
			stream = reader
		}
		if extract != nil {
			n, wrote, extractErr := extractEntry(root, item.name, item.mode, item.modified, stream, *extract, seen)
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
				if n != item.size {
					return result, fmt.Errorf("extract %q: expected %d bytes, read %d", item.name, item.size, n)
				}
				result.Files++
				result.Bytes += uint64(n)
			}
			continue
		}
		if item.mode.IsDir() {
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
		if n != item.size {
			return result, fmt.Errorf("read %q: expected %d bytes, read %d", item.name, item.size, n)
		}
		result.Files++
		result.Bytes += uint64(n)
	}
	return result, nil
}

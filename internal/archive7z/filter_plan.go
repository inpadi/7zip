package archive7z

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/inpadi/7zip/internal/sevenzip"
)

const executableAnalysisSize = 16 << 10

type inputGroup struct {
	filter       sevenzip.BranchFilter
	expectedSize uint64
	files        []inputFile
}

func executableFiltersEnabled(options AddOptions, level int) bool {
	if options.DisableFilters || level == 0 {
		return false
	}
	method := strings.ToLower(options.Method)
	if method == "" {
		method = "lzma2"
	}
	return method == "lzma" || method == "lzma2"
}

func planInputGroups(inputs []inputFile, enabled bool) (directories []inputFile, groups []inputGroup, err error) {
	grouped := make(map[sevenzip.BranchFilter][]inputFile, 4)
	sizes := make(map[sevenzip.BranchFilter]uint64, 4)
	for _, input := range inputs {
		if input.info.IsDir() {
			directories = append(directories, input)
			continue
		}

		filter := sevenzip.BranchFilterNone
		if enabled && executableCandidate(input.name) {
			filter, err = classifyInputExecutable(input)
			if err != nil {
				return nil, nil, err
			}
		}
		size := uint64(max(input.info.Size(), 0))
		if sizes[filter] > math.MaxUint64-size {
			return nil, nil, fmt.Errorf("input group size overflows uint64")
		}
		grouped[filter] = append(grouped[filter], input)
		sizes[filter] += size
	}

	order := [...]sevenzip.BranchFilter{
		sevenzip.BranchFilterNone,
		sevenzip.BranchFilterARM64,
		sevenzip.BranchFilterBCJ,
		sevenzip.BranchFilterIA64,
	}
	for _, filter := range order {
		if len(grouped[filter]) == 0 {
			continue
		}
		groups = append(groups, inputGroup{
			filter:       filter,
			expectedSize: sizes[filter],
			files:        grouped[filter],
		})
	}
	// When every stream is plain, retain the input enumeration order exactly,
	// including directories. This keeps the no-filter archive surface stable.
	if len(groups) == 1 && groups[0].filter == sevenzip.BranchFilterNone && len(directories) > 0 {
		groups[0].files = append([]inputFile(nil), inputs...)
		directories = nil
	}
	return directories, groups, nil
}

func executableCandidate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".dll", ".exe", ".ocx", ".sfx", ".sys":
		return true
	default:
		return false
	}
}

func classifyInputExecutable(input inputFile) (sevenzip.BranchFilter, error) {
	file, err := input.open()
	if err != nil {
		return sevenzip.BranchFilterNone, fmt.Errorf("analyze %q: %w", input.path, err)
	}
	header, readErr := io.ReadAll(io.LimitReader(file, executableAnalysisSize))
	closeErr := file.Close()
	if readErr != nil {
		return sevenzip.BranchFilterNone, fmt.Errorf("analyze %q: %w", input.path, readErr)
	}
	if closeErr != nil {
		return sevenzip.BranchFilterNone, fmt.Errorf("close %q after analysis: %w", input.path, closeErr)
	}
	return classifyPE(header, input.info.Size()), nil
}

func classifyPE(header []byte, size int64) sevenzip.BranchFilter {
	if len(header) < 512 || header[0] != 'M' || header[1] != 'Z' {
		return sevenzip.BranchFilterNone
	}
	peOffsetValue := binary.LittleEndian.Uint32(header[0x3c:])
	if peOffsetValue >= 0x1000 || peOffsetValue&7 != 0 || peOffsetValue > uint32(len(header)-512) {
		return sevenzip.BranchFilterNone
	}
	peOffset := int(peOffsetValue)
	if binary.LittleEndian.Uint32(header[peOffset:]) != 0x00004550 {
		return sevenzip.BranchFilterNone
	}

	machine := binary.LittleEndian.Uint16(header[peOffset+4:])
	var filter sevenzip.BranchFilter
	switch machine {
	case 0x014c: // IMAGE_FILE_MACHINE_I386
		filter = sevenzip.BranchFilterBCJ
	case 0x8664: // IMAGE_FILE_MACHINE_AMD64, including ARM64EC containers
		if peHasSection(header, peOffset, ".a64xrm") {
			filter = sevenzip.BranchFilterARM64
		} else {
			filter = sevenzip.BranchFilterBCJ
		}
	case 0xaa64: // IMAGE_FILE_MACHINE_ARM64
		filter = sevenzip.BranchFilterARM64
	case 0xa641: // IMAGE_FILE_MACHINE_ARM64EC
		filter = sevenzip.BranchFilterARM64
	case 0xa64e: // IMAGE_FILE_MACHINE_ARM64X
		filter = sevenzip.BranchFilterARM64
	case 0x0200: // IMAGE_FILE_MACHINE_IA64
		filter = sevenzip.BranchFilterIA64
	default:
		return sevenzip.BranchFilterNone
	}

	switch filter {
	case sevenzip.BranchFilterARM64:
		if size < 0 || size&3 != 0 {
			return sevenzip.BranchFilterNone
		}
	case sevenzip.BranchFilterIA64:
		if size < 0 || size&15 != 0 {
			return sevenzip.BranchFilterNone
		}
	}
	return filter
}

func peHasSection(header []byte, peOffset int, name string) bool {
	numberOfSections := int(binary.LittleEndian.Uint16(header[peOffset+6:]))
	optionalHeaderSize := int(binary.LittleEndian.Uint16(header[peOffset+20:]))
	if numberOfSections > 64 || optionalHeaderSize > 0x1000 {
		return false
	}
	sectionOffset := peOffset + 24 + optionalHeaderSize
	for i := 0; i < numberOfSections; i++ {
		offset := sectionOffset + i*40
		if offset < 0 || offset > len(header)-40 {
			return false
		}
		sectionName := strings.TrimRight(string(header[offset:offset+8]), "\x00")
		if sectionName == name {
			return true
		}
	}
	return false
}

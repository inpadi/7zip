package vhdx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/inpadi/7zip/internal/security"
)

const VHDX_MAGIC = "vhdxfile"

const (
	ALIGNMENT = 64 * 1024
	MB        = 1024 * 1024

	PAYLOAD_BLOCK_NOT_PRESENT       = 0
	PAYLOAD_BLOCK_UNDEFINED         = 1
	PAYLOAD_BLOCK_ZERO              = 2
	PAYLOAD_BLOCK_UNMAPPED          = 3
	PAYLOAD_BLOCK_FULLY_PRESENT     = 6
	PAYLOAD_BLOCK_PARTIALLY_PRESENT = 7
)

var (
	BAT_REGION_GUID, _           = uuid.Parse("2DC27766-F623-4200-9D64-115E9BFD4A08")
	FILE_PARAMETERS_GUID, _      = uuid.Parse("CAA16737-FA36-4D43-B3B6-33F0AA44E76B")
	LOGICAL_SECTOR_SIZE_GUID, _  = uuid.Parse("8141BF1D-A96F-4709-BA47-F233A8FAAB5F")
	METADATA_REGION_GUID, _      = uuid.Parse("8B7CA206-4790-4B9A-B8FE-575F050F886E")
	PARENT_LOCATOR_GUID, _       = uuid.Parse("A8D35F2D-B30B-454D-ABF7-D3D84834AB0C")
	PHYSICAL_SECTOR_SIZE_GUID, _ = uuid.Parse("CDA348C7-445D-4471-9CC9-E9885251C556")
	VIRTUAL_DISK_ID_GUID, _      = uuid.Parse("BECA12AB-B2E6-4523-93EF-C309E000C746")
	VIRTUAL_DISK_SIZE_GUID, _    = uuid.Parse("2FA54224-CD1B-4876-B211-5DBED83BF4B8")
	VHDX_PARENT_LOCATOR_GUID, _  = uuid.Parse("B04AEFB7-D19E-4A81-B789-25B8E9445913")
)

type FileParameters struct {
	BlockSize           uint32
	LeaveBlockAllocated bool
	HasParent           bool
}

type FileIdentifier struct {
	Signature [8]byte
	Creator   [512]byte
}

type VirtualDiskID struct {
	VirtualDiskID [16]byte
}

type VHDX struct {
	fh io.ReadSeeker

	fileIdentifier  FileIdentifier
	header          Header
	headers         [2]Header
	regionTable     *RegionTable
	regionTables    [2]*RegionTable
	metadata        *MetadataTable
	size            uint64
	blockSize       uint32
	hasParent       bool
	sectorSize      uint32
	id              uuid.UUID
	parent          *VHDX
	bat             *BlockAllocationTable
	sectorsPerBlock int
	chunkRatio      int64
	fileSize        int64
}

type FileAccessorFn func(string) (io.ReadSeeker, error)

var FileAccessor FileAccessorFn

var ErrFileAccessorNotAvailable = errors.New("file accessor needed to access for parent and extents from file")

func NewVHDX(fh io.ReadSeeker) (*VHDX, error) {
	fileSize, err := fh.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if fileSize < 5*ALIGNMENT {
		return nil, errors.New("VHDX file is too small")
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	vhdx := &VHDX{fh: fh, fileSize: fileSize}

	if err := binary.Read(fh, binary.LittleEndian, &vhdx.fileIdentifier); err != nil {
		return nil, err
	}
	if !bytes.Equal(vhdx.fileIdentifier.Signature[:], []byte(VHDX_MAGIC)) {
		return nil, errors.New("invalid file identifier signature")
	}

	// Read headers
	var header1, header2 Header
	if err := readHeader(fh, &header1, 1*ALIGNMENT); err != nil {
		return nil, err
	}
	if err := readHeader(fh, &header2, 2*ALIGNMENT); err != nil {
		return nil, err
	}

	if header1.SequenceNumber > header2.SequenceNumber {
		vhdx.header = header1
	} else {
		vhdx.header = header2
	}
	vhdx.headers = [2]Header{header1, header2}

	if !bytes.Equal(vhdx.header.Signature[:], []byte("head")) {
		return nil, errors.New("invalid header signature")
	}

	// Read region tables
	regionTable1, err := NewRegionTable(fh, 3*ALIGNMENT)
	if err != nil {
		return nil, err
	}
	regionTable2, err := NewRegionTable(fh, 4*ALIGNMENT)
	if err != nil {
		return nil, err
	}

	vhdx.regionTable = regionTable1
	vhdx.regionTables = [2]*RegionTable{regionTable1, regionTable2}

	// Read metadata
	metadataEntry, ok := vhdx.regionTable.lookup[METADATA_REGION_GUID]
	if !ok {
		return nil, errors.New("missing required region: metadata")
	}
	if err := vhdx.validateRegion(metadataEntry); err != nil {
		return nil, fmt.Errorf("invalid metadata region: %w", err)
	}
	metadataTable, err := NewMetadataTable(fh, int64(metadataEntry.FileOffset), int64(metadataEntry.Length))
	if err != nil {
		return nil, err
	}
	vhdx.metadata = metadataTable

	// Set VHDX properties
	virtualSize, ok := vhdx.metadata.lookup[VIRTUAL_DISK_SIZE_GUID].(uint64)
	if !ok || virtualSize == 0 || virtualSize > math.MaxInt64 {
		return nil, errors.New("missing or invalid VHDX virtual disk size")
	}
	vhdx.size = virtualSize
	fileParameters, ok := vhdx.metadata.lookup[FILE_PARAMETERS_GUID].(FileParameters)
	if !ok || fileParameters.BlockSize < MB || fileParameters.BlockSize > 256*MB || fileParameters.BlockSize&(fileParameters.BlockSize-1) != 0 {
		return nil, errors.New("missing or invalid VHDX file parameters")
	}
	vhdx.blockSize = fileParameters.BlockSize
	vhdx.hasParent = fileParameters.HasParent
	sectorSize, ok := vhdx.metadata.lookup[LOGICAL_SECTOR_SIZE_GUID].(uint32)
	if !ok || (sectorSize != 512 && sectorSize != 4096) || fileParameters.BlockSize%sectorSize != 0 {
		return nil, errors.New("missing or invalid VHDX logical sector size")
	}
	vhdx.sectorSize = sectorSize
	id, ok := vhdx.metadata.lookup[VIRTUAL_DISK_ID_GUID].(uuid.UUID)
	if !ok {
		return nil, errors.New("missing VHDX virtual disk identifier")
	}
	vhdx.id = newUUIDFromBytesLE(id[:])
	vhdx.sectorsPerBlock = int(vhdx.blockSize / vhdx.sectorSize)
	vhdx.chunkRatio = ((int64(1) << 23) * int64(vhdx.sectorSize)) / int64(vhdx.blockSize)
	if vhdx.chunkRatio <= 0 {
		return nil, errors.New("invalid VHDX chunk ratio")
	}

	// Handle parent locator if exists
	if vhdx.hasParent {
		if FileAccessor == nil {
			return nil, ErrFileAccessorNotAvailable
		}
		parentLocatorEntry, ok := vhdx.metadata.lookup[PARENT_LOCATOR_GUID].(*ParentLocator)
		if !ok || parentLocatorEntry == nil {
			return nil, errors.New("missing VHDX parent locator")
		}
		if !bytes.Equal(parentLocatorEntry.typeID[:], VHDX_PARENT_LOCATOR_GUID[:]) {
			return nil, fmt.Errorf("unknown parent locator type: %v", parentLocatorEntry.typeID)
		}
		parent, err := openParent(parentLocatorEntry.entries)
		if err != nil {
			return nil, err
		}
		vhdx.parent = parent
	}

	// Read BAT
	batEntry, ok := vhdx.regionTable.lookup[BAT_REGION_GUID]
	if !ok {
		return nil, errors.New("missing required region: BAT")
	}
	if err := vhdx.validateRegion(batEntry); err != nil {
		return nil, fmt.Errorf("invalid BAT region: %w", err)
	}
	vhdx.bat, err = NewBlockAllocationTable(vhdx, int64(batEntry.FileOffset), int64(batEntry.Length))
	if err != nil {
		return nil, err
	}

	return vhdx, nil
}

func (v *VHDX) ReadSectors(sector int64, count int64) ([]byte, error) {
	if err := v.validateSectorRead(sector, count); err != nil {
		return nil, err
	}
	var sectorsRead bytes.Buffer

	for count > 0 {
		block, sectorInBlock := divmod(sector, int64(v.sectorsPerBlock))
		readCount := min64(count, int64(v.sectorsPerBlock)-sectorInBlock)
		readSize := readCount * int64(v.sectorSize)
		batEntry, err := v.bat.pb(block)
		if err != nil {
			return nil, err
		}

		switch batEntry.State {
		case PAYLOAD_BLOCK_NOT_PRESENT:
			if v.parent != nil {
				parentData, err := v.parent.ReadSectors(sector, readCount)
				if err != nil {
					return nil, err
				}
				sectorsRead.Write(parentData)
			} else {
				sectorsRead.Write(make([]byte, readSize))
			}
		case PAYLOAD_BLOCK_UNDEFINED, PAYLOAD_BLOCK_ZERO, PAYLOAD_BLOCK_UNMAPPED:
			sectorsRead.Write(bytes.Repeat([]byte{0x00}, int(readSize)))
		case PAYLOAD_BLOCK_FULLY_PRESENT:
			offset, err := v.dataOffset(batEntry.FileOffsetMb, sectorInBlock*int64(v.sectorSize), readSize)
			if err != nil {
				return nil, err
			}
			_, err = v.fh.Seek(offset, io.SeekStart)
			if err != nil {
				return nil, err
			}
			data := make([]byte, readSize)
			_, err = io.ReadFull(v.fh, data)
			if err != nil {
				return nil, err
			}
			sectorsRead.Write(data)
		case PAYLOAD_BLOCK_PARTIALLY_PRESENT:
			if v.parent == nil {
				return nil, errors.New("partially present VHDX block requires a parent disk")
			}
			sectorBitmapEntry, err := v.bat.sb(block)
			if err != nil {
				return nil, err
			}

			blockInChunk := block % v.chunkRatio
			sectorInChunk := (blockInChunk * int64(v.sectorsPerBlock)) + sectorInBlock
			byteIdx, bitIdx := divmod(sectorInChunk, 8)

			bitmapSize := (bitIdx + readCount + 7) / 8
			off, err := v.dataOffset(sectorBitmapEntry.FileOffsetMb, byteIdx, bitmapSize)
			if err != nil {
				return nil, err
			}
			if _, err := v.fh.Seek(off, io.SeekStart); err != nil {
				return nil, err
			}
			sectorBitmap := make([]byte, bitmapSize)
			if _, err := io.ReadFull(v.fh, sectorBitmap); err != nil {
				return nil, err
			}

			relativeSector := int64(0)
			err = partialRunIter(sectorBitmap, bitIdx, int64(readCount), func(run *PartialRun) error {
				if run.Type == 0 {
					parentData, err := v.parent.ReadSectors(sector+relativeSector, run.Count)
					if err != nil {
						return err
					}
					sectorsRead.Write(parentData)
				} else {
					sec := (sectorInBlock + relativeSector) * int64(v.sectorSize)
					length := run.Count * int64(v.sectorSize)
					boff, err := v.dataOffset(batEntry.FileOffsetMb, sec, length)
					if err != nil {
						return err
					}
					_, err = v.fh.Seek(boff, io.SeekStart)
					if err != nil {
						return err
					}
					data := make([]byte, length)
					_, err = io.ReadFull(v.fh, data)
					if err != nil {
						return err
					}
					sectorsRead.Write(data)
				}
				relativeSector += run.Count
				return nil
			})
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("invalid VHDX BAT state %d", batEntry.State)
		}

		sector += readCount
		count -= readCount
	}

	return sectorsRead.Bytes(), nil
}

func (v *VHDX) validateRegion(entry RegionTableEntry) error {
	if entry.FileOffset > math.MaxInt64 {
		return errors.New("region offset exceeds supported file size")
	}
	end := entry.FileOffset + uint64(entry.Length)
	if end < entry.FileOffset || end > uint64(v.fileSize) {
		return errors.New("region extends beyond the VHDX file")
	}
	return nil
}

func (v *VHDX) validateSectorRead(sector, count int64) error {
	if sector < 0 || count < 0 {
		return errors.New("negative VHDX sector range")
	}
	if count == 0 {
		return nil
	}
	maxSectors := (int64(v.size) + int64(v.sectorSize) - 1) / int64(v.sectorSize)
	if sector >= maxSectors || count > maxSectors-sector {
		return errors.New("VHDX sector range exceeds virtual disk")
	}
	if count > int64(security.MaxDecoderMemory)/int64(v.sectorSize) {
		return errors.New("VHDX sector read exceeds memory limit")
	}
	return nil
}

func (v *VHDX) dataOffset(fileOffsetMB uint64, relative, length int64) (int64, error) {
	if relative < 0 || length < 0 || fileOffsetMB > math.MaxInt64/MB {
		return 0, errors.New("invalid VHDX data extent")
	}
	base := int64(fileOffsetMB * MB)
	if relative > math.MaxInt64-base || length > math.MaxInt64-base-relative {
		return 0, errors.New("VHDX data extent overflows")
	}
	offset := base + relative
	if offset+length > v.fileSize {
		return 0, errors.New("VHDX data extent exceeds file size")
	}
	return offset, nil
}

func (v *VHDX) Size() uint64 {
	return v.size
}

func (v *VHDX) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("negative VHDX read offset")
	}
	if offset >= int64(v.size) {
		return 0, io.EOF
	}
	limited := false
	if int64(len(p)) > int64(v.size)-offset {
		p = p[:int64(v.size)-offset]
		limited = true
	}
	sector := offset / int64(v.sectorSize)
	offsetInSector := int(offset % int64(v.sectorSize))
	totalLength := len(p)
	readData := make([]byte, 0, totalLength)

	for totalLength > 0 {
		sectorCount := (totalLength + offsetInSector + int(v.sectorSize) - 1) / int(v.sectorSize)
		data, err := v.ReadSectors(sector, int64(sectorCount))
		if err != nil {
			return 0, err
		}
		if len(data) < offsetInSector {
			return 0, io.EOF
		}

		data = data[offsetInSector:]
		bytesToRead := min32(len(data), totalLength)

		readData = append(readData, data[:bytesToRead]...)
		totalLength -= bytesToRead
		sector += int64(sectorCount)
		offsetInSector = 0
	}

	copy(p, readData)
	if limited {
		return len(readData), io.EOF
	}
	return len(readData), nil
}

func openParent(locator map[string]string) (*VHDX, error) {
	fp := strings.ReplaceAll(locator["relative_path"], "\\", "/")
	fhp, err := FileAccessor(fp)
	if err == nil {
		return NewVHDX(fhp)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	fp = filepath.Join("/", strings.ReplaceAll(locator["absolute_win32_path"], "\\", "/"))
	fhp, err = FileAccessor(fp)
	if err != nil {
		return nil, err
	}

	return NewVHDX(fhp)
}

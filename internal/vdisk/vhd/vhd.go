package vhd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/inpadi/7zip/internal/security"
)

const VHD_MAGIC = "conectix"

type disk interface {
	ReadSectors(sector int64, count int) ([]byte, error)
}

type VHD struct {
	disk disk
	size int64
}

func NewVHD(fh io.ReadSeeker) (*VHD, error) {
	fileSize, err := fh.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if fileSize < SECTOR_SIZE {
		return nil, errors.New("VHD file is too small")
	}
	footer, err := readFooter(fh)
	if err != nil {
		return nil, err
	}

	var diskItem disk
	switch footer.DiskType {
	case 2:
		if footer.DataOffset != 0xFFFFFFFFFFFFFFFF {
			return nil, errors.New("invalid fixed VHD data offset")
		}
		if footer.CurrentSize > uint64(fileSize-SECTOR_SIZE) {
			return nil, errors.New("fixed VHD data exceeds file size")
		}
		diskItem = NewFixedDisk(fh, footer)
	case 3:
		diskItem, err = NewDynamicDisk(fh, footer, fileSize)
		if err != nil {
			return nil, err
		}
	case 4:
		return nil, errors.New("differencing VHD requires a parent disk and is not supported")
	default:
		return nil, fmt.Errorf("unsupported VHD disk type %d", footer.DiskType)
	}

	return &VHD{disk: diskItem, size: int64(footer.CurrentSize)}, nil
}

func (v *VHD) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("negative VHD read offset")
	}
	if offset >= v.size {
		return 0, io.EOF
	}
	if len(p) > int(security.MaxDecoderMemory) {
		return 0, errors.New("VHD read exceeds memory limit")
	}
	limited := false
	if int64(len(p)) > v.size-offset {
		p = p[:v.size-offset]
		limited = true
	}
	sector := offset / SECTOR_SIZE
	offsetInSector := int(offset % SECTOR_SIZE)
	totalLength := len(p)
	readData := make([]byte, 0, totalLength)

	for totalLength > 0 {
		sectorCount := (totalLength + offsetInSector + SECTOR_SIZE - 1) / SECTOR_SIZE
		data, err := v.disk.ReadSectors(sector, sectorCount)
		if err != nil {
			return 0, err
		}
		if len(data) < offsetInSector {
			return 0, io.EOF
		}

		data = data[offsetInSector:]
		bytesToRead := min(len(data), totalLength)

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

func (v *VHD) Size() int64 {
	return v.size
}

type FixedDisk struct {
	fh     io.ReadSeeker
	footer *Footer
}

func NewFixedDisk(fh io.ReadSeeker, footer *Footer) *FixedDisk {
	return &FixedDisk{fh: fh, footer: footer}
}

func (d *FixedDisk) ReadSectors(sector int64, count int) ([]byte, error) {
	if sector < 0 || count < 0 || count > int(security.MaxDecoderMemory)/SECTOR_SIZE {
		return nil, errors.New("invalid fixed VHD sector range")
	}
	maxSectors := int64((d.footer.CurrentSize + SECTOR_SIZE - 1) / SECTOR_SIZE)
	if sector > maxSectors || int64(count) > maxSectors-sector {
		return nil, errors.New("fixed VHD sector range exceeds virtual disk")
	}
	buf := make([]byte, count*SECTOR_SIZE)
	if _, err := d.fh.Seek(sector*SECTOR_SIZE, io.SeekStart); err != nil {
		return nil, err
	}
	_, err := io.ReadFull(d.fh, buf)
	return buf, err
}

type DynamicDisk struct {
	fh               io.ReadSeeker
	footer           *Footer
	header           *DynamicHeader
	bat              *BlockAllocationTable
	sectorsPerBlock  int
	sectorBitmapSize int
	fileSize         int64
}

func NewDynamicDisk(fh io.ReadSeeker, footer *Footer, fileSize int64) (*DynamicDisk, error) {
	d := &DynamicDisk{fh: fh, footer: footer, fileSize: fileSize}
	if footer.DataOffset > math.MaxInt64 || footer.DataOffset+1024 < footer.DataOffset || footer.DataOffset+1024 > uint64(fileSize) {
		return nil, errors.New("invalid dynamic VHD header offset")
	}
	if _, err := fh.Seek(int64(footer.DataOffset), io.SeekStart); err != nil {
		return nil, err
	}
	data := make([]byte, 1024)
	if _, err := io.ReadFull(fh, data); err != nil {
		return nil, err
	}
	if !bytes.Equal(data[:8], []byte("cxsparse")) {
		return nil, errors.New("invalid dynamic VHD header cookie")
	}
	want := binary.BigEndian.Uint32(data[36:40])
	clear(data[36:40])
	var sum uint32
	for _, value := range data {
		sum += uint32(value)
	}
	if ^sum != want {
		return nil, errors.New("invalid dynamic VHD header checksum")
	}
	header := &DynamicHeader{}
	if err := binary.Read(bytes.NewReader(data), binary.BigEndian, header); err != nil {
		return nil, err
	}
	if header.BlockSize < SECTOR_SIZE || header.BlockSize > security.MaxDecoderMemory || header.BlockSize&(header.BlockSize-1) != 0 || header.MaxTableEntries == 0 {
		return nil, errors.New("invalid dynamic VHD geometry")
	}
	batBytes := uint64(header.MaxTableEntries) * 4
	if header.TableOffset > math.MaxInt64 || header.TableOffset+batBytes < header.TableOffset || header.TableOffset+batBytes > uint64(fileSize) {
		return nil, errors.New("invalid dynamic VHD allocation table offset")
	}
	expectedEntries := (footer.CurrentSize + uint64(header.BlockSize) - 1) / uint64(header.BlockSize)
	if uint64(header.MaxTableEntries) < expectedEntries {
		return nil, errors.New("dynamic VHD allocation table is too small")
	}
	d.header = header
	d.bat = NewBlockAllocationTable(fh, int64(header.TableOffset), int64(header.MaxTableEntries))
	d.sectorsPerBlock = int(header.BlockSize) / SECTOR_SIZE
	d.sectorBitmapSize = ((d.sectorsPerBlock / 8) + SECTOR_SIZE - 1) / SECTOR_SIZE
	return d, nil
}

func (d *DynamicDisk) ReadSectors(sector int64, count int) ([]byte, error) {
	if sector < 0 || count < 0 || count > int(security.MaxDecoderMemory)/SECTOR_SIZE {
		return nil, errors.New("invalid dynamic VHD sector range")
	}
	maxSectors := int64((d.footer.CurrentSize + SECTOR_SIZE - 1) / SECTOR_SIZE)
	if sector > maxSectors || int64(count) > maxSectors-sector {
		return nil, errors.New("dynamic VHD sector range exceeds virtual disk")
	}
	var result bytes.Buffer
	for count > 0 {
		block, offset := sector/int64(d.sectorsPerBlock), sector%int64(d.sectorsPerBlock)
		blockRemaining := int64(d.sectorsPerBlock) - offset
		readCount := min(count, int(blockRemaining))
		sectorOffset, err := d.bat.Get(block)
		if err != nil {
			return nil, err
		}

		if sectorOffset == 0xFFFFFFFF {
			_, _ = result.Write(make([]byte, readCount*SECTOR_SIZE))
			sector += int64(readCount)
			count -= readCount
			continue
		}
		boff := int64(sectorOffset) + int64(d.sectorBitmapSize) + offset
		readSize := int64(readCount * SECTOR_SIZE)
		fileOffset := boff * SECTOR_SIZE
		if fileOffset < 0 || readSize > d.fileSize-fileOffset {
			return nil, errors.New("dynamic VHD block exceeds file size")
		}
		_, err = d.fh.Seek(fileOffset, io.SeekStart)
		if err != nil {
			return nil, err
		}

		buf := make([]byte, readCount*SECTOR_SIZE)
		_, err = io.ReadFull(d.fh, buf)
		if err != nil {
			return nil, err
		}

		_, err = result.Write(buf)
		if err != nil {
			return nil, err
		}

		sector += int64(readCount)
		count -= readCount
	}
	return result.Bytes(), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

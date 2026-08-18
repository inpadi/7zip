package vhd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
		diskItem = NewFixedDisk(fh, footer)
	case 3:
		diskItem, err = NewDynamicDisk(fh, footer)
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
	buf := make([]byte, count*SECTOR_SIZE)
	d.fh.Seek(int64(sector*SECTOR_SIZE), io.SeekStart)
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
}

func NewDynamicDisk(fh io.ReadSeeker, footer *Footer) (*DynamicDisk, error) {
	d := &DynamicDisk{fh: fh, footer: footer}
	if footer.DataOffset > uint64(^uint64(0)>>1) {
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
	if header.BlockSize < SECTOR_SIZE || header.BlockSize&(header.BlockSize-1) != 0 || header.MaxTableEntries == 0 {
		return nil, errors.New("invalid dynamic VHD geometry")
	}
	d.header = header
	d.bat = NewBlockAllocationTable(fh, int64(header.TableOffset), int64(header.MaxTableEntries))
	d.sectorsPerBlock = int(header.BlockSize) / SECTOR_SIZE
	d.sectorBitmapSize = ((d.sectorsPerBlock / 8) + SECTOR_SIZE - 1) / SECTOR_SIZE
	return d, nil
}

func (d *DynamicDisk) ReadSectors(sector int64, count int) ([]byte, error) {
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
		_, err = d.fh.Seek(int64(boff*SECTOR_SIZE), io.SeekStart)
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

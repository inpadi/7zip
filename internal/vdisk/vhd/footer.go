package vhd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	SECTOR_SIZE = 512
)

type Footer struct {
	Cookie             [8]byte
	Features           uint32
	Version            uint32
	DataOffset         uint64
	Timestamp          uint32
	CreatorApplication uint32
	CreatorVersion     uint32
	CreatorHostOS      uint32
	OriginalSize       uint64
	CurrentSize        uint64
	DiskGeometry       uint32
	DiskType           uint32
	Checksum           uint32
	UniqueID           [16]byte
	SavedState         byte
	Reserved           [426]byte
}

type ParentLocator struct {
	PlatformCode       uint32
	PlatformDataSpace  uint32
	PlatformDataLength uint32
	Reserved           uint32
	PlatformDataOffset uint64
}

type DynamicHeader struct {
	Cookie            [8]byte
	DataOffset        uint64
	TableOffset       uint64
	HeaderVersion     uint32
	MaxTableEntries   uint32
	BlockSize         uint32
	Checksum          uint32
	ParentUniqueID    [16]byte
	ParentTimestamp   uint32
	Reserved1         uint32
	ParentUnicodeName [512]byte
	ParentLocators    [8]ParentLocator
	Reserved2         [256]byte
}

func readFooter(fh io.ReadSeeker) (*Footer, error) {
	footer := &Footer{}
	fh.Seek(-SECTOR_SIZE, io.SeekEnd)
	if err := binary.Read(fh, binary.BigEndian, footer); err != nil {
		return nil, err
	}

	if footer.Features&0x00000002 == 0 {
		_, err := fh.Seek(-511, io.SeekEnd)
		if err != nil {
			return nil, err
		}

		if err := binary.Read(fh, binary.BigEndian, footer); err != nil {
			return nil, err
		}
	}
	if !bytes.Equal(footer.Cookie[:], []byte(VHD_MAGIC)) {
		return nil, errors.New("invalid VHD footer cookie")
	}
	data := make([]byte, SECTOR_SIZE)
	if _, err := fh.Seek(-SECTOR_SIZE, io.SeekEnd); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(fh, data); err != nil {
		return nil, err
	}
	want := binary.BigEndian.Uint32(data[64:68])
	clear(data[64:68])
	var sum uint32
	for _, value := range data {
		sum += uint32(value)
	}
	if ^sum != want {
		return nil, errors.New("invalid VHD footer checksum")
	}
	if footer.CurrentSize == 0 || footer.CurrentSize > uint64(^uint64(0)>>1) {
		return nil, errors.New("invalid VHD virtual size")
	}
	return footer, nil
}

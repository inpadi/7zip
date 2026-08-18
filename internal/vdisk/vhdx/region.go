package vhdx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"

	"github.com/google/uuid"
)

type RegionTable struct {
	fh      io.ReadSeeker
	offset  int64
	header  RegionTableHeader
	entries []RegionTableEntry
	lookup  map[uuid.UUID]RegionTableEntry
}

type RegionTableHeader struct {
	Signature  [4]byte
	Checksum   uint32
	EntryCount uint32
	Reserved   [4]byte
}

type RegionTableEntry struct {
	Guid       uuid.UUID
	FileOffset uint64
	Length     uint32
	Required   uint32
}

func NewRegionTable(fh io.ReadSeeker, offset int64) (*RegionTable, error) {
	regionTable := &RegionTable{fh: fh, offset: offset}

	_, err := fh.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, err
	}

	data := make([]byte, ALIGNMENT)
	if _, err := io.ReadFull(fh, data); err != nil {
		return nil, err
	}
	if !bytes.Equal(data[:4], []byte("regi")) {
		return nil, errors.New("invalid region table signature")
	}
	want := binary.LittleEndian.Uint32(data[4:8])
	clear(data[4:8])
	if crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)) != want {
		return nil, errors.New("invalid region table checksum")
	}
	err = binary.Read(bytes.NewReader(data), binary.LittleEndian, &regionTable.header)
	if err != nil {
		return nil, err
	}

	if regionTable.header.EntryCount > 2047 {
		return nil, errors.New("too many VHDX region entries")
	}

	entries := make([]RegionTableEntry, regionTable.header.EntryCount)
	err = binary.Read(bytes.NewReader(data[16:]), binary.LittleEndian, &entries)
	if err != nil {
		return nil, err
	}

	regionTable.lookup = make(map[uuid.UUID]RegionTableEntry)
	for _, entry := range entries {
		if entry.FileOffset%ALIGNMENT != 0 || entry.Length%ALIGNMENT != 0 || entry.FileOffset+uint64(entry.Length) < entry.FileOffset {
			return nil, errors.New("invalid VHDX region extent")
		}
		regionTable.lookup[newUUIDFromBytesLE(entry.Guid[:])] = entry
	}

	return regionTable, nil
}

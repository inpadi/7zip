package vhdx

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/google/uuid"
)

type ParentLocatorHeader struct {
	LocatorType   uuid.UUID
	Reserved      uint16
	KeyValueCount uint16
}
type ParentLocatorEntry struct {
	KeyOffset   uint32
	ValueOffset uint32
	KeyLength   uint16
	ValueLength uint16
}

type ParentLocator struct {
	fh      io.ReadSeeker
	offset  int64
	header  ParentLocatorHeader
	typeID  uuid.UUID
	entries map[string]string
}

func NewParentLocator(fh io.ReadSeeker, length int64) (*ParentLocator, error) {
	if length < 20 || length > 1<<20 {
		return nil, errors.New("invalid VHDX parent locator length")
	}
	offset, err := fh.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	header := ParentLocatorHeader{}
	if err := binary.Read(fh, binary.LittleEndian, &header); err != nil {
		return nil, err
	}
	if int64(header.KeyValueCount)*12 > length-20 {
		return nil, errors.New("VHDX parent locator entries exceed metadata item")
	}
	plEntries := make([]ParentLocatorEntry, header.KeyValueCount)
	if err := binary.Read(fh, binary.LittleEndian, &plEntries); err != nil {
		return nil, err
	}

	typeID := newUUIDFromBytesLE(header.LocatorType[:])
	entries := make(map[string]string)
	for _, ple := range plEntries {
		if ple.KeyLength%2 != 0 || ple.ValueLength%2 != 0 ||
			uint64(ple.KeyOffset)+uint64(ple.KeyLength) > uint64(length) ||
			uint64(ple.ValueOffset)+uint64(ple.ValueLength) > uint64(length) {
			return nil, errors.New("invalid VHDX parent locator entry")
		}
		key := make([]byte, ple.KeyLength)
		value := make([]byte, ple.ValueLength)
		if _, err := fh.Seek(offset+int64(ple.KeyOffset), 0); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(fh, key); err != nil {
			return nil, err
		}
		if _, err := fh.Seek(offset+int64(ple.ValueOffset), 0); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(fh, value); err != nil {
			return nil, err
		}
		entries[utf16ToString(key)] = utf16ToString(value)
	}

	return &ParentLocator{
		fh:      fh,
		offset:  offset,
		header:  header,
		typeID:  typeID,
		entries: entries,
	}, nil
}

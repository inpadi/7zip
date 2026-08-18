package vhdx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

type Header struct {
	Signature      [4]byte
	Checksum       uint32
	SequenceNumber uint64
	FileWriteGuid  [16]byte
	DataWriteGuid  [16]byte
	LogGuid        [16]byte
	LogVersion     uint16
	Version        uint16
	LogLength      uint32
	LogOffset      uint64
	Reserved       [4016]byte
}

func readHeader(fh io.ReadSeeker, header *Header, offset int64) error {
	if _, err := fh.Seek(offset, 0); err != nil {
		return err
	}
	data := make([]byte, 4096)
	if _, err := io.ReadFull(fh, data); err != nil {
		return err
	}
	if !bytes.Equal(data[:4], []byte("head")) {
		return errors.New("invalid VHDX header signature")
	}
	want := binary.LittleEndian.Uint32(data[4:8])
	clear(data[4:8])
	if crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)) != want {
		return errors.New("invalid VHDX header checksum")
	}
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, header); err != nil {
		return err
	}
	if header.Version != 1 || header.LogLength%(1<<20) != 0 || header.LogOffset%(1<<20) != 0 {
		return errors.New("invalid VHDX header fields")
	}
	return nil
}

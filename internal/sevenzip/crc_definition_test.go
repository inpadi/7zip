package sevenzip

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMixedFolderAndSubstreamCRCDefinitions(t *testing.T) {
	const (
		folderCRC    = uint32(0x11223344)
		substreamCRC = uint32(0x55667788)
	)
	var unpackBytes bytes.Buffer
	unpackBytes.WriteByte(idFolder)
	unpackBytes.WriteByte(2) // Two folders.
	unpackBytes.WriteByte(0) // Folder metadata is inline.
	for range 2 {
		unpackBytes.WriteByte(1) // One coder.
		unpackBytes.WriteByte(1) // One-byte method ID, one input and output.
		unpackBytes.WriteByte(0) // Copy method.
	}
	unpackBytes.WriteByte(idCodersUnpackSize)
	unpackBytes.WriteByte(11)
	unpackBytes.WriteByte(22)
	unpackBytes.WriteByte(idCRC)
	unpackBytes.WriteByte(0)    // CRCs use a definition bitmap.
	unpackBytes.WriteByte(0x80) // Only folder zero has a folder CRC.
	if err := binary.Write(&unpackBytes, binary.LittleEndian, folderCRC); err != nil {
		t.Fatal(err)
	}
	unpackBytes.WriteByte(idEnd)

	unpack, err := readUnpackInfo(&unpackBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpack.digestDefined) != 2 || !unpack.digestDefined[0] || unpack.digestDefined[1] {
		t.Fatalf("folder CRC definitions = %v, want [true false]", unpack.digestDefined)
	}

	var substreamBytes bytes.Buffer
	substreamBytes.WriteByte(idCRC)
	substreamBytes.WriteByte(1) // The one unknown CRC is defined.
	if err := binary.Write(&substreamBytes, binary.LittleEndian, substreamCRC); err != nil {
		t.Fatal(err)
	}
	substreamBytes.WriteByte(idEnd)
	substreams, err := readSubStreamsInfo(&substreamBytes, unpack)
	if err != nil {
		t.Fatal(err)
	}
	streams := &streamsInfo{unpackInfo: unpack, subStreamsInfo: substreams}

	for _, test := range []struct {
		file   int
		folder int
		size   uint64
		crc    uint32
	}{
		{file: 0, folder: 0, size: 11, crc: folderCRC},
		{file: 1, folder: 1, size: 22, crc: substreamCRC},
	} {
		folder, size, crc, err := streams.FileFolderAndSize(test.file)
		if err != nil {
			t.Fatal(err)
		}
		if folder != test.folder || size != test.size || crc != test.crc {
			t.Fatalf("file %d = {folder:%d size:%d crc:%08x}, want {folder:%d size:%d crc:%08x}",
				test.file, folder, size, crc, test.folder, test.size, test.crc)
		}
	}
}

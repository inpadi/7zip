package wim

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/inpadi/7zip/internal/security"
)

func TestEmptySecurityDescriptorTableWithZeroLength(t *testing.T) {
	var reader Reader
	descriptors, consumed, err := reader.readSecurityDescriptors(bytes.NewReader(make([]byte, securityblockDiskSize)))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("descriptors = %d, want 0", len(descriptors))
	}
	if consumed != securityblockDiskSize {
		t.Fatalf("consumed = %d, want %d", consumed, securityblockDiskSize)
	}
}

func TestSecurityDescriptorCountLimit(t *testing.T) {
	block := securityblockDisk{NumEntries: security.MaxArchiveEntries + 1}
	var encoded bytes.Buffer
	if err := binary.Write(&encoded, binary.LittleEndian, block); err != nil {
		t.Fatal(err)
	}
	if _, _, err := new(Reader).readSecurityDescriptors(&encoded); err == nil {
		t.Fatal("expected oversized security table to be rejected")
	}
}

func TestCompressedResourceExpandedSizeLimit(t *testing.T) {
	section := io.NewSectionReader(bytes.NewReader(nil), 0, 0)
	if _, err := newCompressedReader(section, security.MaxFileBytes+1, 0, compressionLZX); err == nil {
		t.Fatal("expected oversized WIM resource to be rejected")
	}
}

func TestCompressedReaderRejectsOversizedChunk(t *testing.T) {
	data := make([]byte, chunkSize+9)
	binary.LittleEndian.PutUint32(data[:4], uint32(chunkSize+4))
	section := io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data)))
	if _, err := newCompressedReader(section, 2*chunkSize, 0, compressionXPRESS); err == nil {
		t.Fatal("expected oversized compressed WIM chunk to be rejected")
	}
}

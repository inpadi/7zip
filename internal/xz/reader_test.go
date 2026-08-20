package xz

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReaderUnknownBlockSizes(t *testing.T) {
	payload := xzTestPayload(3 << 20)
	encoded := encodeXZTestStream(t, payload, 1<<20)

	reader, err := (ReaderConfig{DictCap: 1 << 20}).NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded XZ stream differs from input")
	}
}

func TestReaderRejectsBlockChecksumMismatch(t *testing.T) {
	payload := xzTestPayload(1 << 20)
	encoded, writer := encodeXZTestStreamWithWriter(t, payload, maxInt64)

	checksumOffset := HeaderLen + writer.bw.headerLen + int(writer.bw.compressedSize())
	checksumOffset += padLen(writer.bw.compressedSize())
	encoded[checksumOffset] ^= 0x80

	reader, err := (ReaderConfig{DictCap: 1 << 20}).NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr == nil || !strings.Contains(readErr.Error(), "checksum error for block") {
		t.Fatalf("ReadAll error = %v, want block checksum error", readErr)
	}
}

func TestReaderRejectsTruncatedLZMA2Block(t *testing.T) {
	payload := xzTestPayload(1 << 20)
	encoded, writer := encodeXZTestStreamWithWriter(t, payload, maxInt64)
	compressedEnd := HeaderLen + writer.bw.headerLen + int(writer.bw.compressedSize())
	truncated := encoded[:compressedEnd-1]

	reader, err := (ReaderConfig{DictCap: 1 << 20}).NewReader(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAll error = %v, want io.ErrUnexpectedEOF", readErr)
	}
}

func TestReaderRejectsDictionaryAboveLimit(t *testing.T) {
	encoded := encodeXZTestStream(t, xzTestPayload(64<<10), maxInt64)
	reader, err := (ReaderConfig{DictCap: 4 << 10}).NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr == nil || !strings.Contains(readErr.Error(), "exceeds the 4096-byte limit") {
		t.Fatalf("ReadAll error = %v, want dictionary limit error", readErr)
	}
}

func TestReaderClose(t *testing.T) {
	encoded := encodeXZTestStream(t, xzTestPayload(1<<20), maxInt64)
	reader, err := (ReaderConfig{DictCap: 1 << 20}).NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if _, err := reader.Read(make([]byte, 4096)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func encodeXZTestStream(t *testing.T, payload []byte, blockSize int64) []byte {
	t.Helper()
	encoded, _ := encodeXZTestStreamWithWriter(t, payload, blockSize)
	return encoded
}

func encodeXZTestStreamWithWriter(t *testing.T, payload []byte, blockSize int64) ([]byte, *Writer) {
	t.Helper()
	var encoded bytes.Buffer
	writer, err := (WriterConfig{
		DictCap:   1 << 20,
		BufSize:   64 << 10,
		BlockSize: blockSize,
		CheckSum:  CRC64,
	}).NewWriter(&encoded)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}
	return bytes.Clone(encoded.Bytes()), writer
}

func xzTestPayload(size int) []byte {
	pattern := []byte("XZ native-reader interoperability payload 0123456789 abcdefghijklmnopqrstuvwxyz\n")
	return bytes.Repeat(pattern, (size+len(pattern)-1)/len(pattern))[:size]
}

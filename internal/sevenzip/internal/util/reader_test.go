package util

import (
	"bytes"
	"io"
	"testing"
)

type countingReadCloser struct {
	reader io.Reader
	reads  int
	closed bool
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (r *countingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestByteReadCloserBuffersFallback(t *testing.T) {
	payload := bytes.Repeat([]byte("buffered coder input"), 8*1024)
	input := &countingReadCloser{reader: bytes.NewReader(payload)}
	reader := ByteReadCloser(input)

	for i, want := range payload {
		got, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("ReadByte %d = %d, want %d", i, got, want)
		}
	}
	if input.reads > 4 {
		t.Fatalf("%d byte reads caused %d underlying reads", len(payload), input.reads)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !input.closed {
		t.Fatal("Close did not reach the underlying reader")
	}
}

func TestByteReadCloserPreservesExistingReader(t *testing.T) {
	input := NopCloser(bytes.NewReader([]byte("already buffered")))
	if got := ByteReadCloser(input); got != input {
		t.Fatal("reader implementing ReadCloser was wrapped")
	}
}

func TestByteReadCloserPreservesBufferedDataAcrossReadMethods(t *testing.T) {
	input := &countingReadCloser{reader: bytes.NewReader([]byte("abcdef"))}
	reader := ByteReadCloser(input)
	defer reader.Close()

	first, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(append([]byte{first}, rest...)); got != "abcdef" {
		t.Fatalf("decoded data = %q", got)
	}
}

package bzip2

import (
	"bytes"
	"io"
	"sync"
	"testing"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
)

type trackingReadCloser struct {
	io.Reader
	mu     sync.Mutex
	closed int
}

func (r *trackingReadCloser) Close() error {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
	return nil
}

func TestReaderRoundTripAndOwnsInput(t *testing.T) {
	payload := bytes.Repeat([]byte("sevenzip bzip2 coder\n"), 100_000)
	var compressed bytes.Buffer
	w, err := dsnetbzip2.NewWriter(&compressed, &dsnetbzip2.WriterConfig{Level: 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	source := &trackingReadCloser{Reader: bytes.NewReader(compressed.Bytes())}
	r, err := NewReader(nil, uint64(len(payload)), []io.ReadCloser{source})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded %d bytes; want %d", len(got), len(payload))
	}
	source.mu.Lock()
	closed := source.closed
	source.mu.Unlock()
	if closed != 1 {
		t.Fatalf("input closed %d times; want 1", closed)
	}
}

func TestReaderClosesInputOnInvalidHeader(t *testing.T) {
	source := &trackingReadCloser{Reader: bytes.NewReader([]byte("nope"))}
	if _, err := NewReader(nil, 0, []io.ReadCloser{source}); err == nil {
		t.Fatal("invalid BZip2 header was accepted")
	}
	source.mu.Lock()
	closed := source.closed
	source.mu.Unlock()
	if closed != 1 {
		t.Fatalf("input closed %d times; want 1", closed)
	}
}

package bzip2reader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cosnicolaou/pbzip2"
	dsnetbzip2 "github.com/dsnet/compress/bzip2"
)

func compressStream(t *testing.T, payload []byte, level int) []byte {
	t.Helper()
	var dst bytes.Buffer
	w, err := dsnetbzip2.NewWriter(&dst, &dsnetbzip2.WriterConfig{Level: level})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dst.Bytes()
}

func decodeAll(t *testing.T, compressed []byte) ([]byte, error) {
	t.Helper()
	r, err := NewReader(io.NopCloser(bytes.NewReader(compressed)))
	if err != nil {
		return nil, err
	}
	decoded, readErr := io.ReadAll(r)
	closeErr := r.Close()
	return decoded, errors.Join(readErr, closeErr)
}

func TestConcatenatedStreams(t *testing.T) {
	first := bytes.Repeat([]byte("first stream\n"), 80_000)
	second := bytes.Repeat([]byte("second stream\n"), 90_000)
	compressed := append(compressStream(t, first, 9), compressStream(t, second, 9)...)
	got, err := decodeAll(t, compressed)
	if err != nil {
		t.Fatal(err)
	}
	want := append(first, second...)
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded %d bytes; want %d", len(got), len(want))
	}
}

func TestRLEExpandedBlock(t *testing.T) {
	payload := bytes.Repeat([]byte{'A'}, 2<<20)
	got, err := decodeAll(t, compressStream(t, payload, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded %d bytes; want %d", len(got), len(payload))
	}
}

func TestCorruptionAndTruncation(t *testing.T) {
	payload := bytes.Repeat([]byte("checksum payload\n"), 20_000)
	compressed := compressStream(t, payload, 9)

	corrupt := append([]byte(nil), compressed...)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := decodeAll(t, corrupt); err == nil {
		t.Fatal("corrupt stream decoded without an error")
	}
	if _, err := decodeAll(t, compressed[:len(compressed)-1]); err == nil {
		t.Fatal("truncated stream decoded without an error")
	}
	if _, err := NewReader(io.NopCloser(bytes.NewReader([]byte("BZh0")))); !errors.Is(err, errInvalidHeader) {
		t.Fatalf("invalid level error = %v", err)
	}
}

func TestEarlyBlockErrorWithFullCreditWindow(t *testing.T) {
	first := compressStream(t, bytes.Repeat([]byte("first damaged stream\n"), 100_000), 9)
	second := compressStream(t, bytes.Repeat([]byte("second valid stream\n"), 100_000), 9)
	first[10] ^= 0xff // Stored CRC for the first block.
	compressed := append(first, second...)
	r, err := NewReader(io.NopCloser(bytes.NewReader(compressed)))
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, r)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("damaged first block decoded without an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("decoder deadlocked after an early block error")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestShortHeader(t *testing.T) {
	for _, input := range [][]byte{nil, {'B'}, {'B', 'Z', 'h'}} {
		if _, err := NewReader(io.NopCloser(bytes.NewReader(input))); err == nil {
			t.Fatalf("NewReader(%q) succeeded", input)
		}
	}
}

func TestRejectsInvalidConcatenatedLevel(t *testing.T) {
	valid := compressStream(t, []byte("valid"), 9)
	invalidNonEmpty := compressStream(t, []byte("invalid non-empty stream"), 9)
	invalidNonEmpty[3] = '0'
	if _, err := decodeAll(t, append(valid, invalidNonEmpty...)); err == nil {
		t.Fatal("invalid non-empty concatenated level was accepted")
	}
}

func TestMergedOrderReturnsAllCredits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	progress := make(chan pbzip2.Progress, 2)
	credits := make(chan struct{}, maxInFlightBlocks)
	done := make(chan struct{})
	go returnCredits(ctx, progress, credits, done)

	progress <- pbzip2.Progress{Block: 1}
	if got := <-credits; got != (struct{}{}) {
		t.Fatal("missing first credit")
	}
	progress <- pbzip2.Progress{Block: 3}
	for range 2 {
		select {
		case <-credits:
		case <-time.After(time.Second):
			t.Fatal("merged descriptor credit was not returned")
		}
	}
	close(progress)
	<-done
}

type blockingSource struct {
	mu         sync.Mutex
	data       []byte
	blocked    chan struct{}
	closed     chan struct{}
	blockOnce  sync.Once
	closeOnce  sync.Once
	closeCount int
}

func newBlockingSource() *blockingSource {
	return &blockingSource{
		data:    []byte("BZh9"),
		blocked: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (s *blockingSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	if len(s.data) > 0 {
		n := copy(p, s.data)
		s.data = s.data[n:]
		s.mu.Unlock()
		return n, nil
	}
	s.mu.Unlock()
	s.blockOnce.Do(func() { close(s.blocked) })
	<-s.closed
	return 0, io.ErrClosedPipe
}

func (s *blockingSource) Close() error {
	s.mu.Lock()
	s.closeCount++
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func TestCloseCancelsBlockedDecode(t *testing.T) {
	source := newBlockingSource()
	r, err := NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, r)
		readDone <- err
	}()

	select {
	case <-source.blocked:
	case <-time.After(time.Second):
		t.Fatal("scanner did not block on its source")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("active Read did not stop after Close")
	}
	select {
	case <-r.done:
	default:
		t.Fatal("Close returned before decoder goroutines stopped")
	}
	source.mu.Lock()
	closeCount := source.closeCount
	source.mu.Unlock()
	if closeCount != 1 {
		t.Fatalf("source closed %d times; want 1", closeCount)
	}
	if _, err := r.Read(nil); !errors.Is(err, errAlreadyClosed) {
		t.Fatalf("Read after Close error = %v", err)
	}
	if err := r.Close(); !errors.Is(err, errAlreadyClosed) {
		t.Fatalf("second Close error = %v", err)
	}
}

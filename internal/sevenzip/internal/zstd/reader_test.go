package zstd

import (
	"io"
	"sync"
	"testing"
	"time"

	kzstd "github.com/klauspost/compress/zstd"
)

type blockingReadCloser struct {
	data          []byte
	closed        chan struct{}
	blocked       chan struct{}
	closeObserved chan struct{}
	release       chan struct{}
	blockOnce     sync.Once
	observeOnce   sync.Once
	closeOnce     sync.Once
}

func (r *blockingReadCloser) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	r.blockOnce.Do(func() { close(r.blocked) })
	<-r.closed
	r.observeOnce.Do(func() { close(r.closeObserved) })
	<-r.release
	return 0, io.ErrClosedPipe
}

func (r *blockingReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func TestCloseWaitsForAsyncDecoder(t *testing.T) {
	for {
		decoder, ok := zstdReaderPool.Get().(*kzstd.Decoder)
		if !ok {
			break
		}
		decoder.Close()
	}

	source := &blockingReadCloser{
		closed:        make(chan struct{}),
		blocked:       make(chan struct{}),
		closeObserved: make(chan struct{}),
		release:       make(chan struct{}),
	}
	decoder, err := kzstd.NewReader(source, kzstd.WithDecoderConcurrency(2))
	if err != nil {
		t.Fatal(err)
	}
	reader := &readCloser{c: source, r: decoder}
	select {
	case <-source.blocked:
	case <-time.After(5 * time.Second):
		source.Close()
		close(source.release)
		t.Fatal("decoder did not block on the truncated source")
	}

	done := make(chan error, 1)
	go func() { done <- reader.Close() }()
	select {
	case <-source.closeObserved:
	case <-time.After(5 * time.Second):
		close(source.release)
		t.Fatal("decoder did not observe source closure")
	}
	select {
	case closeErr := <-done:
		close(source.release)
		t.Fatalf("Close returned before the decoder worker stopped: %v", closeErr)
	case <-time.After(50 * time.Millisecond):
	}
	close(source.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

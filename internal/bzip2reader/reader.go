// Package bzip2reader provides bounded block-parallel BZip2 decoding.
package bzip2reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

	"github.com/cosnicolaou/pbzip2"
)

const maxInFlightBlocks = 2

var (
	errAlreadyClosed = errors.New("bzip2: reader already closed")
	errInvalidHeader = errors.New("bzip2: invalid stream header")
)

// Reader owns its compressed source. Close cancels decoding, closes the
// source, and waits for all decoder goroutines to finish.
type Reader struct {
	mu        sync.Mutex
	reads     sync.WaitGroup
	stopOnce  sync.Once
	closed    bool
	source    io.ReadCloser
	decoder   *pbzip2.Decompressor
	cancel    context.CancelFunc
	done      chan struct{}
	terminal  error
	sourceErr error
}

// NewReader starts a bounded block-parallel decoder. At most two BZip2 blocks
// are scanned, decoded, or waiting for ordered output at once. A level-9 block
// can expand to about 46.62 MB after BZip2's first RLE stage, so the small
// credit window also bounds retained expanded buffers below the decoder memory
// policy used by the application.
func NewReader(source io.ReadCloser) (*Reader, error) {
	if source == nil {
		return nil, errors.New("bzip2: nil source")
	}

	var header [4]byte
	if _, err := io.ReadFull(source, header[:]); err != nil {
		return nil, fmt.Errorf("bzip2: read stream header: %w", err)
	}
	if !bytes.Equal(header[:3], []byte("BZh")) || header[3] < '1' || header[3] > '9' {
		return nil, errInvalidHeader
	}

	ctx, cancel := context.WithCancel(context.Background())
	progress := make(chan pbzip2.Progress)
	workers := min(runtime.GOMAXPROCS(0), maxInFlightBlocks)
	decoder := pbzip2.NewDecompressor(ctx,
		pbzip2.BZConcurrency(max(workers, 1)),
		pbzip2.BZSendUpdates(progress),
	)
	r := &Reader{
		source:  source,
		decoder: decoder,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	input := io.MultiReader(bytes.NewReader(header[:]), source)
	go r.run(ctx, input, progress)
	return r, nil
}

func (r *Reader) run(ctx context.Context, input io.Reader, progress chan pbzip2.Progress) {
	credits := make(chan struct{}, maxInFlightBlocks)
	for range maxInFlightBlocks {
		credits <- struct{}{}
	}

	progressDone := make(chan struct{})
	go returnCredits(ctx, progress, credits, progressDone)

	// pbzip2 v1.0.6 trims empty concatenated streams before exposing a block.
	// Its parser also accepts level '0', so an outputless BZh0 empty trailer is
	// accepted here. Non-empty invalid secondary levels remain observable and
	// are rejected by scanBlocks below.
	scanner := pbzip2.NewScanner(input)
	err := scanBlocks(ctx, scanner, r.decoder, credits)
	if err != nil {
		r.decoder.Cancel(err)
	}
	finishErr := r.decoder.Finish()
	close(progress)
	<-progressDone
	if err == nil {
		err = finishErr
	}

	r.mu.Lock()
	r.terminal = err
	r.mu.Unlock()
	close(r.done)
}

func scanBlocks(ctx context.Context, scanner *pbzip2.Scanner, decoder *pbzip2.Decompressor, credits chan struct{}) error {
	for {
		select {
		case <-credits:
		case <-ctx.Done():
			return ctx.Err()
		}

		if !scanner.Scan(ctx) {
			credits <- struct{}{}
			return scanner.Err()
		}
		block := scanner.Block()
		if block.StreamBlockSize < 100_000 || block.StreamBlockSize > 900_000 || block.StreamBlockSize%100_000 != 0 {
			return errInvalidHeader
		}
		if err := decoder.Append(block); err != nil {
			return err
		}
	}
}

// Progress is emitted after ordered output has been consumed. Returning the
// order delta, rather than one credit, also accounts for a false magic marker
// that pbzip2 repaired by merging two adjacent descriptors.
func returnCredits(ctx context.Context, progress <-chan pbzip2.Progress, credits chan struct{}, done chan<- struct{}) {
	defer close(done)
	var lastOrder uint64
	for update := range progress {
		if update.Block <= lastOrder {
			continue
		}
		release := update.Block - lastOrder
		lastOrder = update.Block
		for range release {
			if ctx.Err() != nil {
				break
			}
			select {
			case credits <- struct{}{}:
			case <-ctx.Done():
			}
		}
	}
}

func (r *Reader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, errAlreadyClosed
	}
	r.reads.Add(1)
	r.mu.Unlock()
	defer r.reads.Done()

	n, err := r.decoder.Read(p)
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, io.EOF) {
		r.stop(err)
	}
	<-r.done
	r.mu.Lock()
	terminal := r.terminal
	r.mu.Unlock()
	if errors.Is(err, io.EOF) && terminal != nil {
		return n, terminal
	}
	return n, err
}

func (r *Reader) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errAlreadyClosed
	}
	r.closed = true
	r.mu.Unlock()

	r.stop(context.Canceled)
	r.reads.Wait()
	<-r.done
	r.mu.Lock()
	sourceErr := r.sourceErr
	r.mu.Unlock()
	if sourceErr != nil {
		return fmt.Errorf("bzip2: close source: %w", sourceErr)
	}
	return nil
}

func (r *Reader) stop(cause error) {
	r.stopOnce.Do(func() {
		r.cancel()
		r.decoder.Cancel(cause)
		sourceErr := r.source.Close()
		r.mu.Lock()
		r.sourceErr = sourceErr
		r.mu.Unlock()
	})
}

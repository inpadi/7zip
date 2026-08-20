// Package bzip2 implements the Bzip2 decompressor.
package bzip2

import (
	"errors"
	"fmt"
	"io"

	"github.com/inpadi/7zip/internal/bzip2reader"
)

type readCloser struct {
	c io.Closer
	r io.Reader
}

var (
	errAlreadyClosed = errors.New("bzip2: already closed")
	errNeedOneReader = errors.New("bzip2: need exactly one reader")
)

func (rc *readCloser) Close() error {
	if rc.c == nil || rc.r == nil {
		return errAlreadyClosed
	}

	if err := rc.c.Close(); err != nil {
		return fmt.Errorf("bzip2: error closing: %w", err)
	}

	rc.c, rc.r = nil, nil

	return nil
}

func (rc *readCloser) Read(p []byte) (int, error) {
	if rc.r == nil {
		return 0, errAlreadyClosed
	}

	n, err := rc.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("bzip2: error reading: %w", err)
	}

	return n, err
}

// NewReader returns a new bzip2 io.ReadCloser.
func NewReader(_ []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 {
		return nil, errNeedOneReader
	}
	reader, err := bzip2reader.NewReader(readers[0])
	if err != nil {
		return nil, fmt.Errorf("bzip2: error creating reader: %w", errors.Join(err, readers[0].Close()))
	}

	return &readCloser{
		c: reader,
		r: reader,
	}, nil
}

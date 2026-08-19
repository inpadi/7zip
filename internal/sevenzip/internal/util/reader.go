// Package util implements various utility types and interfaces.
package util

import (
	"bufio"
	"io"
)

const byteReaderBufferSize = 64 * 1024

// SizeReadSeekCloser is an io.Reader, io.Seeker, and io.Closer with a Size
// method.
type SizeReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
	Size() int64
}

// Reader is both an io.Reader and io.ByteReader.
type Reader interface {
	io.Reader
	io.ByteReader
}

// ReadCloser is a Reader that is also an io.Closer.
type ReadCloser interface {
	Reader
	io.Closer
}

type nopCloser struct {
	Reader
}

func (nopCloser) Close() error {
	return nil
}

// NopCloser returns a ReadCloser with a no-op Close method wrapping the
// provided Reader r.
func NopCloser(r Reader) ReadCloser {
	return &nopCloser{r}
}

type byteReadCloser struct {
	io.ReadCloser
	reader *bufio.Reader
}

func (rc *byteReadCloser) Read(p []byte) (int, error) {
	return rc.reader.Read(p) //nolint:wrapcheck
}

func (rc *byteReadCloser) ReadByte() (byte, error) {
	return rc.reader.ReadByte() //nolint:wrapcheck
}

// ByteReadCloser returns r unchanged when it already implements ReadCloser.
// Otherwise it adds buffered Read and ReadByte methods while preserving Close.
func ByteReadCloser(r io.ReadCloser) ReadCloser {
	if rc, ok := r.(ReadCloser); ok {
		return rc
	}

	return &byteReadCloser{
		ReadCloser: r,
		reader:     bufio.NewReaderSize(r, byteReaderBufferSize),
	}
}

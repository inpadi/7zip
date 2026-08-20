//go:build !cgo || purego

package lzmadec

import "io"

// Available reports whether the native SDK decoder is part of this build.
func Available() bool { return false }

func newReader(io.ReadCloser, format, []byte, uint64, uint64) (io.ReadCloser, error) {
	return nil, ErrUnavailable
}

func newLZMA2UnknownReader(io.ReadCloser, []byte, uint64) (io.ReadCloser, error) {
	return nil, ErrUnavailable
}

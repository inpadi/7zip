//go:build !cgo || purego || (!amd64 && !arm64)

package lzmaenc

import "io"

// Available reports whether the native SDK encoder is part of this build.
func Available() bool { return false }

func newWriter(io.Writer, format, Config) (io.WriteCloser, []byte, error) {
	return nil, nil, ErrUnavailable
}

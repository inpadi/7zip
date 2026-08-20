package lzmadec

import (
	"errors"
	"io"
)

// ErrUnavailable reports that this build does not include the native decoder.
var ErrUnavailable = errors.New("native LZMA decoder is unavailable")

type format uint8

const (
	formatLZMA format = iota
	formatLZMA2
)

// NewLZMA creates a raw LZMA reader. properties must contain the five-byte
// property block stored in a 7z LZMA coder definition.
func NewLZMA(source io.ReadCloser, properties []byte, unpackSize, maxDictionary uint64) (io.ReadCloser, error) {
	return newReader(source, formatLZMA, properties, unpackSize, maxDictionary)
}

// NewLZMA2 creates a raw LZMA2 reader. properties must contain the one-byte
// dictionary property stored in a 7z LZMA2 coder definition.
func NewLZMA2(source io.ReadCloser, properties []byte, unpackSize, maxDictionary uint64) (io.ReadCloser, error) {
	return newReader(source, formatLZMA2, properties, unpackSize, maxDictionary)
}

// NewLZMA2Unknown creates a raw LZMA2 reader whose uncompressed size is not
// known in advance. The reader requires and validates the LZMA2 end marker.
// It reads compressed input on LZMA2 chunk boundaries so bytes following the
// stream remain available to the caller after decoding finishes.
func NewLZMA2Unknown(source io.ReadCloser, properties []byte, maxDictionary uint64) (io.ReadCloser, error) {
	return newLZMA2UnknownReader(source, properties, maxDictionary)
}

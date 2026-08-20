package lzmaenc

import (
	"errors"
	"io"
)

// ErrUnavailable reports that this build does not include the native encoder.
var ErrUnavailable = errors.New("native LZMA encoder is unavailable")

// Config defines the native encoder's bounded resource and compression setup.
type Config struct {
	Level               int
	Dictionary          uint32
	Threads             int
	MaxMemory           uint64
	EndMarker           bool
	LC                  int
	LP                  int
	PB                  int
	PropertiesDefined   bool
	ExpectedSize        uint64
	ExpectedSizeDefined bool
}

// MemoryReporter is implemented by native writers. PeakMemory reports the
// maximum number of SDK-requested bytes held concurrently after Close returns.
type MemoryReporter interface {
	PeakMemory() uint64
}

type format uint8

const (
	formatLZMA format = iota
	formatLZMA2
)

// NewLZMA creates a raw LZMA writer and returns its five-byte coder properties.
func NewLZMA(destination io.Writer, config Config) (io.WriteCloser, []byte, error) {
	return newWriter(destination, formatLZMA, config)
}

// NewLZMA2 creates a raw LZMA2 writer and returns its one-byte coder properties.
func NewLZMA2(destination io.Writer, config Config) (io.WriteCloser, []byte, error) {
	return newWriter(destination, formatLZMA2, config)
}

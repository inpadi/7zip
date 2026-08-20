package bra

import (
	"errors"
	"io"
)

type writeCloser struct {
	destination io.WriteCloser
	converter   converter
	buffer      []byte
	err         error
	closed      bool
}

func (w *writeCloser) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errAlreadyClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	if len(p) == 0 {
		return 0, nil
	}

	w.buffer = append(w.buffer, p...)
	processed := w.converter.Convert(w.buffer, true)
	if processed > 0 {
		w.err = writeFull(w.destination, w.buffer[:processed])
		remaining := copy(w.buffer, w.buffer[processed:])
		w.buffer = w.buffer[:remaining]
	}
	return len(p), w.err
}

func (w *writeCloser) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err == nil && len(w.buffer) > 0 {
		w.err = writeFull(w.destination, w.buffer)
	}
	w.err = errors.Join(w.err, w.destination.Close())
	return w.err
}

func writeFull(destination io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := destination.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func newWriter(destination io.WriteCloser, converter converter) (io.WriteCloser, error) {
	if destination == nil {
		return nil, errors.New("bra: nil destination")
	}
	return &writeCloser{
		destination: destination,
		converter:   converter,
		buffer:      make([]byte, 0, 64<<10),
	}, nil
}

// NewBCJWriter returns an x86 branch-filtering writer.
func NewBCJWriter(destination io.WriteCloser) (io.WriteCloser, error) {
	return newWriter(destination, new(bcj))
}

// NewARM64Writer returns an ARM64 branch-filtering writer.
func NewARM64Writer(destination io.WriteCloser) (io.WriteCloser, error) {
	return newWriter(destination, new(arm64))
}

// NewIA64Writer returns an IA-64 branch-filtering writer.
func NewIA64Writer(destination io.WriteCloser) (io.WriteCloser, error) {
	return newWriter(destination, new(ia64))
}

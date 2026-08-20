package archivefmt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/inpadi/7zip/internal/xz"
)

const (
	maxBzip2Workers = 24
	maxXZWorkers    = 3
	minXZChunkSize  = 48 << 20
)

var errParallelWriterClosed = errors.New("parallel compression writer is closed")

type chunkEncoder interface {
	Encode([]byte) error
	Bytes() []byte
}

type parallelStreamWriter struct {
	dst       io.Writer
	encoders  []chunkEncoder
	chunkSize int
	current   []byte
	pending   [][]byte
	spare     [][]byte
	written   int64
	err       error
	closed    bool
}

func newParallelStreamWriter(dst io.Writer, chunkSize int, encoders []chunkEncoder) *parallelStreamWriter {
	return &parallelStreamWriter{dst: dst, chunkSize: chunkSize, encoders: encoders}
}

func (w *parallelStreamWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errParallelWriterClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(p) > 0 {
		w.ensureCurrent(min(len(p), w.chunkSize))
		n := min(len(p), w.chunkSize-len(w.current))
		w.current = append(w.current, p[:n]...)
		p = p[n:]
		written += n
		w.written += int64(n)
		if len(w.current) != w.chunkSize {
			continue
		}
		w.pending = append(w.pending, w.current)
		w.current = nil
		if len(w.pending) == len(w.encoders) {
			if err := w.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *parallelStreamWriter) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err != nil {
		return w.err
	}
	if len(w.current) > 0 || w.written == 0 {
		w.pending = append(w.pending, w.current)
		w.current = nil
	}
	return w.flush()
}

func (w *parallelStreamWriter) ensureCurrent(incoming int) {
	if w.current != nil {
		return
	}
	if n := len(w.spare); n > 0 {
		w.current = w.spare[n-1][:0]
		w.spare = w.spare[:n-1]
		return
	}
	initial := min(w.chunkSize, max(incoming, 1<<20))
	w.current = make([]byte, 0, initial)
}

func (w *parallelStreamWriter) flush() error {
	if len(w.pending) == 0 || w.err != nil {
		return w.err
	}
	errs := make([]error, len(w.pending))
	var wg sync.WaitGroup
	wg.Add(len(w.pending))
	for i, chunk := range w.pending {
		go func() {
			defer wg.Done()
			errs[i] = w.encoders[i].Encode(chunk)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			w.err = fmt.Errorf("compress chunk %d: %w", i, err)
			return w.err
		}
	}
	for i := range w.pending {
		if err := writeFull(w.dst, w.encoders[i].Bytes()); err != nil {
			w.err = err
			return err
		}
		w.spare = append(w.spare, w.pending[i][:0])
	}
	w.pending = w.pending[:0]
	return nil
}

func writeFull(dst io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := dst.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

type bzip2ChunkEncoder struct {
	buffer bytes.Buffer
	level  int
	writer *dsnetbzip2.Writer
}

func (e *bzip2ChunkEncoder) Encode(src []byte) error {
	e.buffer.Reset()
	if e.writer == nil {
		writer, err := dsnetbzip2.NewWriter(&e.buffer, &dsnetbzip2.WriterConfig{Level: e.level})
		if err != nil {
			return err
		}
		e.writer = writer
	} else if err := e.writer.Reset(&e.buffer); err != nil {
		return err
	}
	if _, err := e.writer.Write(src); err != nil {
		return err
	}
	return e.writer.Close()
}

func (e *bzip2ChunkEncoder) Bytes() []byte { return e.buffer.Bytes() }

func newBzip2Writer(dst io.Writer, level int) (io.WriteCloser, error) {
	workers := min(runtime.GOMAXPROCS(0), maxBzip2Workers)
	if strconv.IntSize < 64 {
		workers = min(workers, 4)
	}
	if workers <= 1 {
		return dsnetbzip2.NewWriter(dst, &dsnetbzip2.WriterConfig{Level: level})
	}
	encoders := make([]chunkEncoder, workers)
	for i := range encoders {
		encoders[i] = &bzip2ChunkEncoder{level: level}
	}
	return newParallelStreamWriter(dst, level*100000, encoders), nil
}

type xzChunkEncoder struct {
	buffer bytes.Buffer
	config xz.WriterConfig
}

func (e *xzChunkEncoder) Encode(src []byte) error {
	e.buffer.Reset()
	writer, err := e.config.NewWriter(&e.buffer)
	if err != nil {
		return err
	}
	if _, err := writer.Write(src); err != nil {
		return err
	}
	return writer.Close()
}

func (e *xzChunkEncoder) Bytes() []byte { return e.buffer.Bytes() }

func newXZWriter(dst io.Writer, config xz.WriterConfig) (io.WriteCloser, error) {
	workers := min(runtime.GOMAXPROCS(0), maxXZWorkers)
	if strconv.IntSize < 64 {
		workers = 1
	}
	if workers <= 1 {
		return config.NewWriter(dst)
	}
	encoders := make([]chunkEncoder, workers)
	for i := range encoders {
		encoders[i] = &xzChunkEncoder{config: config}
	}
	chunkSize := max(minXZChunkSize, config.DictCap+config.DictCap/2)
	return newParallelStreamWriter(dst, chunkSize, encoders), nil
}

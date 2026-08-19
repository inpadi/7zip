package wim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/inpadi/7zip/internal/security"
	"github.com/inpadi/7zip/internal/wim/lzx"
	"github.com/inpadi/7zip/internal/wim/xpress"
)

const chunkSize = 32768 // Compressed resource chunk size

type compressionMethod uint8

const (
	compressionLZX compressionMethod = iota
	compressionXPRESS
)

type compressedReader struct {
	r            *io.SectionReader
	d            io.ReadCloser
	chunks       []int64
	curChunk     int
	originalSize int64
	method       compressionMethod
}

func newCompressedReader(r *io.SectionReader, originalSize int64, offset int64, method compressionMethod) (*compressedReader, error) {
	if originalSize <= 0 {
		return nil, errors.New("compressed WIM resource has invalid original size")
	}
	if originalSize > security.MaxFileBytes {
		return nil, fmt.Errorf("compressed WIM resource exceeds the %d-byte output limit", security.MaxFileBytes)
	}
	nchunks := (originalSize + chunkSize - 1) / chunkSize
	var base int64
	chunks := make([]int64, nchunks)
	if originalSize <= 0xffffffff {
		// 32-bit chunk offsets
		base = (nchunks - 1) * 4
		chunks32 := make([]uint32, nchunks-1)
		err := binary.Read(r, binary.LittleEndian, chunks32)
		if err != nil {
			return nil, err
		}
		for i, n := range chunks32 {
			chunks[i+1] = int64(n)
		}
	} else {
		// 64-bit chunk offsets
		base = (nchunks - 1) * 8
		err := binary.Read(r, binary.LittleEndian, chunks[1:])
		if err != nil {
			return nil, err
		}
	}

	if base > r.Size() {
		return nil, errors.New("WIM chunk table exceeds compressed resource")
	}
	previous := base
	for i, c := range chunks {
		chunks[i] = c + base
		if chunks[i] < previous || chunks[i] > r.Size() {
			return nil, errors.New("invalid WIM chunk offset")
		}
		previous = chunks[i]
	}

	cr := &compressedReader{
		r:            r,
		chunks:       chunks,
		originalSize: originalSize,
		method:       method,
	}

	err := cr.reset(int(offset / chunkSize))
	if err != nil {
		return nil, err
	}

	suboff := offset % chunkSize
	if suboff != 0 {
		_, err := io.CopyN(io.Discard, cr.d, suboff)
		if err != nil {
			return nil, err
		}
	}
	return cr, nil
}

func (r *compressedReader) chunkOffset(n int) int64 {
	if n == len(r.chunks) {
		return r.r.Size()
	}
	return r.chunks[n]
}

func (r *compressedReader) chunkSize(n int) int {
	return int(r.chunkOffset(n+1) - r.chunkOffset(n))
}

func (r *compressedReader) uncompressedSize(n int) int {
	if n < len(r.chunks)-1 {
		return chunkSize
	}
	size := int(r.originalSize % chunkSize)
	if size == 0 {
		size = chunkSize
	}
	return size
}

func (r *compressedReader) reset(n int) error {
	if n < 0 {
		return errors.New("invalid negative WIM chunk index")
	}
	if n >= len(r.chunks) {
		return io.EOF
	}
	if r.d != nil {
		r.d.Close()
	}
	r.curChunk = n
	size := r.chunkSize(n)
	uncompressedSize := r.uncompressedSize(n)
	if size <= 0 || size > chunkSize {
		return errors.New("invalid WIM compressed chunk size")
	}
	section := io.NewSectionReader(r.r, r.chunkOffset(n), int64(size))
	if size != uncompressedSize {
		switch r.method {
		case compressionLZX:
			d, err := lzx.NewReader(section, uncompressedSize)
			if err != nil {
				return err
			}
			r.d = d
		case compressionXPRESS:
			compressed := make([]byte, size)
			if _, err := io.ReadFull(section, compressed); err != nil {
				return fmt.Errorf("read XPRESS chunk: %w", err)
			}
			decoded, err := xpress.Decompress(compressed, uncompressedSize)
			if err != nil {
				return fmt.Errorf("decompress XPRESS chunk: %w", err)
			}
			r.d = io.NopCloser(bytes.NewReader(decoded))
		default:
			return fmt.Errorf("unsupported WIM compression method %d", r.method)
		}
	} else {
		r.d = io.NopCloser(section)
	}

	return nil
}

func (r *compressedReader) Read(b []byte) (int, error) {
	for {
		n, err := r.d.Read(b)
		if err != io.EOF { //nolint:errorlint
			return n, err
		}

		err = r.reset(r.curChunk + 1)
		if err != nil {
			return n, err
		}
	}
}

func (r *compressedReader) Close() error {
	var err error
	if r.d != nil {
		err = r.d.Close()
		r.d = nil
	}
	return err
}

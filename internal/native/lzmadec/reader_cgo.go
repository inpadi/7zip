//go:build cgo && !purego

package lzmadec

/*
#cgo CFLAGS: -O3 -DNDEBUG -I../sdk7z
#include "native_decoder.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"unsafe"
)

const (
	inputBufferSize = 256 << 10
	decodeBatchSize = 256 << 10

	statusFinishedWithoutMark = 4
	statusNeedsMoreInput      = 3
	statusFinishedWithMark    = 1
)

type reader struct {
	source  io.ReadCloser
	state   *C.I7zLzmaDecoder
	format  format
	cleanup runtime.Cleanup

	input      []byte
	inputStart int
	inputEnd   int
	sourceErr  error

	pending     []byte
	decoded     uint64
	size        uint64
	unknownSize bool
	done        bool
	err         error
}

// Available reports whether the native SDK decoder is part of this build.
func Available() bool { return true }

func newReader(source io.ReadCloser, streamFormat format, properties []byte, unpackSize, maxDictionary uint64) (io.ReadCloser, error) {
	if source == nil {
		return nil, errors.New("native LZMA decoder: nil source")
	}

	expectedProperties := 5
	if streamFormat == formatLZMA2 {
		expectedProperties = 1
	}
	if len(properties) != expectedProperties {
		return nil, fmt.Errorf("native LZMA decoder: got %d property bytes, want %d", len(properties), expectedProperties)
	}

	var createError C.int
	state := C.i7z_lzma_decoder_create(
		C.int(streamFormat),
		(*C.uint8_t)(unsafe.Pointer(&properties[0])),
		C.size_t(len(properties)),
		C.uint64_t(maxDictionary),
		C.size_t(inputBufferSize),
		&createError,
	)
	if state == nil {
		return nil, createErrorValue(int(createError), maxDictionary)
	}

	inputPointer := C.i7z_lzma_decoder_input(state)
	if inputPointer == nil {
		C.i7z_lzma_decoder_destroy(state)
		return nil, errors.New("native LZMA decoder: input allocation failed")
	}

	r := &reader{
		source: source,
		state:  state,
		format: streamFormat,
		input:  unsafe.Slice((*byte)(unsafe.Pointer(inputPointer)), inputBufferSize),
		size:   unpackSize,
	}
	r.cleanup = runtime.AddCleanup(r, destroyState, unsafe.Pointer(state))

	return r, nil
}

func newLZMA2UnknownReader(source io.ReadCloser, properties []byte, maxDictionary uint64) (io.ReadCloser, error) {
	r, err := newReader(source, formatLZMA2, properties, 0, maxDictionary)
	if err != nil {
		return nil, err
	}
	native := r.(*reader)
	native.unknownSize = true
	return native, nil
}

func (r *reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.state == nil {
		return 0, io.ErrClosedPipe
	}

	for len(r.pending) == 0 && !r.done && r.err == nil {
		r.decode()
	}

	if len(r.pending) != 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.EOF
}

func (r *reader) decode() {
	if r.unknownSize {
		r.decodeUnknownLZMA2()
		return
	}

	if r.decoded > r.size {
		r.err = errors.New("native LZMA decoder: produced too much output")
		return
	}

	remaining := r.size - r.decoded
	maxOutput := uint64(decodeBatchSize)
	if remaining < maxOutput {
		maxOutput = remaining
	}
	finishEnd := maxOutput == remaining

	needInput := r.inputStart == r.inputEnd
	for {
		if needInput {
			if err := r.fillInput(); err != nil && r.inputStart == r.inputEnd {
				if errors.Is(err, io.EOF) {
					r.err = io.ErrUnexpectedEOF
				} else {
					r.err = fmt.Errorf("native LZMA decoder: reading input: %w", err)
				}
				return
			}
			needInput = false
		}

		result := C.i7z_lzma_decoder_decode(
			r.state,
			C.size_t(r.inputStart),
			C.size_t(r.inputEnd-r.inputStart),
			C.size_t(maxOutput),
			boolToInt(finishEnd),
		)
		consumed := int(result.consumed)
		produced := int(result.produced)
		available := r.inputEnd - r.inputStart

		if consumed < 0 || consumed > available || produced < 0 || uint64(produced) > maxOutput {
			r.err = errors.New("native LZMA decoder: invalid SDK progress")
			return
		}
		r.inputStart += consumed

		if result.result != 0 {
			r.err = sdkError(int(result.result))
			return
		}
		if produced != 0 {
			r.pending = unsafe.Slice((*byte)(unsafe.Pointer(result.data)), produced)
			r.decoded += uint64(produced)
		}

		status := int(result.status)
		if status == statusFinishedWithMark ||
			(r.format == formatLZMA && status == statusFinishedWithoutMark) {
			if r.decoded != r.size {
				r.err = fmt.Errorf("native LZMA decoder: finished after %d bytes, want %d", r.decoded, r.size)
				return
			}
			r.done = true
			return
		}

		if r.decoded == r.size {
			if produced == 0 && consumed == 0 {
				r.err = errors.New("native LZMA decoder: stream did not finish at the expected size")
				return
			}
			maxOutput = 0
			finishEnd = true
			if status == statusNeedsMoreInput {
				needInput = true
			}
			if len(r.pending) != 0 {
				return
			}
			continue
		}

		if produced != 0 {
			return
		}
		if status == statusNeedsMoreInput {
			needInput = true
			continue
		}
		if consumed == 0 {
			r.err = errors.New("native LZMA decoder: decoder made no progress")
			return
		}
	}
}

func (r *reader) decodeUnknownLZMA2() {
	maxOutput := uint64(decodeBatchSize)
	needInput := r.inputStart == r.inputEnd

	for {
		if needInput {
			if err := r.fillLZMA2Chunk(); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					r.err = io.ErrUnexpectedEOF
				} else {
					r.err = fmt.Errorf("native LZMA decoder: reading input: %w", err)
				}
				return
			}
			needInput = false
		}

		result := C.i7z_lzma_decoder_decode(
			r.state,
			C.size_t(r.inputStart),
			C.size_t(r.inputEnd-r.inputStart),
			C.size_t(maxOutput),
			0,
		)
		consumed := int(result.consumed)
		produced := int(result.produced)
		available := r.inputEnd - r.inputStart

		if consumed < 0 || consumed > available || produced < 0 || uint64(produced) > maxOutput {
			r.err = errors.New("native LZMA decoder: invalid SDK progress")
			return
		}
		r.inputStart += consumed

		if result.result != 0 {
			r.err = sdkError(int(result.result))
			return
		}
		if produced != 0 {
			r.pending = unsafe.Slice((*byte)(unsafe.Pointer(result.data)), produced)
			r.decoded += uint64(produced)
		}

		if int(result.status) == statusFinishedWithMark {
			if r.inputStart != r.inputEnd {
				r.err = errors.New("native LZMA decoder: end marker did not consume its chunk")
				return
			}
			r.done = true
			return
		}
		if produced != 0 {
			return
		}
		if int(result.status) == statusNeedsMoreInput {
			if r.inputStart != r.inputEnd {
				r.err = errors.New("native LZMA decoder: decoder requested input before consuming its chunk")
				return
			}
			needInput = true
			continue
		}
		if consumed == 0 {
			r.err = errors.New("native LZMA decoder: decoder made no progress")
			return
		}
	}
}

// fillLZMA2Chunk reads exactly one framed LZMA2 chunk. In particular, the
// one-byte end marker is never followed by a speculative read into the XZ
// block padding or checksum.
func (r *reader) fillLZMA2Chunk() error {
	r.inputStart = 0
	r.inputEnd = 0

	if r.sourceErr != nil {
		return r.sourceErr
	}
	if _, err := io.ReadFull(r.source, r.input[:1]); err != nil {
		r.sourceErr = err
		return err
	}

	control := r.input[0]
	headerSize := 1
	bodySize := 0
	switch {
	case control == 0:
		// End marker.
	case control == 1 || control == 2:
		headerSize = 3
		if _, err := io.ReadFull(r.source, r.input[1:headerSize]); err != nil {
			r.sourceErr = err
			return err
		}
		bodySize = int(r.input[1])<<8 | int(r.input[2])
		bodySize++
	case control >= 0x80:
		headerSize = 5
		if control >= 0xc0 {
			headerSize++
		}
		if _, err := io.ReadFull(r.source, r.input[1:headerSize]); err != nil {
			r.sourceErr = err
			return err
		}
		bodySize = int(r.input[3])<<8 | int(r.input[4])
		bodySize++
	default:
		return errors.New("invalid LZMA2 chunk control byte")
	}

	total := headerSize + bodySize
	if total > len(r.input) {
		return errors.New("LZMA2 chunk exceeds the native input buffer")
	}
	if bodySize != 0 {
		if _, err := io.ReadFull(r.source, r.input[headerSize:total]); err != nil {
			r.sourceErr = err
			return err
		}
	}
	r.inputEnd = total
	return nil
}

func (r *reader) fillInput() error {
	if r.inputStart != 0 {
		copy(r.input, r.input[r.inputStart:r.inputEnd])
		r.inputEnd -= r.inputStart
		r.inputStart = 0
	}
	if r.inputEnd == len(r.input) {
		return errors.New("native LZMA decoder: compressed input buffer is full")
	}
	if r.sourceErr != nil {
		return r.sourceErr
	}

	n, err := r.source.Read(r.input[r.inputEnd:])
	if n < 0 || n > len(r.input)-r.inputEnd {
		return errors.New("native LZMA decoder: invalid source read count")
	}
	r.inputEnd += n
	if err != nil {
		r.sourceErr = err
	}
	if n == 0 && err == nil {
		return io.ErrNoProgress
	}
	return err
}

func (r *reader) Close() error {
	if r.state == nil {
		return io.ErrClosedPipe
	}
	r.cleanup.Stop()
	r.release()
	return r.source.Close()
}

func destroyState(state unsafe.Pointer) {
	C.i7z_lzma_decoder_destroy((*C.I7zLzmaDecoder)(state))
}

func (r *reader) release() {
	state := r.state
	r.state = nil
	r.pending = nil
	r.input = nil
	if state != nil {
		C.i7z_lzma_decoder_destroy(state)
	}
}

func boolToInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func createErrorValue(code int, maxDictionary uint64) error {
	switch code {
	case int(C.I7Z_LZMA_ERROR_DICTIONARY_LIMIT):
		return fmt.Errorf("native LZMA decoder: dictionary exceeds the %d-byte memory limit", maxDictionary)
	case int(C.I7Z_LZMA_ERROR_MEMORY):
		return errors.New("native LZMA decoder: memory allocation failed")
	case int(C.I7Z_LZMA_ERROR_PARAMETER):
		return errors.New("native LZMA decoder: invalid parameters")
	default:
		return sdkError(code)
	}
}

func sdkError(code int) error {
	var message string
	switch code {
	case 1:
		message = "corrupt data"
	case 2:
		message = "memory allocation failed"
	case 4:
		message = "unsupported properties"
	case 6:
		message = "unexpected end of input"
	case 11:
		message = "internal decoder failure"
	default:
		message = fmt.Sprintf("SDK error %d", code)
	}
	return fmt.Errorf("native LZMA decoder: %s", message)
}

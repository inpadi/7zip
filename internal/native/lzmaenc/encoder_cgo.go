//go:build cgo && !purego && (amd64 || arm64)

package lzmaenc

/*
#cgo CFLAGS: -O3 -DNDEBUG -std=gnu11 -I../sdk7z
#cgo !windows CFLAGS: -pthread
#cgo !windows LDFLAGS: -pthread
#include "native_encoder.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/cgo"
	"sync"
	"unsafe"
)

const (
	sdkErrorMemory = 2
	sdkErrorParam  = 5
	sdkErrorRead   = 8
	sdkErrorWrite  = 9
	sdkErrorThread = 12
)

var errAbandoned = errors.New("native LZMA encoder was abandoned")

type callbackBridge struct {
	input     *io.PipeReader
	output    io.Writer
	inputMu   sync.Mutex
	inputErr  error
	outputMu  sync.Mutex
	outputErr error
}

type workerResult struct {
	done       chan struct{}
	err        error
	peakMemory uint64
}

type writer struct {
	pipe    *io.PipeWriter
	worker  *workerResult
	cleanup runtime.Cleanup
	closed  bool
	err     error
}

// Available reports whether the native SDK encoder is part of this build.
func Available() bool { return true }

func newWriter(destination io.Writer, streamFormat format, config Config) (io.WriteCloser, []byte, error) {
	if destination == nil {
		return nil, nil, errors.New("native LZMA encoder: nil destination")
	}
	if config.Level < 0 || config.Level > 9 {
		return nil, nil, fmt.Errorf("native LZMA encoder: invalid level %d", config.Level)
	}
	if config.Dictionary < 1<<12 {
		return nil, nil, fmt.Errorf("native LZMA encoder: dictionary %d is too small", config.Dictionary)
	}
	if config.MaxMemory == 0 {
		return nil, nil, errors.New("native LZMA encoder: memory limit must be positive")
	}
	if config.Threads < 1 {
		config.Threads = 1
	}
	if config.Threads > 2 {
		config.Threads = 2
	}
	lc, lp, pb := -1, -1, -1
	if config.PropertiesDefined {
		if config.LC < 0 || config.LC > 8 || config.LP < 0 || config.LP > 4 ||
			config.PB < 0 || config.PB > 4 || (streamFormat == formatLZMA2 && config.LC+config.LP > 4) {
			return nil, nil, fmt.Errorf(
				"native LZMA encoder: invalid properties lc=%d lp=%d pb=%d",
				config.LC, config.LP, config.PB,
			)
		}
		lc, lp, pb = config.LC, config.LP, config.PB
	}

	prepareHardware()

	properties := make([]byte, 5)
	propertiesSize := C.size_t(len(properties))
	var createError C.int
	state := C.i7z_lzma_encoder_create(
		C.int(streamFormat),
		C.int(config.Level),
		C.uint32_t(config.Dictionary),
		C.int(config.Threads),
		boolToInt(config.EndMarker),
		C.int(lc),
		C.int(lp),
		C.int(pb),
		C.uint64_t(config.ExpectedSize),
		boolToInt(config.ExpectedSizeDefined),
		C.uint64_t(config.MaxMemory),
		(*C.uint8_t)(unsafe.Pointer(&properties[0])),
		&propertiesSize,
		&createError,
	)
	if state == nil {
		return nil, nil, createErrorValue(int(createError), config.MaxMemory)
	}
	if propertiesSize > C.size_t(len(properties)) {
		C.i7z_lzma_encoder_destroy(state)
		return nil, nil, errors.New("native LZMA encoder: invalid property size from SDK")
	}
	properties = properties[:int(propertiesSize)]

	input, pipe := io.Pipe()
	bridge := &callbackBridge{input: input, output: destination}
	handle := cgo.NewHandle(bridge)
	result := &workerResult{done: make(chan struct{})}

	go func() {
		runResult := C.i7z_lzma_encoder_run(state, C.uintptr_t(handle))
		handle.Delete()
		result.err = bridge.resultError(int(runResult.result), runResult.memory_limit_hit != 0, config.MaxMemory)
		result.peakMemory = uint64(runResult.peak_memory)
		_ = input.CloseWithError(result.err)
		C.i7z_lzma_encoder_destroy(state)
		close(result.done)
	}()

	w := &writer{pipe: pipe, worker: result}
	w.cleanup = runtime.AddCleanup(w, abandonWriter, pipe)
	return w, properties, nil
}

func (w *writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	n, err := w.pipe.Write(p)
	runtime.KeepAlive(w)
	if err != nil {
		<-w.worker.done
		if w.worker.err != nil {
			return n, w.worker.err
		}
	}
	return n, err
}

func (w *writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	w.cleanup.Stop()
	pipeErr := w.pipe.Close()
	<-w.worker.done
	w.err = w.worker.err
	if w.err == nil {
		w.err = pipeErr
	}
	runtime.KeepAlive(w)
	return w.err
}

func (w *writer) PeakMemory() uint64 {
	select {
	case <-w.worker.done:
		return w.worker.peakMemory
	default:
		return 0
	}
}

func abandonWriter(pipe *io.PipeWriter) {
	_ = pipe.CloseWithError(errAbandoned)
}

func (b *callbackBridge) resultError(code int, memoryLimitHit bool, maxMemory uint64) error {
	b.inputMu.Lock()
	inputErr := b.inputErr
	b.inputMu.Unlock()
	b.outputMu.Lock()
	outputErr := b.outputErr
	b.outputMu.Unlock()

	if outputErr != nil {
		return fmt.Errorf("native LZMA encoder: writing output: %w", outputErr)
	}
	if inputErr != nil {
		return fmt.Errorf("native LZMA encoder: reading input: %w", inputErr)
	}
	if code == 0 {
		return nil
	}
	if memoryLimitHit {
		return fmt.Errorf("native LZMA encoder: exceeded the %d-byte memory limit", maxMemory)
	}
	return sdkError(code)
}

//export i7zGoLzmaRead
func i7zGoLzmaRead(handle C.uintptr_t, data unsafe.Pointer, size *C.size_t) C.int {
	bridge := cgo.Handle(handle).Value().(*callbackBridge)
	buffer := unsafe.Slice((*byte)(data), int(*size))
	n, err := bridge.input.Read(buffer)
	*size = C.size_t(n)
	if err == nil || errors.Is(err, io.EOF) {
		return 0
	}
	bridge.inputMu.Lock()
	bridge.inputErr = err
	bridge.inputMu.Unlock()
	return sdkErrorRead
}

//export i7zGoLzmaWrite
func i7zGoLzmaWrite(handle C.uintptr_t, data unsafe.Pointer, size C.size_t) C.size_t {
	bridge := cgo.Handle(handle).Value().(*callbackBridge)
	buffer := unsafe.Slice((*byte)(data), int(size))
	bridge.outputMu.Lock()
	n, err := bridge.output.Write(buffer)
	if err == nil && n != len(buffer) {
		err = io.ErrShortWrite
	}
	if err != nil && bridge.outputErr == nil {
		bridge.outputErr = err
	}
	bridge.outputMu.Unlock()
	if err != nil {
		return 0
	}
	return C.size_t(n)
}

func createErrorValue(code int, maxMemory uint64) error {
	switch code {
	case int(C.I7Z_LZMA_ENCODE_ERROR_MEMORY_LIMIT):
		return fmt.Errorf("native LZMA encoder: exceeded the %d-byte memory limit", maxMemory)
	case int(C.I7Z_LZMA_ENCODE_ERROR_MEMORY):
		return errors.New("native LZMA encoder: memory allocation failed")
	case int(C.I7Z_LZMA_ENCODE_ERROR_PARAMETER):
		return errors.New("native LZMA encoder: invalid parameters")
	default:
		return sdkError(code)
	}
}

func sdkError(code int) error {
	var message string
	switch code {
	case sdkErrorMemory:
		message = "memory allocation failed"
	case sdkErrorParam:
		message = "invalid parameters"
	case sdkErrorRead:
		message = "input read failed"
	case sdkErrorWrite:
		message = "output write failed"
	case sdkErrorThread:
		message = "native worker thread failed"
	default:
		message = fmt.Sprintf("SDK error %d", code)
	}
	return fmt.Errorf("native LZMA encoder: %s", message)
}

func boolToInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

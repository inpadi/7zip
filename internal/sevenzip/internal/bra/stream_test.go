package bra

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

type converterFactory func() converter

type readerFactory func([]byte, uint64, []io.ReadCloser) (io.ReadCloser, error)

type writerFactory func(io.WriteCloser) (io.WriteCloser, error)

func TestBranchConvertersStreamingBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		factory    converterFactory
		reader     readerFactory
		writer     writerFactory
		payload    []byte
		maxTail    int
		knownBytes []byte
	}{
		{
			name:       "BCJ",
			factory:    func() converter { return new(bcj) },
			reader:     NewBCJReader,
			writer:     NewBCJWriter,
			payload:    bcjTestPayload(),
			maxTail:    bcjLookAhead,
			knownBytes: []byte{0xe8, 0x05, 0x00, 0x00, 0x00},
		},
		{
			name:       "ARM64",
			factory:    func() converter { return new(arm64) },
			reader:     NewARM64Reader,
			writer:     NewARM64Writer,
			payload:    arm64TestPayload(),
			maxTail:    arm64Alignment - 1,
			knownBytes: []byte{0x01, 0x00, 0x00, 0x94},
		},
		{
			name:    "IA64",
			factory: func() converter { return new(ia64) },
			reader:  NewIA64Reader,
			writer:  NewIA64Writer,
			payload: ia64TestPayload(),
			maxTail: ia64Alignment - 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for tail := 0; tail <= test.maxTail; tail++ {
				input := append([]byte(nil), test.payload...)
				for i := range tail {
					input = append(input, byte(0xa0+i))
				}
				oneShot := convertOneShot(t, test.factory(), input)
				if bytes.Equal(oneShot, input) {
					t.Fatalf("tail %d: encoder did not convert a branch", tail)
				}
				if tail == 0 && len(test.knownBytes) > 0 &&
					!bytes.Contains(oneShot, test.knownBytes) {
					t.Fatalf("canonical encoding does not contain % x", test.knownBytes)
				}

				for chunkSize := 1; chunkSize <= 2*test.factory().Size()+1; chunkSize++ {
					encoded := encodeInChunks(t, test.writer, input, chunkSize, chunkSize%3+1)
					if !bytes.Equal(encoded, oneShot) {
						t.Fatalf("tail %d, input chunk %d: streaming encoding differs from one-shot", tail, chunkSize)
					}
					for outputChunk := 1; outputChunk <= test.factory().Size()+1; outputChunk++ {
						decoded := decodeInChunks(t, test.reader, encoded, chunkSize, outputChunk, len(input))
						if !bytes.Equal(decoded, input) {
							t.Fatalf("tail %d, input chunk %d, output chunk %d: decoded payload differs", tail, chunkSize, outputChunk)
						}
					}
				}
			}
		})
	}
}

func convertOneShot(t *testing.T, conv converter, input []byte) []byte {
	t.Helper()
	buffer := append([]byte(nil), input...)
	processed := conv.Convert(buffer, true)
	if processed < 0 || processed > len(buffer) {
		t.Fatalf("converter processed %d of %d buffered bytes", processed, len(buffer))
	}
	return buffer
}

func encodeInChunks(
	t *testing.T,
	newWriter writerFactory,
	input []byte,
	chunkSize, destinationChunk int,
) []byte {
	t.Helper()
	destination := &shortWriteCloser{max: destinationChunk}
	w, err := newWriter(destination)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(input); {
		end := min(offset+chunkSize, len(input))
		n, writeErr := w.Write(input[offset:end])
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if n != end-offset {
			t.Fatalf("writer accepted %d of %d bytes", n, end-offset)
		}
		offset = end
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !destination.closed {
		t.Fatal("branch writer did not close its destination")
	}
	return destination.buffer.Bytes()
}

func decodeInChunks(
	t *testing.T,
	newReader readerFactory,
	encoded []byte,
	inputChunk, outputChunk, decodedSize int,
) []byte {
	t.Helper()
	source := &shortReadCloser{reader: bytes.NewReader(encoded), max: inputChunk}
	r, err := newReader(nil, uint64(decodedSize), []io.ReadCloser{source})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	decoded := make([]byte, 0, decodedSize)
	buffer := make([]byte, outputChunk)
	for len(decoded) < decodedSize {
		n, readErr := r.Read(buffer[:min(len(buffer), decodedSize-len(decoded))])
		decoded = append(decoded, buffer[:n]...)
		if readErr != nil {
			if readErr == io.EOF && len(decoded) == decodedSize {
				break
			}
			t.Fatalf("read %d of %d decoded bytes: %v", len(decoded), decodedSize, readErr)
		}
		if n == 0 {
			t.Fatalf("read made no progress after %d of %d decoded bytes", len(decoded), decodedSize)
		}
	}
	return decoded
}

type shortReadCloser struct {
	reader *bytes.Reader
	max    int
}

func (r *shortReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p[:min(len(p), r.max)])
}

func (*shortReadCloser) Close() error { return nil }

type shortWriteCloser struct {
	buffer bytes.Buffer
	max    int
	closed bool
}

func (w *shortWriteCloser) Write(p []byte) (int, error) {
	return w.buffer.Write(p[:min(len(p), w.max)])
}

func (w *shortWriteCloser) Close() error {
	w.closed = true
	return nil
}

func bcjTestPayload() []byte {
	payload := bytes.Repeat([]byte{0x90}, 80)
	for _, branch := range []struct {
		offset       int
		opcode       byte
		displacement uint32
	}{
		{offset: 0, opcode: 0xe8, displacement: 0},
		{offset: 8, opcode: 0xe9, displacement: 0x10},
		{offset: 17, opcode: 0xe8, displacement: 0xffffff80},
		{offset: 31, opcode: 0xe9, displacement: 0x00012345},
		{offset: 55, opcode: 0xe8, displacement: 0xfffedcba},
	} {
		payload[branch.offset] = branch.opcode
		binary.LittleEndian.PutUint32(payload[branch.offset+1:], branch.displacement)
	}
	return payload
}

func arm64TestPayload() []byte {
	payload := make([]byte, 12*arm64Alignment)
	for offset := 0; offset < len(payload); offset += arm64Alignment {
		binary.LittleEndian.PutUint32(payload[offset:], 0xd503201f) // NOP
	}
	for _, offset := range []int{4, 16, 28, 40} {
		binary.LittleEndian.PutUint32(payload[offset:], 0x94000000)
	}
	return payload
}

func ia64TestPayload() []byte {
	payload := make([]byte, 4*ia64Alignment)
	for i := range payload {
		payload[i] = byte(i*37 + 11)
	}
	for bundle := 0; bundle < len(payload); bundle += ia64Alignment {
		payload[bundle] = 0x16
		for _, slot := range []int{1, 2} {
			offset := bundle + slot*5 - 4
			payload[offset] = 0
			binary.LittleEndian.PutUint32(payload[offset+1:], uint32(0x0a000000)<<slot)
		}
	}
	return payload
}

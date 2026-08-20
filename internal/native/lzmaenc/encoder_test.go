package lzmaenc_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/inpadi/7zip/internal/native/lzmaenc"
	golzma "github.com/inpadi/7zip/internal/xz/lzma"
)

const (
	testDictionary  = 1 << 20
	testMemoryLimit = 256 << 20
)

func TestLZMARoundTrip(t *testing.T) {
	requireNative(t)
	payload := mixedPayload(2 << 20)
	var compressed bytes.Buffer
	encoder, properties, err := lzmaenc.NewLZMA(&compressed, testConfig())
	if err != nil {
		t.Fatalf("NewLZMA: %v", err)
	}
	writeChunks(t, encoder, payload, 7919)
	if err := encoder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if reporter, ok := encoder.(lzmaenc.MemoryReporter); !ok || reporter.PeakMemory() == 0 || reporter.PeakMemory() > testMemoryLimit {
		t.Fatalf("native peak memory is unavailable or outside its limit")
	}
	if len(properties) != 5 {
		t.Fatalf("property length = %d, want 5", len(properties))
	}

	header := append([]byte(nil), properties...)
	header = binary.LittleEndian.AppendUint64(header, uint64(len(payload)))
	stream := io.MultiReader(bytes.NewReader(header), bytes.NewReader(compressed.Bytes()))
	decoder, err := (golzma.ReaderConfig{DictCap: testDictionary}).NewReader(stream)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	decoded, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded LZMA payload differs from input")
	}
}

func TestLZMA2RoundTrip(t *testing.T) {
	requireNative(t)
	payload := mixedPayload(2 << 20)
	var compressed bytes.Buffer
	encoder, properties, err := lzmaenc.NewLZMA2(&compressed, testConfig())
	if err != nil {
		t.Fatalf("NewLZMA2: %v", err)
	}
	writeChunks(t, encoder, payload, 8191)
	if err := encoder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(properties) != 1 {
		t.Fatalf("property length = %d, want 1", len(properties))
	}
	dictionary, err := golzma.DecodeDictCap(properties[0])
	if err != nil {
		t.Fatalf("DecodeDictCap: %v", err)
	}
	if dictionary != testDictionary {
		t.Fatalf("dictionary = %d, want %d", dictionary, testDictionary)
	}

	decoder, err := (golzma.Reader2Config{DictCap: testDictionary}).NewReader2(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatalf("NewReader2: %v", err)
	}
	decoded, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded LZMA2 payload differs from input")
	}
}

func TestMemoryLimit(t *testing.T) {
	requireNative(t)
	config := testConfig()
	config.Dictionary = 32 << 20
	config.MaxMemory = 64 << 10
	var compressed bytes.Buffer
	encoder, _, err := lzmaenc.NewLZMA2(&compressed, config)
	if err != nil {
		return
	}
	_, writeErr := encoder.Write([]byte("memory limit payload"))
	closeErr := encoder.Close()
	if writeErr == nil && closeErr == nil {
		t.Fatal("encoder exceeded its memory limit without an error")
	}
}

func TestDestinationError(t *testing.T) {
	requireNative(t)
	destinationErr := errors.New("destination failed")
	destination := &errorWriter{err: destinationErr}
	encoder, _, err := lzmaenc.NewLZMA2(destination, testConfig())
	if err != nil {
		t.Fatalf("NewLZMA2: %v", err)
	}
	_, writeErr := encoder.Write(mixedPayload(1 << 20))
	closeErr := encoder.Close()
	if !errors.Is(writeErr, destinationErr) && !errors.Is(closeErr, destinationErr) {
		t.Fatalf("Write error = %v, Close error = %v, want destination error", writeErr, closeErr)
	}
}

func BenchmarkLZMA2Encode(b *testing.B) {
	if !lzmaenc.Available() {
		b.Skip("native encoder is unavailable")
	}
	payload := mixedPayload(8 << 20)
	config := testConfig()
	config.Dictionary = 8 << 20
	config.MaxMemory = 512 << 20
	b.SetBytes(int64(len(payload)))

	b.Run("NativeSDK", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		for b.Loop() {
			var compressed bytes.Buffer
			encoder, _, err := lzmaenc.NewLZMA2(&compressed, config)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := encoder.Write(payload); err != nil {
				b.Fatal(err)
			}
			if err := encoder.Close(); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(compressed.Len())/float64(len(payload)), "ratio")
		}
	})

	b.Run("Go", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		for b.Loop() {
			var compressed bytes.Buffer
			encoder, err := (golzma.Writer2Config{DictCap: int(config.Dictionary)}).NewWriter2(&compressed)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := encoder.Write(payload); err != nil {
				b.Fatal(err)
			}
			if err := encoder.Close(); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(compressed.Len())/float64(len(payload)), "ratio")
		}
	})
}

func testConfig() lzmaenc.Config {
	return lzmaenc.Config{
		Level:      7,
		Dictionary: testDictionary,
		Threads:    2,
		MaxMemory:  testMemoryLimit,
	}
}

func writeChunks(t *testing.T, writer io.Writer, payload []byte, chunkSize int) {
	t.Helper()
	for len(payload) != 0 {
		chunk := min(chunkSize, len(payload))
		n, err := writer.Write(payload[:chunk])
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if n != chunk {
			t.Fatalf("Write count = %d, want %d", n, chunk)
		}
		payload = payload[chunk:]
	}
}

func mixedPayload(size int) []byte {
	payload := make([]byte, size)
	state := uint64(0x9e3779b97f4a7c15)
	for i := range payload {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		payload[i] = byte(state)
	}
	for offset := 256 << 10; offset < len(payload); offset += 256 << 10 {
		length := min(64<<10, len(payload)-offset)
		copy(payload[offset:offset+length], payload[offset-(64<<10):offset-(64<<10)+length])
	}
	return payload
}

func requireNative(t *testing.T) {
	t.Helper()
	if !lzmaenc.Available() {
		t.Skip("native encoder is unavailable")
	}
}

type errorWriter struct {
	err error
}

func (w *errorWriter) Write(p []byte) (int, error) {
	return len(p), w.err
}

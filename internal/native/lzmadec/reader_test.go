package lzmadec_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/inpadi/7zip/internal/native/lzmadec"
	golzma "github.com/inpadi/7zip/internal/xz/lzma"
)

const testDictionary = 1 << 20

func TestLZMA(t *testing.T) {
	requireNative(t)
	payload := testPayload(2 << 20)
	properties, compressed := encodeLZMA(t, payload)

	reader, err := lzmadec.NewLZMA(
		io.NopCloser(bytes.NewReader(compressed)), properties,
		uint64(len(payload)), testDictionary,
	)
	if err != nil {
		t.Fatalf("NewLZMA: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded LZMA payload differs from input")
	}
}

func TestLZMA2(t *testing.T) {
	requireNative(t)
	payload := testPayload(2 << 20)
	properties, compressed := encodeLZMA2(t, payload)

	reader, err := lzmadec.NewLZMA2(
		io.NopCloser(bytes.NewReader(compressed)), properties,
		uint64(len(payload)), testDictionary,
	)
	if err != nil {
		t.Fatalf("NewLZMA2: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded LZMA2 payload differs from input")
	}
}

func TestLZMA2MixedPayload(t *testing.T) {
	requireNative(t)
	payload := mixedPayload(2 << 20)
	properties, compressed := encodeLZMA2(t, payload)

	reader, err := lzmadec.NewLZMA2(
		io.NopCloser(bytes.NewReader(compressed)), properties,
		uint64(len(payload)), testDictionary,
	)
	if err != nil {
		t.Fatalf("NewLZMA2: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded mixed LZMA2 payload differs from input")
	}
}

func TestLZMA2UnknownSize(t *testing.T) {
	requireNative(t)
	payload := mixedPayload(3 << 20)
	properties, compressed := encodeLZMA2(t, payload)
	trailer := []byte("xz block padding and checksum")
	source := bytes.NewReader(append(bytes.Clone(compressed), trailer...))

	reader, err := lzmadec.NewLZMA2Unknown(
		io.NopCloser(source), properties, testDictionary,
	)
	if err != nil {
		t.Fatalf("NewLZMA2Unknown: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded unknown-size LZMA2 payload differs from input")
	}
	remaining, err := io.ReadAll(source)
	if err != nil {
		t.Fatalf("reading trailer: %v", err)
	}
	if !bytes.Equal(remaining, trailer) {
		t.Fatalf("remaining input = %q, want %q", remaining, trailer)
	}
}

func TestLZMA2UnknownSizeTruncated(t *testing.T) {
	requireNative(t)
	payload := mixedPayload(1 << 20)
	properties, compressed := encodeLZMA2(t, payload)

	for _, remove := range []int{1, 2, len(compressed) / 2} {
		t.Run(fmt.Sprintf("Remove%d", remove), func(t *testing.T) {
			truncated := compressed[:len(compressed)-remove]
			reader, err := lzmadec.NewLZMA2Unknown(
				io.NopCloser(bytes.NewReader(truncated)), properties, testDictionary,
			)
			if err != nil {
				t.Fatalf("NewLZMA2Unknown: %v", err)
			}
			_, readErr := io.ReadAll(reader)
			_ = reader.Close()
			if !errors.Is(readErr, io.ErrUnexpectedEOF) {
				t.Fatalf("ReadAll error = %v, want io.ErrUnexpectedEOF", readErr)
			}
		})
	}
}

func TestTruncatedLZMA2(t *testing.T) {
	requireNative(t)
	payload := testPayload(1 << 20)
	properties, compressed := encodeLZMA2(t, payload)
	compressed = compressed[:len(compressed)/2]

	reader, err := lzmadec.NewLZMA2(
		io.NopCloser(bytes.NewReader(compressed)), properties,
		uint64(len(payload)), testDictionary,
	)
	if err != nil {
		t.Fatalf("NewLZMA2: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr == nil {
		t.Fatal("truncated stream decoded without an error")
	}
}

func TestWrongOutputSize(t *testing.T) {
	requireNative(t)
	payload := testPayload(1 << 20)
	properties, compressed := encodeLZMA(t, payload)

	reader, err := lzmadec.NewLZMA(
		io.NopCloser(bytes.NewReader(compressed)), properties,
		uint64(len(payload)-1), testDictionary,
	)
	if err != nil {
		t.Fatalf("NewLZMA: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr == nil {
		t.Fatal("stream with the wrong output size decoded without an error")
	}
}

func TestDictionaryLimit(t *testing.T) {
	requireNative(t)
	payload := testPayload(256 << 10)
	properties, compressed := encodeLZMA2(t, payload)

	reader, err := lzmadec.NewLZMA2(
		io.NopCloser(bytes.NewReader(compressed)), properties,
		uint64(len(payload)), testDictionary/2,
	)
	if reader != nil {
		_ = reader.Close()
		t.Fatal("NewLZMA2 returned a reader above the dictionary limit")
	}
	if err == nil || !strings.Contains(err.Error(), "dictionary exceeds") {
		t.Fatalf("NewLZMA2 error = %v, want dictionary limit error", err)
	}
}

func TestClose(t *testing.T) {
	requireNative(t)
	payload := testPayload(256 << 10)
	properties, compressed := encodeLZMA2(t, payload)
	source := &trackingReadCloser{Reader: bytes.NewReader(compressed)}

	reader, err := lzmadec.NewLZMA2(source, properties, uint64(len(payload)), testDictionary)
	if err != nil {
		t.Fatalf("NewLZMA2: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !source.closed {
		t.Fatal("Close did not close the compressed source")
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func BenchmarkLZMA2Decode(b *testing.B) {
	if !lzmadec.Available() {
		b.Skip("native decoder is unavailable")
	}
	payload := mixedPayload(8 << 20)
	properties, compressed := encodeLZMA2(b, payload)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()

	b.Run("NativeSDK", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		for range b.N {
			reader, err := lzmadec.NewLZMA2(
				io.NopCloser(bytes.NewReader(compressed)), properties,
				uint64(len(payload)), testDictionary,
			)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				b.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("NativeSDKUnknownSize", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		for range b.N {
			reader, err := lzmadec.NewLZMA2Unknown(
				io.NopCloser(bytes.NewReader(compressed)), properties,
				testDictionary,
			)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				b.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Go", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		for range b.N {
			reader, err := (golzma.Reader2Config{DictCap: testDictionary}).NewReader2(bytes.NewReader(compressed))
			if err != nil {
				b.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
}

func encodeLZMA(tb testingTB, payload []byte) ([]byte, []byte) {
	tb.Helper()
	var encoded bytes.Buffer
	writer, err := (golzma.WriterConfig{DictCap: testDictionary}).NewWriter(&encoded)
	if err != nil {
		tb.Fatalf("NewWriter: %v", err)
	}
	if _, err := writer.Write(payload); err != nil {
		tb.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		tb.Fatalf("Close: %v", err)
	}
	if encoded.Len() < golzma.HeaderLen {
		tb.Fatalf("encoded LZMA stream is only %d bytes", encoded.Len())
	}
	stream := encoded.Bytes()
	return bytes.Clone(stream[:5]), bytes.Clone(stream[golzma.HeaderLen:])
}

func encodeLZMA2(tb testingTB, payload []byte) ([]byte, []byte) {
	tb.Helper()
	var encoded bytes.Buffer
	writer, err := (golzma.Writer2Config{DictCap: testDictionary}).NewWriter2(&encoded)
	if err != nil {
		tb.Fatalf("NewWriter2: %v", err)
	}
	if _, err := writer.Write(payload); err != nil {
		tb.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		tb.Fatalf("Close: %v", err)
	}
	return []byte{golzma.EncodeDictCap(testDictionary)}, bytes.Clone(encoded.Bytes())
}

func testPayload(size int) []byte {
	pattern := []byte("native LZMA decoder differential test payload: abcdefghijklmnopqrstuvwxyz 0123456789\n")
	return bytes.Repeat(pattern, (size+len(pattern)-1)/len(pattern))[:size]
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

	const (
		stride = 256 << 10
		repeat = 64 << 10
	)
	for offset := stride; offset < len(payload); offset += stride {
		length := min(repeat, len(payload)-offset)
		copy(payload[offset:offset+length], payload[offset-repeat:offset-repeat+length])
	}
	return payload
}

func requireNative(t *testing.T) {
	t.Helper()
	if !lzmadec.Available() {
		t.Skip("native decoder is unavailable")
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

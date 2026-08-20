package archivefmt

import (
	"bytes"
	stdbzip2 "compress/bzip2"
	"compress/flate"
	"io"
	"math/rand/v2"
	"testing"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	kpflate "github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/zstd"
)

const benchmarkPayloadSize = 2 << 20

type benchmarkCountingWriter int64

func (w *benchmarkCountingWriter) Write(p []byte) (int, error) {
	*w += benchmarkCountingWriter(len(p))
	return len(p), nil
}

func benchmarkPayload() []byte {
	payload := make([]byte, benchmarkPayloadSize)
	for offset := 0; offset < len(payload); offset += 4096 {
		block := payload[offset:min(offset+4096, len(payload))]
		if offset%(4*4096) == 0 {
			generator := rand.New(rand.NewPCG(uint64(offset), 1))
			for i := range block {
				block[i] = byte(generator.Uint32())
			}
			continue
		}
		copy(block, bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), (len(block)+43)/44))
	}
	return payload
}

func benchmarkStreamPayload(size int) []byte {
	payload := make([]byte, size)
	text := []byte("the quick brown fox jumps over the lazy dog; compression benchmark payload\n")
	for offset := 0; offset < len(payload); offset += 64 << 10 {
		block := payload[offset:min(offset+(64<<10), len(payload))]
		if (offset/(64<<10))%3 == 0 {
			generator := rand.New(rand.NewPCG(uint64(offset), uint64(offset)+1))
			for i := range block {
				block[i] = byte(generator.Uint32())
			}
			continue
		}
		for i := range block {
			block[i] = text[i%len(text)]
		}
	}
	return payload
}

func BenchmarkDeflateCompression(b *testing.B) {
	payload := benchmarkPayload()
	for _, test := range []struct {
		name  string
		level int
	}{
		{name: "BestSpeed", level: flate.BestSpeed},
		{name: "Default", level: flate.DefaultCompression},
		{name: "BestCompression", level: flate.BestCompression},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				var dst benchmarkCountingWriter
				writer, err := flate.NewWriter(&dst, test.level)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(writer, bytes.NewReader(payload)); err != nil {
					b.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(dst)/float64(len(payload)), "ratio")
			}
		})
	}
}

func BenchmarkDeflateDecompression(b *testing.B) {
	payload := benchmarkPayload()
	var compressed bytes.Buffer
	w, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name string
		new  func(io.Reader) io.ReadCloser
	}{
		{name: "Stdlib", new: flate.NewReader},
		{name: "Klauspost", new: kpflate.NewReader},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				r := test.new(bytes.NewReader(compressed.Bytes()))
				n, readErr := io.Copy(io.Discard, r)
				closeErr := r.Close()
				if readErr != nil || closeErr != nil || n != int64(len(payload)) {
					b.Fatalf("decoded %d bytes: read=%v close=%v", n, readErr, closeErr)
				}
			}
		})
	}
}

func BenchmarkZstdDecompression(b *testing.B) {
	payload := benchmarkStreamPayload(64 << 20)
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(7)),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		b.Fatal(err)
	}
	compressed := enc.EncodeAll(payload, nil)
	enc.Close()
	for _, test := range []struct {
		name    string
		options []zstd.DOption
	}{
		{
			name: "SingleLowmem",
			options: []zstd.DOption{
				zstd.WithDecoderConcurrency(1),
				zstd.WithDecoderLowmem(true),
			},
		},
		{
			name: "SingleHighmem",
			options: []zstd.DOption{
				zstd.WithDecoderConcurrency(1),
				zstd.WithDecoderLowmem(false),
			},
		},
		{
			name: "DefaultLowmem",
			options: []zstd.DOption{
				zstd.WithDecoderLowmem(true),
			},
		},
		{
			name: "DefaultHighmem",
			options: []zstd.DOption{
				zstd.WithDecoderLowmem(false),
			},
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				dec, err := zstd.NewReader(bytes.NewReader(compressed), test.options...)
				if err != nil {
					b.Fatal(err)
				}
				n, readErr := io.Copy(io.Discard, dec)
				dec.Close()
				if readErr != nil || n != int64(len(payload)) {
					b.Fatalf("decoded %d bytes: %v", n, readErr)
				}
			}
		})
	}
}

func BenchmarkBzip2Decompression(b *testing.B) {
	payload := benchmarkStreamPayload(16 << 20)
	var compressed bytes.Buffer
	enc, err := dsnetbzip2.NewWriter(&compressed, &dsnetbzip2.WriterConfig{Level: 9})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := enc.Write(payload); err != nil {
		b.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name string
		new  func(io.Reader) (io.Reader, error)
	}{
		{
			name: "Stdlib",
			new: func(r io.Reader) (io.Reader, error) {
				return stdbzip2.NewReader(r), nil
			},
		},
		{
			name: "DSNet",
			new: func(r io.Reader) (io.Reader, error) {
				return dsnetbzip2.NewReader(r, nil)
			},
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				r, err := test.new(bytes.NewReader(compressed.Bytes()))
				if err != nil {
					b.Fatal(err)
				}
				n, readErr := io.Copy(io.Discard, r)
				if readErr != nil || n != int64(len(payload)) {
					b.Fatalf("decoded %d bytes: %v", n, readErr)
				}
			}
		})
	}
}

func BenchmarkBzip2Compression(b *testing.B) {
	payload := benchmarkStreamPayload(8 << 20)
	for _, test := range []struct {
		name string
		new  func(io.Writer) (io.WriteCloser, error)
	}{
		{
			name: "Serial",
			new: func(dst io.Writer) (io.WriteCloser, error) {
				return dsnetbzip2.NewWriter(dst, &dsnetbzip2.WriterConfig{Level: 9})
			},
		},
		{
			name: "Parallel",
			new: func(dst io.Writer) (io.WriteCloser, error) {
				return newBzip2Writer(dst, 9)
			},
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				var dst benchmarkCountingWriter
				w, err := test.new(&dst)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := w.Write(payload); err != nil {
					b.Fatal(err)
				}
				if err := w.Close(); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(dst)/float64(len(payload)), "ratio")
			}
		})
	}
}

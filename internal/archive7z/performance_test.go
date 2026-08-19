package archive7z

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/inpadi/7zip/internal/security"
	"github.com/inpadi/7zip/internal/sevenzip"
)

const benchmarkPayloadSize = 2 << 20

type memoryWriteSeeker struct {
	data   []byte
	offset int64
}

func (w *memoryWriteSeeker) Write(p []byte) (int, error) {
	end := w.offset + int64(len(p))
	if end > int64(len(w.data)) {
		w.data = append(w.data, make([]byte, end-int64(len(w.data)))...)
	}
	copy(w.data[w.offset:end], p)
	w.offset = end
	return len(p), nil
}

func (w *memoryWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = w.offset + offset
	case io.SeekEnd:
		next = int64(len(w.data)) + offset
	default:
		return 0, fmt.Errorf("invalid seek origin %d", whence)
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek offset %d", next)
	}
	w.offset = next
	return next, nil
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

func buildBenchmarkArchive(b *testing.B, payload []byte, password string, level int) []byte {
	b.Helper()
	var dst memoryWriteSeeker
	w, err := newWriter(&dst, writerOptions{solid: true, password: password, level: level, method: "lzma2"})
	if err != nil {
		b.Fatal(err)
	}
	entry, err := w.create(writerFileHeader{Name: "payload.bin", Modified: time.Unix(0, 0)})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		b.Fatal(err)
	}
	if err := entry.Close(); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	return dst.data
}

func BenchmarkLZMA2Compression(b *testing.B) {
	payload := benchmarkPayload()
	for _, test := range []struct {
		name     string
		level    int
		password string
	}{
		{name: "Fast", level: 1},
		{name: "FastEncrypted", level: 1, password: "benchmark-password"},
		{name: "Normal", level: 5},
		{name: "NormalEncrypted", level: 5, password: "benchmark-password"},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				archive := buildBenchmarkArchive(b, payload, test.password, test.level)
				b.ReportMetric(float64(len(archive))/float64(len(payload)), "ratio")
			}
		})
	}
}

func BenchmarkLZMA2ParallelCompression(b *testing.B) {
	payload := bytes.Repeat(benchmarkPayload(), 8)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		archive := buildBenchmarkArchive(b, payload, "", 1)
		b.ReportMetric(float64(len(archive))/float64(len(payload)), "ratio")
	}
}

func BenchmarkLZMA2Decompression(b *testing.B) {
	payload := benchmarkPayload()
	for _, test := range []struct {
		name     string
		level    int
		password string
	}{
		{name: "Fast", level: 1},
		{name: "FastEncrypted", level: 1, password: "benchmark-password"},
		{name: "Normal", level: 5},
		{name: "NormalEncrypted", level: 5, password: "benchmark-password"},
	} {
		archive := buildBenchmarkArchive(b, payload, test.password, test.level)
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				reader, err := sevenzip.NewReaderWithPassword(bytes.NewReader(archive), int64(len(archive)), test.password)
				if err != nil {
					b.Fatal(err)
				}
				var budget security.Budget
				if _, _, err := readFile(reader.File[0], io.Discard, &budget); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

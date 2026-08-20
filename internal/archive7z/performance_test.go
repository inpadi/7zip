package archive7z

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// BenchmarkExternalExtraction measures a real archive supplied by the caller.
// It is skipped during normal test runs.
func BenchmarkExternalExtraction(b *testing.B) {
	archive := os.Getenv("I7Z_BENCH_ARCHIVE")
	if archive == "" {
		b.Skip("set I7Z_BENCH_ARCHIVE to a 7z archive")
	}
	entries, err := List(archive, "", nil)
	if err != nil {
		b.Fatal(err)
	}
	var size int64
	for _, entry := range entries {
		if entry.Mode.IsRegular() {
			size += int64(entry.Size)
		}
	}

	b.Run("Test", func(b *testing.B) {
		b.SetBytes(size)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := Test(archive, "", nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, benchmark := range []struct {
		name string
		mode PublicationMode
	}{
		{name: "Direct", mode: PublicationDirect},
		{name: "Atomic", mode: PublicationAtomic},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			root := b.TempDir()
			b.SetBytes(size)
			b.ReportAllocs()
			iteration := 0
			for b.Loop() {
				output := filepath.Join(root, fmt.Sprintf("output-%d", iteration))
				iteration++
				if _, err := Extract(archive, ExtractOptions{OutputDir: output, Overwrite: OverwriteAll, Publication: benchmark.mode}); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				removeErr := os.RemoveAll(output)
				b.StartTimer()
				if removeErr != nil {
					b.Fatal(removeErr)
				}
			}
		})
	}
	if reference := os.Getenv("SEVENZIP_BENCH_PATH"); reference != "" {
		b.Run("Reference7Zip", func(b *testing.B) {
			root := b.TempDir()
			b.SetBytes(size)
			var cpu time.Duration
			iteration := 0
			for b.Loop() {
				output := filepath.Join(root, fmt.Sprintf("output-%d", iteration))
				iteration++
				command := exec.Command(reference, "x", "-bd", "-y", "-o"+output, archive)
				command.Stdout = io.Discard
				command.Stderr = io.Discard
				if err := command.Run(); err != nil {
					b.Fatal(err)
				}
				cpu += command.ProcessState.UserTime() + command.ProcessState.SystemTime()
				b.StopTimer()
				removeErr := os.RemoveAll(output)
				b.StartTimer()
				if removeErr != nil {
					b.Fatal(removeErr)
				}
			}
			b.ReportMetric(cpu.Seconds()/float64(b.N), "cpu-sec/op")
		})
	}
}

func BenchmarkExternalExtractorsPaired(b *testing.B) {
	archive := os.Getenv("I7Z_BENCH_ARCHIVE")
	i7z := os.Getenv("I7Z_BENCH_EXECUTABLE")
	reference := os.Getenv("SEVENZIP_BENCH_PATH")
	if archive == "" || i7z == "" || reference == "" {
		b.Skip("set I7Z_BENCH_ARCHIVE, I7Z_BENCH_EXECUTABLE, and SEVENZIP_BENCH_PATH")
	}
	type measurement struct {
		wall []time.Duration
		cpu  []time.Duration
	}
	measurements := map[string]*measurement{
		"i7z":   {},
		"7-Zip": {},
	}
	type tool struct {
		name string
		path string
		args func(string) []string
	}
	tools := []tool{
		{name: "i7z", path: i7z, args: func(output string) []string {
			return []string{"x", "-ba", "-y", "-o" + output, archive}
		}},
		{name: "7-Zip", path: reference, args: func(output string) []string {
			return []string{"x", "-bd", "-y", "-o" + output, archive}
		}},
	}
	root := b.TempDir()
	iteration := 0
	for b.Loop() {
		order := tools
		if iteration%2 != 0 {
			order = []tool{tools[1], tools[0]}
		}
		for _, current := range order {
			output := filepath.Join(root, fmt.Sprintf("%s-%d", current.name, iteration))
			command := exec.Command(current.path, current.args(output)...)
			command.Stdout = io.Discard
			command.Stderr = io.Discard
			start := time.Now()
			if err := command.Run(); err != nil {
				b.Fatal(err)
			}
			measurement := measurements[current.name]
			measurement.wall = append(measurement.wall, time.Since(start))
			measurement.cpu = append(measurement.cpu, command.ProcessState.UserTime()+command.ProcessState.SystemTime())
			b.StopTimer()
			removeErr := os.RemoveAll(output)
			b.StartTimer()
			if removeErr != nil {
				b.Fatal(removeErr)
			}
		}
		iteration++
	}
	for name, measurement := range measurements {
		b.ReportMetric(medianDuration(measurement.wall).Seconds(), name+"-wall-sec")
		b.ReportMetric(medianDuration(measurement.cpu).Seconds(), name+"-cpu-sec")
	}
}

func medianDuration(values []time.Duration) time.Duration {
	slices.Sort(values)
	middle := len(values) / 2
	if len(values)%2 != 0 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

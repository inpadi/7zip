package security

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const MaxOperationDuration = 30 * time.Minute

const copyBufferSize = 256 << 10

var copyBufferPool = sync.Pool{
	New: func() any {
		buffer := make([]byte, copyBufferSize)
		return &buffer
	},
}

const (
	MaxArchiveEntries     = 100_000
	MaxMetadataItems      = 1_000_000
	MaxMetadataBytes      = 64 << 20
	MaxDecoderMemory      = 256 << 20
	MaxListFileBytes      = 16 << 20
	MaxArchiveVolumes     = 1_024
	MaxCompressionRatio   = 1_000
	CompressionRatioSlack = int64(1 << 20)
	MaxFileBytes          = int64(16 << 30)
	MaxArchiveBytes       = int64(64 << 30)
	MaxTotalBytes         = int64(64 << 30)
	MaxNestingDepth       = 128
)

// Budget limits the amount of untrusted expanded data processed by one command.
type Budget struct {
	entries  int
	bytes    int64
	maxBytes int64
	deadline time.Time
}

func (b *Budget) SetCompressedBytes(compressed int64) {
	b.maxBytes = MaxTotalBytes
	if compressed < 0 {
		return
	}
	if compressed > (MaxTotalBytes-CompressionRatioSlack)/MaxCompressionRatio {
		return
	}
	b.maxBytes = min(MaxTotalBytes, compressed*MaxCompressionRatio+CompressionRatioSlack)
}

func CheckCompressionRatio(name string, expanded, compressed uint64) error {
	if expanded <= uint64(CompressionRatioSlack) || compressed > (^uint64(0)-uint64(CompressionRatioSlack))/MaxCompressionRatio {
		return nil
	}
	if expanded > compressed*MaxCompressionRatio+uint64(CompressionRatioSlack) {
		return fmt.Errorf("archive entry %q exceeds the %d:1 compression-ratio limit", name, MaxCompressionRatio)
	}
	return nil
}

func (b *Budget) AddEntry(name string, declared uint64) error {
	if b.entries >= MaxArchiveEntries {
		return fmt.Errorf("archive contains more than %d entries", MaxArchiveEntries)
	}
	b.entries++
	if declared > uint64(MaxFileBytes) {
		return fmt.Errorf("archive entry %q exceeds the %d-byte file limit", name, MaxFileBytes)
	}
	return nil
}

func (b *Budget) Copy(dst io.Writer, src io.Reader, name string) (int64, error) {
	if b.deadline.IsZero() {
		b.deadline = time.Now().Add(MaxOperationDuration)
	}
	maximum := b.maxBytes
	if maximum == 0 {
		maximum = MaxTotalBytes
	}
	remaining := maximum - b.bytes
	if remaining <= 0 {
		return 0, fmt.Errorf("archive exceeds the %d-byte expanded-data limit", maximum)
	}
	limit := min(MaxFileBytes, remaining)
	reader := &io.LimitedReader{R: deadlineReader{reader: src, deadline: b.deadline}, N: limit + 1}
	buffer := copyBufferPool.Get().(*[]byte)
	n, err := io.CopyBuffer(writerOnly{Writer: dst}, reader, *buffer)
	copyBufferPool.Put(buffer)
	if n > limit {
		return n, fmt.Errorf("archive entry %q exceeds its extraction budget", name)
	}
	b.bytes += n
	if err != nil {
		return n, err
	}
	return n, nil
}

type writerOnly struct{ io.Writer }

type deadlineReader struct {
	reader   io.Reader
	deadline time.Time
}

func (r deadlineReader) Read(p []byte) (int, error) {
	if time.Now().After(r.deadline) {
		return 0, fmt.Errorf("archive operation exceeded the %s processing deadline", MaxOperationDuration)
	}
	return r.reader.Read(p)
}

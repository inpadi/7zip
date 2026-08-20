package archivefmt

import (
	"bytes"
	"compress/bzip2"
	"errors"
	"io"
	"testing"

	"github.com/inpadi/7zip/internal/xz"
)

type copyChunkEncoder struct{ buffer []byte }

func (e *copyChunkEncoder) Encode(src []byte) error {
	e.buffer = append(e.buffer[:0], src...)
	return nil
}

func (e *copyChunkEncoder) Bytes() []byte { return e.buffer }

func TestParallelStreamWriterPreservesOrder(t *testing.T) {
	var dst bytes.Buffer
	w := newParallelStreamWriter(&dst, 3, []chunkEncoder{
		&copyChunkEncoder{},
		&copyChunkEncoder{},
	})
	for _, part := range []string{"a", "bcdef", "ghij"} {
		if _, err := io.WriteString(w, part); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := dst.String(), "abcdefghij"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, errParallelWriterClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestParallelBzip2WriterRoundTrip(t *testing.T) {
	payload := benchmarkStreamPayload(350_001)
	var compressed bytes.Buffer
	w := newParallelStreamWriter(&compressed, 100_000, []chunkEncoder{
		&bzip2ChunkEncoder{level: 1},
		&bzip2ChunkEncoder{level: 1},
	})
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(compressed.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatalf("decoded %d bytes; want %d", len(decoded), len(payload))
	}
}

func TestParallelXZWriterRoundTrip(t *testing.T) {
	payload := benchmarkStreamPayload(350_001)
	config := xz.WriterConfig{DictCap: 1 << 20}
	var compressed bytes.Buffer
	w := newParallelStreamWriter(&compressed, 100_000, []chunkEncoder{
		&xzChunkEncoder{config: config},
		&xzChunkEncoder{config: config},
	})
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := xz.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatalf("decoded %d bytes; want %d", len(decoded), len(payload))
	}
}

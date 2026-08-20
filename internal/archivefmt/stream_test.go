package archivefmt

import (
	"bytes"
	"io"
	"strconv"
	"testing"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/inpadi/7zip/internal/xz"
)

type countingReader struct {
	reader io.Reader
	reads  int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func TestXZReaderAvoidsByteAtATimeInput(t *testing.T) {
	payload := bytes.Repeat([]byte("buffered XZ input\n"), 32*1024)
	var archive bytes.Buffer
	writer, err := xz.NewWriter(&archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	input := &countingReader{reader: bytes.NewReader(archive.Bytes())}
	reader, err := newXZReader(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded payload differs from input")
	}
	if input.reads*4 >= archive.Len() {
		t.Fatalf("compressed input required %d reads for %d bytes", input.reads, archive.Len())
	}
}

func TestBzip2BlockLevel(t *testing.T) {
	tests := []struct {
		level int
		want  int
	}{
		{level: -1, want: 9},
		{level: 0, want: 1},
		{level: 1, want: 1},
		{level: 2, want: 3},
		{level: 3, want: 5},
		{level: 4, want: 7},
		{level: 5, want: 9},
		{level: 7, want: 9},
		{level: 9, want: 9},
	}
	for _, test := range tests {
		if got := bzip2BlockLevel(test.level); got != test.want {
			t.Errorf("bzip2BlockLevel(%d) = %d; want %d", test.level, got, test.want)
		}
	}
}

func TestBzip2ReaderConcatenatedStreams(t *testing.T) {
	first := bytes.Repeat([]byte("first bzip2 stream\n"), 10_000)
	second := bytes.Repeat([]byte("second bzip2 stream\n"), 10_000)
	var archive bytes.Buffer
	writeBzip2TestStream(t, &archive, first)
	writeBzip2TestStream(t, &archive, second)

	r, err := dsnetbzip2.NewReader(bytes.NewReader(archive.Bytes()), nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), first...), second...)
	if !bytes.Equal(decoded, want) {
		t.Fatalf("decoded %d bytes; want %d", len(decoded), len(want))
	}
}

func TestBzip2ReaderRejectsTruncation(t *testing.T) {
	payload := bytes.Repeat([]byte("truncated bzip2 stream\n"), 20_000)
	var archive bytes.Buffer
	writeBzip2TestStream(t, &archive, payload)
	for _, size := range []int{4, archive.Len() / 2, archive.Len() - 8, archive.Len() - 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			r, err := dsnetbzip2.NewReader(bytes.NewReader(archive.Bytes()[:size]), nil)
			if err != nil {
				return
			}
			if _, err := io.Copy(io.Discard, r); err == nil {
				t.Fatalf("accepted stream truncated to %d of %d bytes", size, archive.Len())
			}
		})
	}
}

func writeBzip2TestStream(t *testing.T, dst io.Writer, payload []byte) {
	t.Helper()
	w, err := dsnetbzip2.NewWriter(dst, &dsnetbzip2.WriterConfig{Level: 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

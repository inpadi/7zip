package archivefmt

import (
	"bytes"
	"io"
	"testing"

	"github.com/ulikunitz/xz"
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

package archivefmt

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
)

func writeBzip2TestFile(t *testing.T, path string, payload []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := dsnetbzip2.NewWriter(file, &dsnetbzip2.WriterConfig{Level: 9})
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenBzip2StreamUsesBoundedReader(t *testing.T) {
	payload := bytes.Repeat([]byte("standalone bzip2\n"), 100_000)
	path := filepath.Join(t.TempDir(), "payload.bz2")
	writeBzip2TestFile(t, path, payload)

	input, err := openStream(path, FormatBzip2)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(input.reader)
	closeErr := input.close()
	if err := errorsJoin(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded %d bytes; want %d", len(got), len(payload))
	}
}

func TestOpenTarBzip2UsesBoundedReader(t *testing.T) {
	payload := bytes.Repeat([]byte("tar bzip2\n"), 100_000)
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "payload.txt", Mode: 0o644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "payload.tar.bz2")
	writeBzip2TestFile(t, path, archive.Bytes())
	input, err := openTar(path, FormatTarBzip2)
	if err != nil {
		t.Fatal(err)
	}
	header, nextErr := input.reader.Next()
	if nextErr != nil {
		input.close()
		t.Fatal(nextErr)
	}
	got, readErr := io.ReadAll(input.reader)
	closeErr := input.close()
	if err := errorsJoin(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if header.Name != "payload.txt" || !bytes.Equal(got, payload) {
		t.Fatalf("decoded %q (%d bytes); want payload.txt (%d bytes)", header.Name, len(got), len(payload))
	}
}

func errorsJoin(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

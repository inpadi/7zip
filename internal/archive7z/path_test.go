package archive7z

import (
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDestinationRejectsUnsafeNames(t *testing.T) {
	root := t.TempDir()
	unsafe := []string{"", ".", "..", "../outside", "/absolute", `C:\absolute`, "a/../../outside", "a\x00b"}
	for _, name := range unsafe {
		if _, _, err := destination(root, name, false); err == nil {
			t.Errorf("destination(%q) unexpectedly succeeded", name)
		}
	}
}

func TestDestinationStaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	target, relative, err := destination(root, `folder\file.txt`, false)
	if err != nil {
		t.Fatal(err)
	}
	if relative != filepath.Join("folder", "file.txt") {
		t.Fatalf("relative = %q", relative)
	}
	want := filepath.Join(root, "folder", "file.txt")
	if target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}
}

func TestDestinationFlattens(t *testing.T) {
	root := t.TempDir()
	_, relative, err := destination(root, "folder/file.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if relative != "file.txt" {
		t.Fatalf("relative = %q, want file.txt", relative)
	}
}

func TestWindowsReservedName(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path rule")
	}
	if _, _, err := destination(t.TempDir(), "CON.txt", false); err == nil {
		t.Fatal("reserved device name unexpectedly accepted")
	}
}

func TestWriteUint64RoundTrip(t *testing.T) {
	values := []uint64{0, 0x7f, 0x80, 0x3fff, 0x4000, 0x1fffff, 1 << 32, 1 << 56, ^uint64(0)}
	for _, value := range values {
		var encoded byteBuffer
		writeUint64(&encoded, value)
		got := decodeUint64ForTest(t, encoded.data)
		if got != value {
			t.Errorf("round trip %d = %d (bytes %x)", value, got, encoded.data)
		}
	}
}

type byteBuffer struct{ data []byte }

func (b *byteBuffer) WriteByte(value byte) error {
	b.data = append(b.data, value)
	return nil
}

func decodeUint64ForTest(t *testing.T, data []byte) uint64 {
	t.Helper()
	first := data[0]
	mask := byte(0x80)
	var value uint64
	for i := 0; i < 8; i++ {
		if first&mask == 0 {
			value |= uint64(first&byte(mask-1)) << (8 * i)
			return value
		}
		value |= uint64(data[i+1]) << (8 * i)
		mask >>= 1
	}
	return value
}

func TestCRC32Combine(t *testing.T) {
	t.Parallel()

	parts := [][]byte{
		[]byte("first archive entry"),
		nil,
		bytes.Repeat([]byte{0x00, 0x7f, 0xff}, 1000),
		[]byte("last archive entry"),
	}
	var combined uint32
	var all []byte
	for _, part := range parts {
		combined = crc32Combine(combined, crc32.ChecksumIEEE(part), uint64(len(part)))
		all = append(all, part...)
	}
	if want := crc32.ChecksumIEEE(all); combined != want {
		t.Fatalf("combined CRC = %08x, want %08x", combined, want)
	}
}

func TestChecksumReaderWriteTo(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("checksum payload"), 10_000)
	reader := &checksumReader{reader: bytes.NewReader(payload)}
	var dst bytes.Buffer
	written, err := io.Copy(&dst, reader)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written = %d, want %d", written, len(payload))
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatal("copied payload differs from input")
	}
	if want := crc32.ChecksumIEEE(payload); reader.checksum != want {
		t.Fatalf("checksum = %08x, want %08x", reader.checksum, want)
	}

	reader = &checksumReader{reader: bytes.NewReader(payload)}
	if _, err := io.Copy(shortWriter{}, reader); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short copy error = %v, want %v", err, io.ErrShortWrite)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return max(len(p)-1, 0), nil
}

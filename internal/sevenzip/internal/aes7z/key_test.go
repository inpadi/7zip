package aes7z

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"
)

func TestCalculateKeyMatchesReference(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		password string
		cycles   int
		salt     []byte
	}{
		{name: "ASCII", password: "correct horse", cycles: 3},
		{name: "Unicode", password: "smile \U0001f600", cycles: 5, salt: []byte{1, 2, 3, 4}},
		{name: "Raw", password: "raw key", cycles: 0x3f, salt: []byte("salt")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := calculateKey(test.password, test.cycles, test.salt)
			if err != nil {
				t.Fatal(err)
			}
			want := referenceKey(test.password, test.cycles, test.salt)
			if string(got) != string(want) {
				t.Fatalf("key = %x, want %x", got, want)
			}
		})
	}
}

func referenceKey(password string, cycles int, salt []byte) []byte {
	prefix := append([]byte(nil), salt...)
	for _, value := range utf16.Encode([]rune(password)) {
		prefix = binary.LittleEndian.AppendUint16(prefix, value)
	}
	key := make([]byte, sha256.Size)
	if cycles == 0x3f {
		copy(key, prefix)
		return key
	}
	h := sha256.New()
	var counter [8]byte
	for i := range uint64(1 << cycles) {
		binary.LittleEndian.PutUint64(counter[:], i)
		_, _ = h.Write(prefix)
		_, _ = h.Write(counter[:])
	}
	return h.Sum(nil)
}

func TestCalculateKeyRejectsExcessiveCycles(t *testing.T) {
	t.Parallel()
	if _, err := calculateKey("password", maxCyclesPower+1, nil); !errors.Is(err, errCyclesPowerTooLarge) {
		t.Fatalf("calculateKey error = %v, want %v", err, errCyclesPowerTooLarge)
	}
}

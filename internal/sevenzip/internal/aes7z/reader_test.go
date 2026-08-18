package aes7z

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
	"strconv"
	"testing"
)

func TestReaderHandlesArbitraryBufferSizes(t *testing.T) {
	t.Parallel()

	plaintext := bytes.Repeat([]byte("0123456789abcdef"), 513)
	const password = "buffer-test"
	properties, ciphertext := encryptForTest(t, plaintext, password)

	for _, size := range []int{1, 7, aes.BlockSize, 31, 4096, len(plaintext) + 100} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			reader := newTestReader(t, properties, ciphertext, password)
			var decoded bytes.Buffer
			buffer := make([]byte, size)
			if _, err := io.CopyBuffer(&decoded, reader, buffer); err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded.Bytes(), plaintext) {
				t.Fatalf("decoded %d bytes, want %d", decoded.Len(), len(plaintext))
			}
		})
	}
}

func TestReaderRejectsPartialCipherBlock(t *testing.T) {
	t.Parallel()

	properties, ciphertext := encryptForTest(t, []byte("0123456789abcdef"), "password")
	reader := newTestReader(t, properties, ciphertext[:len(ciphertext)-1], "password")
	_, err := io.ReadAll(reader)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAll error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func encryptForTest(t *testing.T, plaintext []byte, password string) ([]byte, []byte) {
	t.Helper()
	if len(plaintext)%aes.BlockSize != 0 {
		t.Fatal("test plaintext must contain complete AES blocks")
	}
	const cycles = 2
	iv := bytes.Repeat([]byte{0x5a}, aes.BlockSize)
	key, err := calculateKey(password, cycles, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := append([]byte(nil), plaintext...)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, ciphertext)
	properties := append([]byte{cycles | 0x40, aes.BlockSize - 1}, iv...)
	return properties, ciphertext
}

func newTestReader(t *testing.T, properties, ciphertext []byte, password string) *readCloser {
	t.Helper()
	reader, err := NewReader(properties, uint64(len(ciphertext)), []io.ReadCloser{
		io.NopCloser(bytes.NewReader(ciphertext)),
	})
	if err != nil {
		t.Fatal(err)
	}
	rc := reader.(*readCloser)
	if err := rc.Password(password); err != nil {
		t.Fatal(err)
	}
	return rc
}

// Package aes7z implements the 7-zip AES decryption.
package aes7z

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
)

var (
	errAlreadyClosed          = errors.New("aes7z: already closed")
	errNeedOneReader          = errors.New("aes7z: need exactly one reader")
	errInsufficientProperties = errors.New("aes7z: not enough properties")
	errNoPasswordSet          = errors.New("aes7z: no password set")
	errUnsupportedMethod      = errors.New("aes7z: unsupported compression method")
)

type readCloser struct {
	rc       io.ReadCloser
	salt, iv []byte
	cycles   int
	cbc      cipher.BlockMode
	pending  [aes.BlockSize]byte
	start    int
	end      int
}

func (rc *readCloser) Close() error {
	if rc.rc == nil {
		return errAlreadyClosed
	}

	if err := rc.rc.Close(); err != nil {
		return fmt.Errorf("aes7z: error closing: %w", err)
	}

	rc.rc = nil

	return nil
}

func (rc *readCloser) Password(p string) error {
	key, err := calculateKey(p, rc.cycles, rc.salt)
	if err != nil {
		return err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("aes7z: error creating cipher: %w", err)
	}

	rc.cbc = cipher.NewCBCDecrypter(block, rc.iv)

	return nil
}

func (rc *readCloser) Read(p []byte) (int, error) {
	if rc.rc == nil {
		return 0, errAlreadyClosed
	}

	if rc.cbc == nil {
		return 0, errNoPasswordSet
	}
	if len(p) == 0 {
		return 0, nil
	}

	n := copy(p, rc.pending[rc.start:rc.end])
	rc.start += n
	if rc.start == rc.end {
		rc.start, rc.end = 0, 0
	}
	if n == len(p) {
		return n, nil
	}

	complete := (len(p) - n) / aes.BlockSize * aes.BlockSize
	if complete > 0 {
		read, err := io.ReadFull(rc.rc, p[n:n+complete])
		if read%aes.BlockSize != 0 {
			return n, fmt.Errorf("aes7z: error reading block: %w", io.ErrUnexpectedEOF)
		}
		if read > 0 {
			rc.cbc.CryptBlocks(p[n:n+read], p[n:n+read])
			n += read
		}
		if err != nil {
			if n > 0 && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				return n, nil
			}
			if errors.Is(err, io.EOF) {
				return 0, io.EOF
			}
			return n, fmt.Errorf("aes7z: error reading block: %w", err)
		}
	}

	if n < len(p) {
		if _, err := io.ReadFull(rc.rc, rc.pending[:]); err != nil {
			if n > 0 && errors.Is(err, io.EOF) {
				return n, nil
			}
			if errors.Is(err, io.EOF) {
				return 0, io.EOF
			}
			return n, fmt.Errorf("aes7z: error reading block: %w", err)
		}
		rc.cbc.CryptBlocks(rc.pending[:], rc.pending[:])
		copied := copy(p[n:], rc.pending[:])
		n += copied
		rc.start, rc.end = copied, aes.BlockSize
	}
	return n, nil
}

// NewReader returns a new AES-256-CBC & SHA-256 io.ReadCloser. The Password
// method must be called before attempting to call Read so that the block
// cipher is correctly initialised.
func NewReader(p []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 {
		return nil, errNeedOneReader
	}

	// Need at least two bytes initially
	if len(p) < 2 {
		return nil, errInsufficientProperties
	}

	if p[0]&0xc0 == 0 {
		return nil, errUnsupportedMethod
	}

	rc := new(readCloser)

	salt := p[0]>>7&1 + p[1]>>4
	iv := p[0]>>6&1 + p[1]&0x0f

	if len(p) != int(2+salt+iv) {
		return nil, errInsufficientProperties
	}

	rc.salt = p[2 : 2+salt]
	rc.iv = make([]byte, aes.BlockSize)
	copy(rc.iv, p[2+salt:])

	rc.cycles = int(p[0] & 0x3f)
	rc.rc = readers[0]

	return rc, nil
}

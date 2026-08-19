//go:build 386 || arm || mips || mipsle || wasm

package archivefmt

import (
	"bufio"
	"io"

	"github.com/inpadi/7zip/internal/security"
	"github.com/ulikunitz/xz"
)

const xzInputBufferSize = 64 * 1024

func newXZReader(input io.Reader) (io.ReadCloser, error) {
	// The v1 range decoder reads through io.ByteReader. Buffer it so each
	// compressed byte does not become an individual file read.
	reader, err := (xz.ReaderConfig{DictCap: security.MaxDecoderMemory}).NewReader(bufio.NewReaderSize(input, xzInputBufferSize))
	if err != nil {
		return nil, err
	}
	return io.NopCloser(reader), nil
}

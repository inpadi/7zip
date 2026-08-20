//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package archivefmt

import (
	"bufio"
	"io"

	"github.com/inpadi/7zip/internal/security"
	"github.com/inpadi/7zip/internal/xz"
)

const xzInputBufferSize = 64 * 1024

func newXZReader(input io.Reader) (io.ReadCloser, error) {
	reader, err := (xz.ReaderConfig{DictCap: security.MaxDecoderMemory}).NewReader(bufio.NewReaderSize(input, xzInputBufferSize))
	if err != nil {
		return nil, err
	}
	return reader, nil
}

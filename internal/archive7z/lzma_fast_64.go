//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package archive7z

import (
	"io"

	"github.com/inpadi/7zip/internal/xz/lzma"
)

// newFastLZMA2Writer uses the stable, bounded-memory LZMA2 writer.
func newFastLZMA2Writer(dst io.Writer, dictionarySize int) (io.WriteCloser, error) {
	return (lzma.Writer2Config{DictCap: dictionarySize}).NewWriter2(dst)
}

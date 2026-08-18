//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package archive7z

import (
	"io"
	"runtime"

	lzmav2 "github.com/ulikunitz/xz/v2/lzma"
)

// newFastLZMA2Writer trades some compression ratio for bounded parallelism at -mx=1/2.
func newFastLZMA2Writer(dst io.Writer, dictionarySize int) (io.WriteCloser, error) {
	workers := min(runtime.GOMAXPROCS(0), 8)
	return lzmav2.NewWriter2Options(dst, lzmav2.Writer2Options{
		WindowSize:    dictionarySize,
		BufferSize:    2 * dictionarySize,
		Workers:       workers,
		ParserOptions: doubleHashParserOptions{},
	})
}

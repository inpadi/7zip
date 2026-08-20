//go:build 386 || arm || mips || mipsle || wasm

package archive7z

import (
	"io"

	"github.com/inpadi/7zip/internal/xz/lzma"
)

// The xz/v2 lz dependency does not compile on 32-bit targets, and its parallel
// buffers are a poor fit for their address space. Keep the stable writer there.
func newFastLZMA2Writer(dst io.Writer, dictionarySize int) (io.WriteCloser, error) {
	return (lzma.Writer2Config{DictCap: dictionarySize}).NewWriter2(dst)
}

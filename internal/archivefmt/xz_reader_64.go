//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package archivefmt

import (
	"io"
	"runtime"

	xzv2 "github.com/ulikunitz/xz/v2"
)

func newXZReader(input io.Reader) (io.ReadCloser, error) {
	return xzv2.NewReaderOptions(input, xzv2.ReaderOptions{
		Workers: min(runtime.GOMAXPROCS(0), 8),
	})
}

//go:build cgo && !purego && !noasm && windows && amd64

package lzmadec

/*
#cgo CFLAGS: -DZ7_LZMA_DEC_OPT
*/
import "C"

import _ "github.com/inpadi/7zip/internal/native/lzmadec/asmwin64"

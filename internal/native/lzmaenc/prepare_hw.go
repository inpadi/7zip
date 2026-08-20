//go:build cgo && !purego && !noasm && (amd64 || arm64)

package lzmaenc

/*
#include "native_encoder.h"
*/
import "C"

import "sync"

var hardwareOnce sync.Once

func prepareHardware() {
	hardwareOnce.Do(func() {
		C.i7z_lzma_encoder_prepare_hardware()
	})
}

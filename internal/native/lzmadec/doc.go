// Package lzmadec provides an optional native LZMA and LZMA2 decoder.
//
// The native implementation uses the public-domain 7-Zip SDK decoder when
// cgo is enabled. Builds made with CGO_ENABLED=0 or the purego build tag keep
// the portable Go decoder selected by the caller. The noasm build tag retains
// the native C decoder while disabling architecture-specific assembly.
package lzmadec

// Package lzmaenc provides optional native LZMA and LZMA2 encoders.
//
// The native implementation uses the public-domain 7-Zip SDK BT/optimal
// encoder on 64-bit x86 and ARM builds with cgo enabled. The purego build tag
// or CGO_ENABLED=0 leaves selection of the portable encoder to the caller.
package lzmaenc

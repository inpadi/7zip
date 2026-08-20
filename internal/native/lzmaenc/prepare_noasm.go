//go:build cgo && !purego && noasm && (amd64 || arm64)

package lzmaenc

// Leaving the SDK function table unprepared selects its scalar match-finder
// normalization path. The archive layer still has the pure-Go fallback.
func prepareHardware() {}

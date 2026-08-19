package sevenzip

import "testing"

func TestIA64DecompressorRegistered(t *testing.T) {
	if decompressor([]byte{0x03, 0x03, 0x04, 0x01}) == nil {
		t.Fatal("IA-64 decompressor is not registered")
	}
}

package bra

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestIA64RoundTrip(t *testing.T) {
	original := make([]byte, 3*ia64Alignment)
	for i := range original {
		original[i] = byte(i*37 + 11)
	}

	// Template 0x16 selects branch slots 1 through 3. These words satisfy the
	// IA-64 branch opcode checks and exercise address conversion in each bundle.
	for bundle := 0; bundle < len(original); bundle += ia64Alignment {
		original[bundle] = 0x16
		for _, slot := range []int{1, 2} {
			offset := bundle + slot*5 - 4
			original[offset] = 0
			binary.LittleEndian.PutUint32(original[offset+1:], uint32(0x0a000000)<<slot)
		}
	}

	encoded := append([]byte(nil), original...)
	encoder := new(ia64)
	if got := encoder.Convert(encoded, true); got != len(encoded) {
		t.Fatalf("encoded %d bytes, want %d", got, len(encoded))
	}
	if bytes.Equal(encoded, original) {
		t.Fatal("encoder did not convert any branch addresses")
	}

	decoder := new(ia64)
	if got := decoder.Convert(encoded, false); got != len(encoded) {
		t.Fatalf("decoded %d bytes, want %d", got, len(encoded))
	}
	if !bytes.Equal(encoded, original) {
		t.Fatal("IA-64 branch conversion did not round trip")
	}
}

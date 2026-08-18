package wim

import (
	"bytes"
	"testing"
)

func TestEmptySecurityDescriptorTableWithZeroLength(t *testing.T) {
	var reader Reader
	descriptors, consumed, err := reader.readSecurityDescriptors(bytes.NewReader(make([]byte, securityblockDiskSize)))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("descriptors = %d, want 0", len(descriptors))
	}
	if consumed != securityblockDiskSize {
		t.Fatalf("consumed = %d, want %d", consumed, securityblockDiskSize)
	}
}

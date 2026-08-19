package vhd

import (
	"bytes"
	"testing"
)

func TestFixedDiskRejectsInvalidSectorRanges(t *testing.T) {
	disk := NewFixedDisk(bytes.NewReader(make([]byte, 2*SECTOR_SIZE)), &Footer{CurrentSize: SECTOR_SIZE})
	if _, err := disk.ReadSectors(-1, 1); err == nil {
		t.Fatal("expected an error for a negative sector")
	}
	if _, err := disk.ReadSectors(1, 1); err == nil {
		t.Fatal("expected an error for a sector beyond the virtual disk")
	}
}

package vhdx

import (
	"math"
	"testing"
)

func TestPartialRunIterHonorsStartingBit(t *testing.T) {
	var runs []PartialRun
	err := partialRunIter([]byte{0b00111100}, 2, 5, func(run *PartialRun) error {
		runs = append(runs, *run)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0] != (PartialRun{Type: 1, Count: 4}) || runs[1] != (PartialRun{Type: 0, Count: 1}) {
		t.Fatalf("unexpected runs: %#v", runs)
	}
}

func TestPartialRunIterRejectsOutOfRangeBitmap(t *testing.T) {
	if err := partialRunIter([]byte{0}, 7, 2, func(*PartialRun) error { return nil }); err == nil {
		t.Fatal("expected an error for a bitmap range extending beyond its buffer")
	}
}

func TestDataOffsetRejectsOverflowAndTruncation(t *testing.T) {
	v := &VHDX{fileSize: 2 * MB}
	if offset, err := v.dataOffset(1, 512, 512); err != nil || offset != MB+512 {
		t.Fatalf("valid extent: offset=%d err=%v", offset, err)
	}
	if _, err := v.dataOffset(math.MaxUint64, 0, 1); err == nil {
		t.Fatal("expected an error for an overflowing BAT offset")
	}
	if _, err := v.dataOffset(1, MB, 1); err == nil {
		t.Fatal("expected an error for an extent beyond the file")
	}
}

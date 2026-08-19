package vhd

import (
	"encoding/binary"
	"fmt"
	"io"
)

type BlockAllocationTable struct {
	fh         io.ReadSeeker
	offset     int64
	maxEntries int64
}

func NewBlockAllocationTable(fh io.ReadSeeker, offset, maxEntries int64) *BlockAllocationTable {
	return &BlockAllocationTable{fh: fh, offset: offset, maxEntries: maxEntries}
}

func (bat *BlockAllocationTable) Get(block int64) (uint32, error) {
	if block < 0 || block >= bat.maxEntries {
		return 0, fmt.Errorf("invalid block %d (max block is %d)", block, bat.maxEntries-1)
	}
	if bat.offset < 0 || block > (int64(^uint64(0)>>1)-bat.offset)/4 {
		return 0, fmt.Errorf("invalid allocation table offset for block %d", block)
	}
	_, err := bat.fh.Seek(bat.offset+block*4, io.SeekStart)
	if err != nil {
		return 0, err
	}

	var sectorOffset uint32
	if err := binary.Read(bat.fh, binary.BigEndian, &sectorOffset); err != nil {
		return 0, err
	}

	return sectorOffset, nil
}

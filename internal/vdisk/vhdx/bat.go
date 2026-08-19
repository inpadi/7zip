package vhdx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type batEntry struct {
	State        int
	FileOffsetMb uint64
}

type BlockAllocationTable struct {
	vhdx       *VHDX
	offset     int64
	chunkRatio int64
	pbCount    int64
	sbCount    int64
	entryCount int64
}

func NewBlockAllocationTable(vhdx *VHDX, offset, length int64) (*BlockAllocationTable, error) {
	pbCount := int64((vhdx.size + uint64(vhdx.blockSize) - 1) / uint64(vhdx.blockSize))
	sbCount := (pbCount + vhdx.chunkRatio - 1) / vhdx.chunkRatio
	entryCount := pbCount + ((pbCount - 1) / vhdx.chunkRatio)
	if vhdx.hasParent {
		if sbCount > math.MaxInt64/(vhdx.chunkRatio+1) {
			return nil, errors.New("VHDX BAT entry count overflows")
		}
		entryCount = sbCount * (vhdx.chunkRatio + 1)
	}
	if offset < 0 || length < 0 || entryCount <= 0 || entryCount > length/8 {
		return nil, errors.New("VHDX BAT does not cover the virtual disk")
	}

	return &BlockAllocationTable{
		vhdx:       vhdx,
		offset:     offset,
		chunkRatio: vhdx.chunkRatio,
		pbCount:    pbCount,
		sbCount:    sbCount,
		entryCount: entryCount,
	}, nil
}

func (bat *BlockAllocationTable) get(entry int64) (batEntry, error) {
	if entry < 0 || entry >= bat.entryCount {
		return batEntry{}, fmt.Errorf("invalid entry for BAT lookup: %d (max entry is %d)", entry, bat.entryCount-1)
	}
	if entry > (math.MaxInt64-bat.offset)/8 {
		return batEntry{}, fmt.Errorf("VHDX BAT offset overflows for entry %d", entry)
	}

	_, err := bat.vhdx.fh.Seek(bat.offset+entry*8, 0)
	if err != nil {
		return batEntry{}, err
	}
	var entryData [8]byte
	if err := binary.Read(bat.vhdx.fh, binary.LittleEndian, &entryData); err != nil {
		return batEntry{}, err
	}

	raw := binary.LittleEndian.Uint64(entryData[:])
	if raw&0xFFFF8 != 0 {
		return batEntry{}, fmt.Errorf("invalid reserved bits in VHDX BAT entry %d", entry)
	}
	return batEntry{State: int(raw & 7), FileOffsetMb: raw >> 20}, nil
}

func (bat *BlockAllocationTable) pb(block int64) (batEntry, error) {
	if block < 0 || block >= bat.pbCount {
		return batEntry{}, fmt.Errorf("invalid VHDX payload block %d", block)
	}
	sbEntries := block / bat.chunkRatio
	return bat.get(block + sbEntries)
}

func (bat *BlockAllocationTable) sb(block int64) (batEntry, error) {
	if block < 0 || block >= bat.pbCount {
		return batEntry{}, fmt.Errorf("invalid VHDX sector bitmap block %d", block)
	}
	numSb := block / bat.chunkRatio
	return bat.get((numSb+1)*bat.chunkRatio + numSb)
}

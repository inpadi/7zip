package sevenzip

import (
	"fmt"
	"io"

	"github.com/inpadi/7zip/internal/sevenzip/internal/bra"
)

// BranchFilter identifies a length-preserving executable branch filter.
type BranchFilter uint8

const (
	BranchFilterNone BranchFilter = iota
	BranchFilterARM64
	BranchFilterBCJ
	BranchFilterIA64
)

// NewBranchFilterWriter wraps destination with the requested encoder.
func NewBranchFilterWriter(destination io.WriteCloser, filter BranchFilter) (io.WriteCloser, error) {
	switch filter {
	case BranchFilterNone:
		return destination, nil
	case BranchFilterARM64:
		return bra.NewARM64Writer(destination)
	case BranchFilterBCJ:
		return bra.NewBCJWriter(destination)
	case BranchFilterIA64:
		return bra.NewIA64Writer(destination)
	default:
		return nil, fmt.Errorf("unsupported branch filter %d", filter)
	}
}

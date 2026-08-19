package bra

import (
	"encoding/binary"
	"io"
)

const ia64Alignment = 16

type ia64 struct {
	ip uint32
}

func (c *ia64) Size() int { return ia64Alignment }

// Convert implements 7-Zip's IA-64 branch address conversion. IA-64 stores
// three instruction slots in each 16-byte bundle, with the template byte
// selecting which slots can contain a branch.
func (c *ia64) Convert(b []byte, encoding bool) int {
	processed := len(b) & ^(ia64Alignment - 1)
	if processed == 0 {
		return 0
	}

	pc := c.ip - ia64Alignment
	pc >>= 3

	for bundle := 0; bundle < processed; bundle += ia64Alignment {
		mask := uint32(0x334b0000) >> (b[bundle] & 0x1e)
		pc += 2
		mask &= 3
		if mask == 0 {
			continue
		}

		for slot := mask; slot < 4; slot++ {
			offset := bundle + int(slot)*5 - 4
			t := binary.LittleEndian.Uint16(b[offset:])
			z := binary.LittleEndian.Uint32(b[offset+1:]) >> slot
			if (uint32(t)>>slot)&0xe0 != 0 || (z-0x0a000000)&0x1e000000 != 0 {
				continue
			}

			v := z & 0x011fffff
			z ^= v
			if encoding {
				v += pc & 0x003fffff
			} else {
				v -= pc | ^uint32(0x003fffff)
			}
			v &^= 0x00c00000
			v += 0x00e00000
			v &= 0x011fffff
			z |= v
			binary.LittleEndian.PutUint32(b[offset+1:], z<<slot)
		}
	}

	c.ip += uint32(processed) //nolint:gosec
	return processed
}

// NewIA64Reader returns a new IA-64 branch-filtering io.ReadCloser.
func NewIA64Reader(_ []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	return newReader(readers, new(ia64))
}

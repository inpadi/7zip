// Package xpress decodes the LZ77+Huffman variant of Microsoft XPRESS.
package xpress

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	huffmanSymbols     = 512
	huffmanTableBits   = 15
	huffmanTableLength = 1 << huffmanTableBits
)

var errTruncated = errors.New("truncated XPRESS data")

type input struct {
	data []byte
	pos  int
}

func (in *input) readByte() (byte, error) {
	if in.pos >= len(in.data) {
		return 0, errTruncated
	}
	value := in.data[in.pos]
	in.pos++
	return value, nil
}

func (in *input) readUint16() (uint16, error) {
	if in.pos+2 > len(in.data) {
		return 0, errTruncated
	}
	value := binary.LittleEndian.Uint16(in.data[in.pos:])
	in.pos += 2
	return value, nil
}

func symbolLength(lengths []byte, symbol int) int {
	if symbol%2 == 0 {
		return int(lengths[symbol/2] & 0x0f)
	}
	return int(lengths[symbol/2] >> 4)
}

// Decompress expands one XPRESS Huffman chunk to exactly outputSize bytes.
func Decompress(data []byte, outputSize int) ([]byte, error) {
	if outputSize < 0 {
		return nil, errors.New("negative XPRESS output size")
	}
	if outputSize == 0 {
		return []byte{}, nil
	}
	if len(data) < huffmanSymbols/2+4 {
		return nil, errTruncated
	}

	lengths := data[:huffmanSymbols/2]
	var table [huffmanTableLength]uint16
	tableIndex := 0
	for bitLength := 1; bitLength <= huffmanTableBits; bitLength++ {
		for symbol := 0; symbol < huffmanSymbols; symbol++ {
			if symbolLength(lengths, symbol) != bitLength {
				continue
			}
			entries := 1 << (huffmanTableBits - bitLength)
			if tableIndex+entries > len(table) {
				return nil, errors.New("invalid XPRESS Huffman table")
			}
			for i := 0; i < entries; i++ {
				table[tableIndex] = uint16(symbol)
				tableIndex++
			}
		}
	}
	if tableIndex != len(table) {
		return nil, errors.New("incomplete XPRESS Huffman table")
	}

	in := input{data: data, pos: huffmanSymbols / 2}
	first, err := in.readUint16()
	if err != nil {
		return nil, err
	}
	second, err := in.readUint16()
	if err != nil {
		return nil, err
	}
	bits := uint32(first)<<16 | uint32(second)
	extraBits := 16
	consume := func(count int) error {
		bits <<= count
		extraBits -= count
		if extraBits < 0 {
			word, readErr := in.readUint16()
			if readErr != nil {
				return readErr
			}
			bits |= uint32(word) << -extraBits
			extraBits += 16
		}
		return nil
	}

	output := make([]byte, 0, outputSize)
	for len(output) < outputSize {
		symbol := int(table[bits>>(32-huffmanTableBits)])
		length := symbolLength(lengths, symbol)
		if length == 0 {
			return nil, errors.New("invalid zero-length XPRESS symbol")
		}
		if err := consume(length); err != nil {
			return nil, err
		}

		if symbol < 256 {
			output = append(output, byte(symbol))
			continue
		}

		match := symbol - 256
		matchLength := match & 0x0f
		distanceBits := match >> 4
		if matchLength == 0x0f {
			value, readErr := in.readByte()
			if readErr != nil {
				return nil, readErr
			}
			if value == 0xff {
				extended, readErr := in.readUint16()
				if readErr != nil {
					return nil, readErr
				}
				matchLength = int(extended)
			} else {
				matchLength = int(value) + 0x0f
			}
		}
		matchLength += 3
		if matchLength > outputSize-len(output) {
			return nil, errors.New("XPRESS match exceeds output size")
		}

		distance := 1
		if distanceBits != 0 {
			distance = int(bits>>(32-distanceBits)) + 1<<distanceBits
			if err := consume(distanceBits); err != nil {
				return nil, err
			}
		}
		if distance > len(output) {
			return nil, fmt.Errorf("invalid XPRESS match distance %d at output offset %d", distance, len(output))
		}
		for i := 0; i < matchLength; i++ {
			output = append(output, output[len(output)-distance])
		}
	}
	return output, nil
}

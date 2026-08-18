//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package archive7z

import (
	"errors"
	"io"

	"github.com/ulikunitz/lz"
	oldlz "github.com/ulikunitz/lz/olz"
)

// doubleHashParserOptions adapts the older multi-candidate parser to xz/v2.
// Its match quality avoids the large ratio loss of v2's single-entry hash.
type doubleHashParserOptions struct{}

var (
	_ lz.Configurator = doubleHashParserOptions{}
	_ lz.Parser       = (*doubleHashParserAdapter)(nil)
)

func (options doubleHashParserOptions) NewParser(windowSize, retentionSize, bufferSize int) (lz.Parser, error) {
	config := oldlz.DHPConfig{
		InputLen1: 4,
		HashBits1: 16,
		InputLen2: 6,
		HashBits2: 19,
	}
	config.SetBufConfig(oldlz.BufferConfig{
		ShrinkSize: retentionSize,
		BufferSize: bufferSize,
		WindowSize: windowSize,
		BlockSize:  128 << 10,
	})
	parser, err := config.NewParser()
	if err != nil {
		return nil, err
	}
	return &doubleHashParserAdapter{
		parser:     parser,
		options:    options,
		windowSize: windowSize,
	}, nil
}

type doubleHashParserAdapter struct {
	parser     oldlz.Parser
	options    doubleHashParserOptions
	windowSize int
	oldBlock   oldlz.Block
}

func (p *doubleHashParserAdapter) Parse(block *lz.Block, n int, flags lz.ParserFlags) (int, error) {
	config := p.parser.ParserConfig()
	bufferConfig := config.BufConfig()
	bufferConfig.BlockSize = n
	config.SetBufConfig(bufferConfig)

	oldFlags := 0
	if flags&lz.NoTrailingLiterals != 0 {
		oldFlags |= oldlz.NoTrailingLiterals
	}
	parsed, err := p.parser.Parse(&p.oldBlock, oldFlags)
	block.Literals = append(block.Literals[:0], p.oldBlock.Literals...)
	block.Sequences = block.Sequences[:0]
	for _, sequence := range p.oldBlock.Sequences {
		block.Sequences = append(block.Sequences, lz.Seq{
			LitLen:   sequence.LitLen,
			MatchLen: sequence.MatchLen,
			Offset:   sequence.Offset,
			Aux:      sequence.Aux,
		})
	}
	return parsed, translateOldLZError(err)
}

func (p *doubleHashParserAdapter) Write(data []byte) (int, error) {
	n, err := p.parser.Write(data)
	// The old parser requires callers to reclaim parsed bytes explicitly.
	if errors.Is(err, oldlz.ErrFullBuffer) && p.parser.Shrink() > 0 {
		written, nextErr := p.parser.Write(data[n:])
		n += written
		err = nextErr
	}
	return n, translateOldLZError(err)
}

func (p *doubleHashParserAdapter) ReadFrom(r io.Reader) (int64, error) {
	var total int64
	for {
		n, err := p.parser.ReadFrom(r)
		total += n
		if !errors.Is(err, oldlz.ErrFullBuffer) || p.parser.Shrink() == 0 {
			return total, translateOldLZError(err)
		}
	}
}

func (p *doubleHashParserAdapter) ReadAt(data []byte, offset int64) (int, error) {
	n, err := p.parser.ReadAt(data, offset)
	return n, translateOldLZError(err)
}

func (p *doubleHashParserAdapter) ByteAt(offset int64) (byte, error) {
	value, err := p.parser.ByteAt(offset)
	return value, translateOldLZError(err)
}

func (p *doubleHashParserAdapter) Reset(data []byte) error {
	return translateOldLZError(p.parser.Reset(data))
}

func (p *doubleHashParserAdapter) WindowSize() int          { return p.windowSize }
func (p *doubleHashParserAdapter) Options() lz.Configurator { return p.options }

func translateOldLZError(err error) error {
	switch {
	case errors.Is(err, oldlz.ErrEmptyBuffer):
		return lz.ErrEndOfBuffer
	case errors.Is(err, oldlz.ErrFullBuffer):
		return lz.ErrFullBuffer
	case errors.Is(err, oldlz.ErrOutOfBuffer):
		return lz.ErrOutOfBuffer
	default:
		return err
	}
}

# Decoder provenance

The XPRESS LZ77+Huffman decoder is adapted from
`github.com/markkurossi/xpress` revision `15ca3e5bf77f`. The original MIT
license is retained in `LICENSE`.

The local implementation requires an exact caller-provided output size and
adds bounds checks for malformed Huffman tables, match lengths, and match
distances.

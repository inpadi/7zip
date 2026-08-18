# Decoder provenance

This package is derived from `github.com/bodgit/sevenzip` version 1.6.4.
It is kept in-tree so the 7z decoder can be audited, fuzzed, and evolved with
the rest of this port. The original BSD 3-Clause license is retained in
`LICENSE`.

Local changes include import-path relocation into
`github.com/inpadi/7zip/internal/sevenzip` and packed-size metadata used for
7-Zip-compatible console listings. Further compatibility changes must be
recorded here.

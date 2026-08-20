# pbzip2 provenance

The bounded block-parallel BZip2 reader uses the Go module
`github.com/cosnicolaou/pbzip2` v1.0.6, upstream commit
`44326437238d9b3ac57eeb1e4c5218db5179dcfb`:

https://github.com/cosnicolaou/pbzip2/tree/v1.0.6

pbzip2's parallel scanner and scheduler are Copyright 2019-2021 Cosmos
Nicolaou and are licensed under the Apache License, Version 2.0. The exact
upstream license text is retained in `third_party/pbzip2/LICENSE`. The v1.0.6
source distribution has no NOTICE file. Module content and checksums are
pinned by `go.mod` and `go.sum`.

The decoder under pbzip2's `internal/bzip2` is derived from Go's
`compress/bzip2` and retains `Copyright 2011 The Go Authors` and BSD-style
license headers. The Go BSD license and additional patent grant are retained
as `third_party/pbzip2/GO-LICENSE` and `third_party/pbzip2/GO-PATENTS`.

The scheduling and source-ownership wrapper under `internal/bzip2reader` is
local port code. It uses pbzip2's public Scanner and Decompressor APIs with a
two-block credit window to bound decoded buffers and make Close deterministic.
The upstream v1.0.6 scanner silently trims empty concatenated streams and its
secondary-header check admits level `0`; consequently an outputless `BZh0`
empty trailer is accepted. Observable non-empty secondary streams are checked
locally for the standard levels 1 through 9. Avoiding an unanchored byte-pattern
filter preserves valid compressed payloads and the decoder's hot path.

# Internal XZ fork

This directory contains the runtime packages from
[`github.com/ulikunitz/xz`](https://github.com/ulikunitz/xz), based on release
`v0.5.16` at commit `024f9092972afea64fa5c25157566881bac36938`.

The fork is internal so applications and libraries built from this module use
the same reviewed implementation. The upstream copyright and license are in
[`LICENSE`](LICENSE).

Local performance changes:

- represent encoder and decoder operations as concrete values, avoiding an
  interface allocation for each literal or match;
- reuse a bounded buffered reader across LZMA2 compressed chunks, avoiding
  byte-sized reads from the underlying stream;
- update range-coder probabilities through a native-width local value and a
  single write-back.

The focused upstream LZMA and rolling-hash tests and their small fixtures are
retained. Command packages, documentation, and the 10 MB root XZ benchmark
corpus are not copied because this module only embeds the XZ and LZMA runtime.
Validate an upstream update in a separate checkout before copying its runtime
changes and reapplying the local patches. Application-level performance
coverage lives in `internal/archive7z/performance_test.go`.

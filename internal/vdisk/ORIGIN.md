# Parser provenance

The VHD and VHDX readers are derived from `github.com/asalih/go-vdisk` at
commit `2408b989d511` (2026-06-29). The original MIT license is retained in
`LICENSE`.

This fork is limited to read-only logical-disk reconstruction. Local changes
add validation, bounded reads, correct sparse-block handling, and explicit
errors for unsupported differencing parents.

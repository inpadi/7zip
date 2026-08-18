# Parser provenance

This package is derived from `github.com/Microsoft/go-winio/wim` version
0.6.2. The original MIT license is retained in `LICENSE`.

The initial local changes relocate the LZX import and remove the Windows/Linux
build constraint from code that uses only portable Go APIs. Format behavior is
validated on Windows, Linux, and macOS by this repository's tests.

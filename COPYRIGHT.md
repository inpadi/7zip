# Copyright and acknowledgment

7-Zip Copyright (C) 1999-2026 Igor Pavlov.

7-Zip Go port modifications Copyright (c) 2026 7-Zip Go port contributors.

This is an independent, unofficial port of 7-Zip. It preserves the copyright
and license notices of the original project and of every incorporated
third-party component. Copyright in the original 7-Zip work remains with Igor
Pavlov and the other copyright holders identified in the source tree. No
upstream author's name is used to imply sponsorship or endorsement of this
port.

The port contributors gratefully thank Igor Pavlov, the original developer of
7-Zip, for his excellent work and for making 7-Zip available as free software.
The design, documentation, and long-running development of 7-Zip made this
port possible. The full legacy native reference source trees are not
distributed as part of this repository. A limited public-domain subset of the
official 7-Zip 26.02 SDK used by the optional native codec backends is retained
under `internal/native/sdk7z`, with exact source provenance documented there.

See `LICENSE` and the notices referenced there for the terms that apply to each
part of the repository.

The XZ and LZMA implementation under `internal/xz` is derived from
`github.com/ulikunitz/xz` v0.5.16. Its upstream copyright and BSD-style
license are retained in `internal/xz/LICENSE`.

Parallel BZip2 decoding uses `github.com/cosnicolaou/pbzip2` v1.0.6,
Copyright 2019-2021 Cosmos Nicolaou, under the Apache License, Version 2.0.
Its internal decoder is derived from Go's `compress/bzip2`, Copyright 2011
The Go Authors, under the Go BSD license. Both licenses, the Go patent grant,
and exact provenance are retained under `third_party/pbzip2`.

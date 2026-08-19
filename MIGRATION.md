# Go migration and compatibility contract

## Product scope

- Upstream baseline: 7-Zip 26.02, commit `f9d78af`
- Retained product: portable archive engine and `7z`-compatible command
- Supported platforms: Windows, Linux, and macOS
- Excluded permanently: Windows GUI, shell integration, and SDK/COM ABI compatibility
- Compatibility oracle: matching upstream `7z`, `7zz`, or `7za`

Compatibility is bidirectional: upstream must consume Go output, and Go must consume upstream output for every advertised format and mode.

## Current matrix

| Surface | Status | Notes |
| --- | --- | --- |
| Create/update `.7z` | Implemented subset | Copy/LZMA/LZMA2, solid/non-solid, directories, AES data and header encryption |
| Read/test/extract `.7z` | Implemented subset | In-tree Go decoder, passwords, solid blocks, registered filters/codecs |
| ZIP | Implemented subset | Store/Deflate, transactional updates; no encrypted ZIP creation |
| TAR | Implemented | Plain, GZIP, BZIP2, XZ, and Zstandard compositions |
| Single streams | Implemented | GZIP, BZIP2, XZ, and Zstandard |
| ISO/UDF | Read-only subset | UDF, ISO 9660/Joliet, and Rock Ridge names; no image creation |
| WIM | Read-only subset | Uncompressed/XPRESS/LZX resources and multiple images; no LZMS or creation |
| VHD/VHDX | Read-only subset | Fixed/dynamic logical disk extraction; no differencing parents |
| `a`, `u`, `l`, `t`, `x`, `e` | Implemented subset | Parsing/output is intentionally strict where parity is incomplete |
| Delete and rename | Not started | Upstream `d` and `rn` commands |
| Stream and selection CLI | Implemented subset | stdin/stdout, list files, `*`/`?`, recursion, include/exclude masks |
| Compression tuning | Implemented subset | Levels and supported method selection fail explicitly by format |
| Console output | Implemented subset | `l -ba` row parity; other commands retain Go-specific summaries |
| Remaining formats | Not started | RAR, CAB, package formats, filesystems, and others |
| Advanced output | Not started | Multi-volume, SFX, prompts, and full output-stream routing |

Archive updates are transactional rewrites. Existing entries not replaced by an input name are decoded and preserved; replacement data is not published until the new archive closes and syncs successfully.

## Replacement phases

1. **Native 7z writing**: headers, folders, substreams, solid blocks, LZMA2, AES data/header encryption, and updates. Implemented.
2. **Portable format set**: ZIP, TAR compositions, and common single-stream compressors with upstream oracle tests. Implemented for the matrix above.
3. **In-tree 7z decoding**: reader, codecs, filters, AES, and checksums are now vendored with provenance and use only local decoder imports. Corpus expansion and fuzz parity remain.
4. **Mutation parity**: delete, rename, update action rules, links, alternate streams, volumes, and SFX. Stream I/O is implemented.
5. **Remaining format handlers**: ISO/WIM/VHD/VHDX read slices are implemented. Continue in risk-based groups with explicit read/write matrices and fixtures.
6. **CLI parity**: list files, core wildcards, include/exclude masks, tuning, and bare list rows are implemented. Warnings, prompts, routing, and remaining output still need differential tests.
7. **Legacy removal**: completed. The `C/`, `CPP/`, and `Asm/` reference trees were removed while format and licensing documentation was retained. Compatibility testing uses the pinned external upstream oracle.

## Remaining compatibility gates

- Every advertised command, switch, container, codec, encryption mode, and metadata type has a checked compatibility row.
- Bidirectional fixtures pass against pinned upstream on each supported OS where the format exists.
- Parser and decoder fuzzing has stable seed corpora, allocation limits, and no known crashes or uncontrolled memory growth.
- Extraction covers traversal, absolute paths, links/junctions, device names, alternate streams, collisions, and platform-safe file replacement.
- Corrupt, truncated, wrong-password, multi-volume, and large archives return tested errors and exit codes.
- Performance and memory are measured against upstream on representative archives.
- Licensing notices for retained, translated, and third-party components are complete.

Removal of the reference source does not claim that these gates are complete. Missing behavior remains explicit in the matrix above, and parity regressions must be diagnosed against the pinned external upstream build and bidirectional fixtures.

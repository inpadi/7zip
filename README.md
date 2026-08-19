# 7-Zip Go port

This fork is migrating the portable archive engine and `7z` command from upstream [7-Zip](https://7-zip.org/) 26.02 to Go. Windows GUI, shell integration, and SDK/COM compatibility are explicitly out of scope.

The migration is incremental, but the legacy `C/`, `CPP/`, and `Asm/` reference trees are no longer included in this repository. Compatibility is checked against the pinned upstream release described in [MIGRATION.md](MIGRATION.md); the Go executable is built entirely from the Go source tree.

## Download prebuilt binaries

Prebuilt binaries from the current `main` branch are available directly from the repository:

| Operating system | Architecture | Download |
| --- | --- | --- |
| Windows | x86-64 | [i7z.exe](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/windows/amd64/i7z.exe) |
| Windows | ARM64 | [i7z.exe](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/windows/arm64/i7z.exe) |
| Windows | x86 (32-bit) | [i7z.exe](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/windows/386/i7z.exe) |
| Linux | x86-64 | [i7z](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/linux/amd64/i7z) |
| Linux | ARM64 | [i7z](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/linux/arm64/i7z) |
| Linux | ARM (32-bit) | [i7z](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/linux/arm/i7z) |
| Linux | x86 (32-bit) | [i7z](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/linux/386/i7z) |
| macOS | Intel | [i7z](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/darwin/amd64/i7z) |
| macOS | Apple silicon | [i7z](https://github.com/inpadi/7zip/raw/refs/heads/main/Out/darwin/arm64/i7z) |

The matching checksums are in [`Out/SHA256SUMS`](Out/SHA256SUMS). These links follow `main`; tagged [GitHub Releases](../../releases) provide immutable release artifacts, per-target CycloneDX SBOMs, and GitHub build-provenance attestations.

Verify a downloaded artifact before running it:

```sh
sha256sum --check SHA256SUMS --ignore-missing
gh attestation verify ./i7z-linux-amd64 --repo inpadi/7zip
```

Release binaries are built with `CGO_ENABLED=0` from the tagged revision. See [RELEASE.md](RELEASE.md) for the complete release and verification policy.

## Implemented

Commands:

- `a` creates an archive or adds/replaces files in an existing archive
- `u` updates an archive using the same transactional rewrite engine
- `l` and `l -slt` list archive contents
- `t` decodes and verifies archive contents
- `x` and `e` extract with or without stored paths

Formats:

- `.7z`: Copy, LZMA, and LZMA2 creation; solid or non-solid blocks; AES-256 data encryption; optional encrypted headers; password-protected reading; directory metadata; and transactional updates
- `.zip`: Store/Deflate creation, reading, extraction, and transactional updates
- `.tar`, `.tar.gz`/`.tgz`, `.tar.bz2`/`.tbz2`, `.tar.xz`/`.txz`, `.tar.zst`/`.tzst`: creation, reading, extraction, and transactional updates
- `.gz`, `.bz2`, `.xz`, `.zst`: single-stream creation, reading, extraction, and replacement
- `.iso`, `.udf`: read, list, test, and extract UDF or ISO 9660/Joliet images, including Rock Ridge alternate names
- `.wim`: read, list, test, and extract uncompressed, XPRESS, and LZX-compressed resources; multiple images are exposed under numbered directories
- `.vhd` and `.vhdx`: read, list, test, and extract fixed or dynamic disks as a sparse logical `0.img`

The image formats are currently read-only. Differencing VHD/VHDX parents, WIM LZMS resources, WIM reparse points, and mounting filesystems contained inside virtual disks are not implemented.

Relevant switches include `-p{password}`, `-mhe=on|off`, `-ms=on|off`, `-mx[=0-9]`, `-m0={method}`, `-si{name}`, `-so`, `-r`, `-i!{mask}`, `-x!{mask}`, `@listfile`, `-scs{charset}`, `-ba`, `-slt`, and `-t{type}`. The wildcard engine follows 7-Zip's `*`/`?` component rules; brackets are literal, an exact directory selects its subtree, and `-r` recursively applies wildcard masks. `l -ba` uses upstream-compatible fixed-width entry rows. Unsupported switches and incompatible format combinations fail explicitly.

Extraction rejects traversal and absolute paths, filesystem links/junctions in output paths, special files, Windows device paths, flattened-name collisions, and unsafe overwrites.

## Build and test

Go 1.25.5 or newer is required.

```sh
go build -o 7zip-go ./cmd/7zip
go test ./...
go vet ./...
```

When `7z`, `7zz`, or `7za` is installed, the tests run bidirectional interoperability checks for writable formats and upstream-consumer checks for read-only images. They cover solid and non-solid 7z blocks, encrypted data and headers, wrong passwords, updates, each advertised format, and payload verification.

On Windows, the standalone PowerShell harness builds the current `i7z` source and hash-verifies self- and cross-extraction for every accepted compression method:

```powershell
.\test-i7z-interoperability.ps1
.\test-i7z-interoperability.ps1 -SourcePath 'D:\test-data' -CompressionLevel 9
```

The harness reports compression and extraction time, throughput, archive size, requested level, compression ratio, and space saved for both tools. It generates a small payload when `-SourcePath` is omitted; directory sources are packaged once as an uncompressed stream for single-stream codecs, outside the timed operations.

It uses `C:\Program Files\7-Zip\7z.exe` as the reference executable by default. Use `-SevenZipPath` or `-I7zPath` to test different binaries, and `-KeepArtifacts` to retain the generated archives and extraction directories.

Examples:

```sh
7zip-go a -psecret -mhe=on archive.7z ./directory
7zip-go u archive.7z ./changed-file
7zip-go a backup.tar.zst ./directory
7zip-go a -mx=9 -m0=lzma archive.7z ./directory
cat payload.bin | 7zip-go a -sidata.bin -so -t7z archive.7z > streamed.7z
7zip-go l -ba -r -i!*.txt archive.7z
7zip-go t backup.tar.zst
7zip-go x -ooutput archive.7z
```

## Remaining CLI scope

The original project has more archive handlers and mutation commands than this milestone. RAR, CAB, RPM, DEB, and other upstream formats are not yet claimed. Delete, rename, multi-volume output, SFX, per-rule recursion modes, interactive prompts, and byte-identical output for commands other than `l -ba` remain.

The 7z reader, format parser, codec registration, AES decoder, and branch filters are fully in-tree under `internal/sevenzip`. That code began from the BSD-licensed `github.com/bodgit/sevenzip` 1.6.4 implementation; its license and provenance are retained beside the source. The native 7z header/folder writer and AES encoder are implemented in-tree from the upstream implementation and [`DOC/7zFormat.txt`](DOC/7zFormat.txt).

Upstream licensing material remains in [`DOC/License.txt`](DOC/License.txt), [`DOC/copying.txt`](DOC/copying.txt), and [`DOC/unRarLicense.txt`](DOC/unRarLicense.txt). Dependency versions are locked by `go.mod` and `go.sum`.

## Project notices

This is an independent, unofficial port. See [`COPYRIGHT.md`](COPYRIGHT.md) for the upstream acknowledgment and [`LICENSE`](LICENSE) for the license terms that apply to the original project, port changes, and incorporated components.

Official release binaries must be cryptographically signed and published with verifiable checksums and provenance. The purpose and release requirements are documented in [`RELEASE.md`](RELEASE.md).

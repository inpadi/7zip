# 7-Zip Go port

This fork is migrating the portable archive engine and `7z` command from upstream [7-Zip](https://7-zip.org/) 26.02 to Go. Windows GUI, shell integration, and SDK/COM compatibility are explicitly out of scope.

The migration is incremental, but the legacy `C/`, `CPP/`, and `Asm/` reference trees are no longer included in this repository. Compatibility is checked against the pinned upstream release described in [MIGRATION.md](MIGRATION.md). The portable engine is Go; a bounded, public-domain 7-Zip SDK subset under `internal/native` provides optional LZMA/LZMA2 acceleration.

## Download prebuilt binaries

The latest attested release packages are available here:

| Operating system | Architecture | Download |
| --- | --- | --- |
| Windows | x86-64 | [i7z-windows-amd64.zip](https://github.com/inpadi/7zip/releases/latest/download/i7z-windows-amd64.zip) |
| Windows | ARM64 | [i7z-windows-arm64.zip](https://github.com/inpadi/7zip/releases/latest/download/i7z-windows-arm64.zip) |
| Windows | x86 (32-bit) | [i7z-windows-386.zip](https://github.com/inpadi/7zip/releases/latest/download/i7z-windows-386.zip) |
| Linux | x86-64 | [i7z-linux-amd64.tar.gz](https://github.com/inpadi/7zip/releases/latest/download/i7z-linux-amd64.tar.gz) |
| Linux | ARM64 | [i7z-linux-arm64.tar.gz](https://github.com/inpadi/7zip/releases/latest/download/i7z-linux-arm64.tar.gz) |
| Linux | ARM (32-bit) | [i7z-linux-arm.tar.gz](https://github.com/inpadi/7zip/releases/latest/download/i7z-linux-arm.tar.gz) |
| Linux | x86 (32-bit) | [i7z-linux-386.tar.gz](https://github.com/inpadi/7zip/releases/latest/download/i7z-linux-386.tar.gz) |
| macOS | Intel | [i7z-darwin-amd64.tar.gz](https://github.com/inpadi/7zip/releases/latest/download/i7z-darwin-amd64.tar.gz) |
| macOS | Apple silicon | [i7z-darwin-arm64.tar.gz](https://github.com/inpadi/7zip/releases/latest/download/i7z-darwin-arm64.tar.gz) |

The matching `SHA256SUMS` file is published with each tagged [GitHub Release](../../releases). Every package contains its binary, CycloneDX SBOM, and the applicable license and provenance files; GitHub also publishes a build-provenance attestation for each package.

Verify a downloaded artifact before running it:

```sh
sha256sum --check SHA256SUMS --ignore-missing
gh attestation verify ./i7z-linux-amd64.tar.gz --repo inpadi/7zip
```

Tagged releases use the cgo/SDK backend for the canonical Windows AMD64 package and also publish [`i7z-windows-amd64-portable.zip`](https://github.com/inpadi/7zip/releases/latest/download/i7z-windows-amd64-portable.zip) with a `CGO_ENABLED=0` binary. Other release targets currently use the portable Go backend. See [RELEASE.md](RELEASE.md) for the complete release and verification policy.

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

Relevant switches include `-p{password}`, `-mhe=on|off`, `-ms=on|off`, `-mf=on|off`, `-mx[=0-9]`, `-m0={method}`, `-mep=direct|atomic`, `-si{name}`, `-so`, `-r`, `-i!{mask}`, `-x!{mask}`, `@listfile`, `-scs{charset}`, `-ba`, `-slt`, and `-t{type}`. The wildcard engine follows 7-Zip's `*`/`?` component rules; brackets are literal, an exact directory selects its subtree, and `-r` recursively applies wildcard masks. `l -ba` uses upstream-compatible fixed-width entry rows. Unsupported switches and incompatible format combinations fail explicitly.

Extraction creates absent destination files directly by default and removes a partial file if decoding or integrity validation fails. Direct mode reuses a pinned handle for adjacent files in the same directory; do not concurrently relocate the destination tree during extraction. `-mep=atomic` instead revalidates the directory identity for every file, writes and syncs a temporary file, and only then publishes it. Replacing an existing file always uses the temporary-file path.

Extraction rejects traversal and absolute paths, filesystem links/junctions in output paths, special files, Windows device paths, flattened-name collisions, and unsafe overwrites.

## Build and test

Go 1.27.0 or newer is required.

```sh
go build -o 7zip-go ./cmd/7zip
go test ./...
go vet ./...
```

On 64-bit x86 and ARM targets, a build with cgo enabled selects the native 7-Zip SDK LZMA/LZMA2 encoder. Native decoding is available on cgo targets, and Windows AMD64 also uses 7-Zip's baseline x64 decoder assembly. The encoder runtime-checks optional SSE4.1/AVX2 or NEON support before using it; unsupported CPUs stay on the scalar C path.

Use `-tags noasm` to keep the native SDK while forcing its scalar paths. Use `-tags purego`, or disable cgo, to select the portable Go codecs at compile time:

```sh
go build -tags noasm -o 7zip-go-scalar ./cmd/7zip
go build -tags purego -o 7zip-go-portable ./cmd/7zip
CGO_ENABLED=0 go build -o 7zip-go-portable ./cmd/7zip
```

The native level-7 encoder uses a 128 MiB dictionary and peaked near 1.42 GB (1.32 GiB) of tracked C allocation on the driver corpus; SDK allocation is capped at 4 GiB. Choose the portable build when cgo deployment or that encoder memory profile is unsuitable.

When `7z`, `7zz`, or `7za` is installed, the tests run bidirectional interoperability checks for writable formats and upstream-consumer checks for read-only images. They cover solid and non-solid 7z blocks, encrypted data and headers, wrong passwords, updates, each advertised format, and payload verification.

On Windows, the standalone PowerShell harness builds the current `i7z` source and hash-verifies self- and cross-extraction for every accepted compression method:

```powershell
.\test-i7z-interoperability.ps1
.\test-i7z-interoperability.ps1 -SourcePath 'D:\test-data' -CompressionLevel 9
```

The harness reports compression and extraction time, throughput, archive size, requested level, compression ratio, and space saved for both tools. It generates a small payload when `-SourcePath` is omitted; directory sources are packaged once as an uncompressed stream for single-stream codecs, outside the timed operations.

See [PERFORMANCE.md](PERFORMANCE.md) for the current profiling results, controlled codec comparisons, and native acceleration details.

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

## Go library

Import the module as `sevenzip` to open an archive as a staged read-write
filesystem. Archive paths follow `io/fs` conventions and always use forward
slashes. `Close` transactionally rebuilds a changed archive; `Discard` closes
it without publishing staged changes.

```go
package main

import (
	"log"
	"os"

	sevenzip "github.com/inpadi/7zip"
)

func main() {
	archive, err := sevenzip.Open("assets.7z", nil)
	if err != nil {
		log.Fatal(err)
	}

	if err := archive.MkdirAll("images", 0o755); err != nil {
		_ = archive.Discard()
		log.Fatal(err)
	}
	if err := archive.WriteFile("images/index.txt", []byte("updated\n"), 0o644); err != nil {
		_ = archive.Discard()
		log.Fatal(err)
	}
	file, err := archive.OpenFile("events.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		_ = archive.Discard()
		log.Fatal(err)
	}
	if _, err := file.WriteString("archive updated\n"); err != nil {
		_ = archive.Discard()
		log.Fatal(err)
	}
	if err := file.Close(); err != nil {
		_ = archive.Discard()
		log.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		log.Fatal(err)
	}
}
```

Use `sevenzip.Create` instead of `Open` for a new archive. `Archive` implements
`fs.FS`, `fs.ReadFileFS`, `fs.ReadDirFS`, and `fs.StatFS`, and also provides
`Create`, `OpenFile`, `WriteFile`, `Mkdir`, `MkdirAll`, `Remove`, `RemoveAll`,
`Rename`, `Chmod`, and `Chtimes`. Set `Options.ReadOnly` when no mutations
should be allowed. ISO, WIM, VHD, and VHDX archives are always read-only.

The filesystem is materialized in a private temporary directory while open,
so available disk space must cover the extracted contents and the rebuilt
archive. Single-stream gzip, bzip2, XZ, and Zstandard files must contain exactly
one regular file when closed.

## Remaining CLI scope

The original project has more archive handlers and mutation commands than this milestone. RAR, CAB, RPM, DEB, and other upstream formats are not yet claimed. Delete, rename, multi-volume output, SFX, per-rule recursion modes, interactive prompts, and byte-identical output for commands other than `l -ba` remain.

The 7z reader, format parser, codec registration, AES decoder, and branch filters are fully in-tree under `internal/sevenzip`. That code began from the BSD-licensed `github.com/bodgit/sevenzip` 1.6.4 implementation; its license and provenance are retained beside the source. The native 7z header/folder writer and AES encoder are implemented in-tree from the upstream implementation and [`DOC/7zFormat.txt`](DOC/7zFormat.txt).

Upstream licensing material remains in [`DOC/License.txt`](DOC/License.txt), [`DOC/copying.txt`](DOC/copying.txt), and [`DOC/unRarLicense.txt`](DOC/unRarLicense.txt). Dependency versions are locked by `go.mod` and `go.sum`.

## Project notices

This is an independent, unofficial port. See [`COPYRIGHT.md`](COPYRIGHT.md) for the upstream acknowledgment and [`LICENSE`](LICENSE) for the license terms that apply to the original project, port changes, and incorporated components.

Official release binaries must be cryptographically signed and published with verifiable checksums and provenance. The purpose and release requirements are documented in [`RELEASE.md`](RELEASE.md).

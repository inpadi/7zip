# Performance investigation

This note records the August 2026 investigation into the gap between i7z and
7-Zip 26.02. The supplied benchmark used approximately 385 MiB of input at
`-mx=7`. Its most important results were:

| Path | i7z | 7-Zip | Main explanation |
| --- | ---: | ---: | --- |
| Copy compression | 198 MiB/s | 313 MiB/s | Per-file source and buffered-I/O overhead |
| LZMA2 compression | 1.26 MiB/s, 34.60% | 3.88 MiB/s, 23.09% | Unequal preset, greedy matcher/parser, one thread |
| BZip2 compression | 5.85 MiB/s | 24.01 MiB/s | Unequal block preset; one-pass, single-threaded Go encoder |
| XZ compression | 1.24 MiB/s | 4.75 MiB/s | Same LZMA limitations plus one XZ block |
| Zstandard extraction | 234 MiB/s | 485 MiB/s | i7z explicitly selected one decoder worker |

The numeric level alone did not make the LZMA tests comparable. Since 7-Zip
24.09, 64-bit 7-Zip uses a 128 MiB dictionary at level 7. i7z uses 32 MiB.
The 7-Zip application also selects normal-mode BT4, 64 fast bytes, an optimal
parser, and multiple threads. The portable Go encoder changes only dictionary
capacity across most levels.

## Implemented results

The follow-up implementation was measured on the exact 151,375,872-byte
`C:\reinstall.dk\drivers\Win10` TAR stream and its 150,554,642-byte directory
form. Warm runs were alternated where practical; filesystem and Defender noise
still make one-shot wall times unsuitable for small differences.

| Path | Previous i7z | Accelerated i7z | 7-Zip 26.02 |
| --- | ---: | ---: | ---: |
| 7z LZMA creation, level 7, filters disabled | 74.242 s, 42,450,049 B | 30.2 s, 33,707,949 B | 25.3 s, 33,697,207 B |
| 7z LZMA2 creation, level 7, filters disabled | 72.402 s, 42,331,212 B | 28.5 s, 33,596,118 B | 25.5 s, 33,587,871 B |
| BZip2 creation, level 7 | 27.519 s, 54,898,561 B | 2.110 s, 54,971,355 B | 2.294 s, 51,848,918 B |
| XZ creation, level 7 | 55.801 s, 42,081,820 B | 26.911 s, 42,291,648 B | 34.160 s, 33,559,260 B |
| 7z LZMA2 integrity decode | 1.947 s | 0.775 s | 0.789 s |
| XZ integrity decode | 1.80 s | 0.685 s | 0.755 s |
| BZip2 integrity decode | 2.303 s | 1.422 s median | 1.291 s median |
| Copy extraction, 1,042 files | 4.996 s | 3.486 s median | 3.659 s median |

Default 7-Zip LZMA2 creation took 21.0 s and produced 33,041,312 bytes in the
original fresh run. It automatically classified executables into sequential
`ARM64`, `BCJ`, `IA64`, and unfiltered folders. With `-mf=off -myx=0`, the SDK
codec outputs differed by only 8,247 bytes and i7z's complete archive path was
11.8% slower. The remaining default-preset gap was therefore file analysis and
branch filtering rather than the LZMA core or missing SIMD.

i7z now applies the same executable classification and sequential branch-filter
folders by default for LZMA and LZMA2. On the exact 150,554,642-byte directory,
one adjacent measurement produced 33,054,114 bytes in 27.053 s versus 7-Zip's
33,041,312 bytes in 25.556 s: 12,802 bytes larger and 5.86% slower in that pair.
This is not a stable median. A later reverse-order pair measured 22.573 s for
7-Zip and 29.921 s for i7z, while a subsequent 7-Zip no-filter control degraded
to 60.596 s, showing severe paging, thermal, or background-process variance.
The deterministic archive-size result and cross-tool validation are higher
confidence than the one-shot wall times.

The exact folder inputs are 68,996,618 plain bytes, 73,083,008 BCJ bytes,
8,207,160 ARM64 bytes, and 267,856 IA64 bytes. Splitting those inputs while
disabling every filter made the archive 147,258 bytes larger than one solid
stream; enabling the transforms instead recovered 693,817 bytes, for a net
546,559-byte improvement. BCJ supplied essentially all of that gain. Both tools
encode these folders one after another, and each is below its 256 MiB LZMA2
block threshold, so the policy retains approximately the current 1.42 GB peak
rather than multiplying it by four.

The level-7 native encoder peaked at 1,417,337,968 tracked C-allocation bytes.
BZip2's 24-worker run peaked at 877 MiB and XZ's three-worker run at 811 MiB.
These are performance presets with explicit bounds, not low-memory settings.

## Findings

### LZMA, LZMA2, and XZ

The internal fork of `ulikunitz/xz` uses `HashTable4` by default. It examines at
most 16 same-hash candidates and a few short distances, ignores three of the
four LZMA repetition distances while matching, and greedily emits the best
immediate operation. 7-Zip level 7 uses a binary-tree match finder and a
price-based optimal parser that evaluates future operations.

A controlled test used the upstream `enwik7` corpus and the same 8 MiB
dictionary for every encoder:

| Encoder | Median seconds | Ratio |
| --- | ---: | ---: |
| Go greedy, 16 candidates | 2.56 | 0.3330 |
| Go greedy, 64 candidates | 7.16 | 0.3215 |
| Go greedy, 256 candidates | 17.84 | 0.3136 |
| 7-Zip HC4 fast | 2.36 | 0.3207 |
| 7-Zip HC4 normal | 12.10 | 0.2918 |
| 7-Zip BT4 normal | 6.93 | 0.2697 |

Raising the Go search limit is therefore not a solution. At 256 candidates it
was 2.6 times slower than 7-Zip BT4 and still emitted 16.3% more data. The
fork's existing `BinaryTree` is also unusable: on 1 MiB of `enwik7` it changed
the ratio from 0.3263 to 0.6104 and time from 0.282 s to 11.98 s. This agrees
with the upstream project's own TODO item to fix that matcher.

A CPU profile of the current normal LZMA2 writer attributed 37.8% of samples
directly to `hashTable.getMatches`, 15.9% to range `EncodeBit`, and 7.5% to the
rolling hash. Bytewise prefix comparison was below 1%. Go 1.27 SIMD cannot
materially accelerate the random hash-chain walk or the serial range coder.

Merely changing i7z level 7 to a 128 MiB dictionary is risky with the current
matcher. Its dictionary-index array uses four bytes per dictionary byte. A
128 MiB dictionary therefore creates roughly 650 MiB of Go encoder state, and
it still cannot see an older match after 16 newer hash collisions. Dictionary
preset parity should be delivered with the replacement encoder and an explicit
memory policy, not presented as a standalone speed optimization.

### Native LZMA proof of concept

An isolated cgo proof of concept compiled the official 7-Zip 26.02 ANSI-C SDK
with MinGW GCC 16.1 at `-O3`. On a 16 MiB mixed corpus, both encoders used a
32 MiB dictionary and LZMA properties `lc3/lp0/pb2`:

| Encoder | Speed | Bytes | Ratio |
| --- | ---: | ---: | ---: |
| Go `HashTable4` | 1.93 MiB/s | 5,675,332 | 33.83% |
| SDK normal BT, two matcher threads | 2.13 MiB/s | 4,774,279 | 28.46% |

The speed delta varied across one-pass runs, but the native output was
consistently about 16% smaller. SDK decoding reached 26.12 MiB/s versus
13.39 MiB/s for Go on the same SDK-created stream, a 1.95x gain without the
SDK's optional x64 decoder assembly. Cross-decoding passed in both directions.

The native encoder's tracked C-allocation peak was 325.5 MiB. The Go run
reported 168 MiB cumulative allocation; those measurements are not identical,
but both show that memory must be budgeted explicitly. The SDK estimates about
`11.5 * dictionary + 6 MiB` for the normal encoder, so a 128 MiB dictionary is
approximately 1.48 GiB before LZMA2 block-level parallelism.

### Native LZMA implementation

The proof of concept is now an in-tree optional backend built from the pinned
public-domain 7-Zip 26.02 SDK subset under `internal/native/sdk7z`. The encoder
keeps BT4 matching, optimal parsing, range coding, and its two matcher threads
inside C. Go callbacks exchange coarse SDK buffers, and an allocation-counting
`ISzAlloc` fails closed at a 4 GiB ceiling. Level 7 now uses the same 128 MiB
dictionary as current 64-bit 7-Zip.

The decoder owns its input and dictionary buffers in C. On Windows AMD64 it
uses 7-Zip's baseline x86-64 decoder assembly; `noasm` selects scalar SDK C.
The assembly requires no optional AVX or SSE level. Encoder match-finder
helpers use the SDK's runtime SSE4.1/AVX2 or NEON checks and retain scalar C
when unsupported. `purego` or `CGO_ENABLED=0` selects the reviewed Go fork,
and unsupported encoder architectures do the same.

On an 8 MiB mixed corpus, the x64 decoder reached 257-283 MiB/s, scalar C
95-112 MiB/s, and Go 51-54 MiB/s. The exact LZMA2 archive result above closes
the matcher/parser ratio gap without attempting to vectorize the serial range
coder. The portable encoder deliberately keeps its older, smaller dictionary
mapping because applying the 128 MiB preset to its four-byte-per-position hash
table would consume excessive memory without fixing its greedy parser.

### Copy and Store

`store` is an alias for `copy`; the differing one-shot rows in the supplied
table are run-order/cache noise. A controlled Windows test used 1,024 files of
128 KiB each. Excluding filesystem synchronization to isolate CPU/syscall
costs, the original path took 1.484 s versus about 0.816 s for 7-Zip.

Profiling found 0.82 s in `inputFile.open`, including a redundant pre-open
`Lstat`, and 0.55 s in small-buffer copying. A pooled 256 KiB buffer reduced
the result to 1.220 s. Opening through the pinned root and comparing the opened
handle directly with enumeration metadata reduced it to 0.865 s while still
rejecting replacement. Those two changes are now implemented for both 7z and
the generic format paths.

Extraction is dominated by different semantics. On the same many-file corpus,
the current transactional path took 10.10 s, a no-`Sync` prototype took 8.95 s,
and 7-Zip took 4.31 s. In the no-`Sync` profile, rename was 37% of CPU time,
closing file/directory handles was 21%, directory traversal was 13%, temporary
creation was 12%, and copying was only 11%.

Direct root-pinned `O_CREATE|O_EXCL` output is now the default for absent files.
It remains traversal- and collision-safe and removes a partial file on decode
or integrity failure, but gives up atomic visibility and crash cleanup. The
`-mep=atomic` mode retains temporary-file synchronization and publication.
Replacing an existing file always uses a temporary file and rooted rename.
On the exact 1,042-file corpus, five alternating runs gave 3.486 s median for
i7z and 3.659 s for 7-Zip; other snapshots varied by about 10% as Defender and
filesystem state changed.

A one-current-parent root cache reduced a no-`Sync` extraction from 8.585 s to
6.934 s median wall time (19.2%), with CPU falling 17.2%. Atomic mode
revalidates the cached directory identity before every reuse. Direct mode keeps
the handle pinned without that extra syscall and therefore documents that the
destination tree must not be relocated concurrently.

i7z now Deflate-compresses an ordinary 7z next header when the encoded form is
smaller. On the exact directory Copy archive, overhead fell from 121,154 to
17,938 bytes versus 15,067 for 7-Zip. Both the internal reader and upstream
7-Zip validate the encoded header.

### Other codecs

- Go 1.27 already improves `compress/flate`; i7z Deflate compression is faster
  than 7-Zip in the supplied test. The remaining ratio difference is encoder
  strategy, not a reason to replace the working fast path.
- ZIP now registers `klauspost/compress/flate` for decoding. Focused runs were
  27-42% faster than the standard-library decoder; whole extraction gains will
  be lower when filesystem publication dominates.
- Zstandard no longer forces one worker and now disables the decoder's
  low-memory tables. On the exact 151,375,872-byte driver TAR, median integrity
  decode improved from 0.1410 s to 0.0924 s (1.53x). A 385 MiB full-file
  extraction improved from about 789 to 1,006 MiB/s. The high-memory decoder
  uses roughly 7-17 MiB more state, depending on concurrency. Klauspost already
  runtime-dispatches its amd64 BMI2 and arm64 assembly and retains generic Go
  under the `noasm` build tag. On this payload, a `noasm` control was effectively
  tied at about 4.18 versus 4.16 GiB/s, so the measured gain comes from decoder
  tables rather than assuming SIMD helps every input.
- BZip2 compression now emits ordered concatenated streams from up to 24
  independent 900 KiB workers. On the exact driver TAR, a warmed run took
  2.110 s and peaked at 877 MiB versus 2.294 s for 7-Zip. First-use results
  varied up to 4.98 s while the worker state was allocated. The archive grew
  by 72,794 bytes versus the serial Go encoder (0.133% of compressed size), and
  7-Zip validates the concatenated output. The cap follows `GOMAXPROCS`; 32-bit
  builds use at most four workers.
- BZip2 decoding now uses the pure-Go `pbzip2` block scanner and decoder for
  standalone BZip2, tar.bz2, and BZip2 coders inside 7z. The local scheduler
  acquires one of two credits before scanning a block and returns credits only
  after ordered output is consumed. Order deltas return the extra credit when
  pbzip2 repairs a false block-marker match by merging descriptors. Thus the
  scanner, worker queues, completed-result heap, and expanded buffers together
  retain at most two submitted blocks. One level-9 block can expand from
  900,000 transformed bytes to about 46.62 MB after the first RLE stage, plus
  about 3.6 MB of inverse-BWT state and compressed input; the two-block cap is
  intentionally conservative under the 256 MiB decoder policy. Close cancels
  the pipe, closes a blocked source, and waits for active reads and all decoder
  goroutines. On the exact 7-Zip-created stream, seven alternating warm CLI
  tests gave 1.422 s median for i7z versus 1.291 s for 7-Zip (10.2% slower).
  Five sampled i7z runs peaked at 62.3 MiB working set and 101.0 MiB private
  memory. A
  libbzip2 1.0.8 `-O3` prototype reached only 55 MiB/s, so scalar cgo remains
  both slower and unnecessary.
- XZ compression now uses three ordered 48 MiB streams on 64-bit builds. The
  exact driver TAR improved from 55.801 s to 26.911 s and peaked at 811 MiB;
  7-Zip took 34.160 s in the same controlled run. Stream resets added 209,828
  bytes (0.50% of compressed size), and 7-Zip reports four valid streams and
  four blocks. XZ remains serial on 32-bit builds. The chunk size grows with
  dictionaries above 32 MiB so it never resets more frequently than the level
  7 configuration tested here.

## Remaining native work

The implemented SDK backend follows the original design: coarse cgo callbacks,
C-owned bounded state, deterministic close/cancellation, explicit threads, and
pure-Go fallbacks. Unit tests cross-decode both directions, exercise output and
memory-limit failures, and run native, `noasm`, `purego`, and race variants.
Dedicated ASan/UBSan CI and broader differential fuzzing remain worthwhile.

The release workflow now builds the canonical Windows AMD64 binary with cgo
and the SDK backend, verifies that its native encoder and x64 decoder symbols
are present, and packages a separate `-portable` binary with `CGO_ENABLED=0`.
Each release package also contains its SBOM and preserved license/provenance
files. Other release targets remain portable cross-builds. CI tests the default,
`noasm`, `purego`, and cgo-disabled tiers independently so acceleration does
not make cgo a mandatory deployment dependency.

Executable classification and sequential filtered folders are implemented as
an archive-level policy, with `-mf=off` retaining an explicit unfiltered mode.
Parallel LZMA2 block planning only helps inputs above the 256 MiB level-7 block
threshold and would require roughly 3.5-4.1 GiB for two encoder blocks, so it
needs a separate memory policy.
Native zlib or libdeflate is lower priority because Deflate is already
competitive.

## Benchmark policy

The interoperability script currently runs each case once in a fixed order.
Performance runs should add warm-up, alternate tool order, repeat at least five
times, and report median plus a tail percentile. For diagnosis, run both:

- product presets (`-mx=7`), which measure user-visible behavior; and
- equal parameters (`-md=32m`, one thread, explicit match finder), which isolate
  implementation quality.

The script should also report effective method properties and peak memory.
Compression ratio, throughput, thread count, and memory are one preset result;
none should be compared in isolation.

## Primary sources

- [7-Zip 24.09 dictionary change](https://github.com/ip7z/7zip/releases/tag/24.09)
- [7-Zip LZMA encoder properties and parser](https://github.com/ip7z/7zip/blob/main/C/LzmaEnc.c)
- [7-Zip LZMA SDK](https://www.7-zip.org/sdk.html)
- [7-Zip LZMA2 encoder API](https://github.com/ip7z/7zip/blob/main/C/Lzma2Enc.h)
- [`ulikunitz/xz` roadmap](https://github.com/ulikunitz/xz/blob/master/TODO.md)
- [liblzma multithreaded container API](https://tukaani.org/xz/liblzma-api/container_8h.html)
- [bzip2/libbzip2 1.0.8 manual and license](https://sourceware.org/bzip2/manual/manual.pdf)
- [pbzip2 block-parallel Go decoder](https://github.com/cosnicolaou/pbzip2/tree/v1.0.6)
- [Go BZip2 concatenated-stream decoder](https://go.dev/src/compress/bzip2/bzip2.go)
- [7-Zip codec history](https://github.com/ip7z/7zip/blob/main/DOC/src-history.txt)
- [Klauspost Zstandard implementation](https://github.com/klauspost/compress/tree/master/zstd)
- [Go 1.27 release notes](https://go.dev/doc/go1.27)
- [cgo command documentation](https://pkg.go.dev/cmd/cgo)

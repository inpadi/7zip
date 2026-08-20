# Security policy

## Processing untrusted archives

Archive parsing and extraction must run as a dedicated unprivileged account. Do not grant the process access to secrets or production paths that are unrelated to its input and output directories. Apply an operating-system memory limit, CPU quota, writable-disk quota, process limit, and execution deadline in addition to the in-process limits.

Extraction roots, archive-output parents, and list files may not contain symbolic-link or reparse-point components. Source enumeration rejects link entries; each source is then opened through a pinned root and the resulting handle must still identify the enumerated file. Reads, extraction, and archive publication are rooted through pinned directory handles, archive link entries are rejected, and output files never retain group/world write permissions. Hardened extraction intentionally does not restore archived timestamps because portable path-based timestamp APIs are race-prone.

The built-in policy rejects archives exceeding 100,000 entries, 16 GiB per expanded file, 64 GiB total expanded data, a 1000:1 compression ratio with 1 MiB slack, 256 MiB decoder memory, 64 MiB metadata, 128 directory levels, or 30 minutes of streamed processing.

When cgo is enabled, LZMA decoding uses a pinned 7-Zip SDK subset but retains
the same 256 MiB dictionary ceiling and C-owned buffers. `-tags purego` or
`CGO_ENABLED=0` removes that native backend. Archive creation has a separate
4 GiB native-allocation ceiling; the level-7 encoder has measured about
1.42 GiB, so production creation jobs also need an operating-system memory
limit appropriate to their selected preset.

## Reporting vulnerabilities

Report suspected vulnerabilities privately through GitHub's security-advisory interface. Do not include production secrets, credentials, or sensitive archives in a report.

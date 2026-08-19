# Security policy

## Processing untrusted archives

Archive parsing and extraction must run as a dedicated unprivileged account. Do not grant the process access to secrets or production paths that are unrelated to its input and output directories. Apply an operating-system memory limit, CPU quota, writable-disk quota, process limit, and execution deadline in addition to the in-process limits.

Extraction roots, archive-output parents, list files, and source paths may not contain symbolic-link or reparse-point components. Reads, extraction, and archive publication are rooted through pinned directory handles, archive link entries are rejected, and output files never retain group/world write permissions. Hardened extraction intentionally does not restore archived timestamps because portable path-based timestamp APIs are race-prone.

The built-in policy rejects archives exceeding 100,000 entries, 16 GiB per expanded file, 64 GiB total expanded data, a 1000:1 compression ratio with 1 MiB slack, 256 MiB decoder memory, 64 MiB metadata, 128 directory levels, or 30 minutes of streamed processing.

## Reporting vulnerabilities

Report suspected vulnerabilities privately through GitHub's security-advisory interface. Do not include production secrets, credentials, or sensitive archives in a report.

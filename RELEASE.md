# Release statement

## Purpose

This independent Go port exists in part to strengthen control over the
software supply chain, reduce dependence on opaque prebuilt artifacts, and
protect binary distribution against unauthorized modification or foreign
interference. Its source-first, reviewable build process is intended to make
changes attributable and releases independently verifiable.

This purpose is not an allegation that the upstream 7-Zip project, its
developer, or any person or country has interfered with 7-Zip. It is also not a
guarantee that this port is free of defects, vulnerabilities, or compromise.
Security depends on review, testing, protected release credentials, and
independent verification.

## Signed official binaries

Official binary releases of this port must be cryptographically signed. Each
official release must provide:

- a versioned source tag tied to the reviewed source revision;
- binaries built from that revision;
- cryptographic checksums for every distributed artifact;
- a signature or signed provenance attestation covering the artifacts and
  checksums; and
- the signing identity or public-key information and verification
  instructions needed to validate the release.

A signature establishes the claimed publisher and detects modification after
signing. It does not by itself prove that a binary is secure. An artifact that
cannot be validated against the published release signature must not be
represented as an official binary of this project.

Third parties remain free to build, modify, sign, and redistribute the
software under the applicable licenses, but their artifacts are not official
project releases unless the project release process has authenticated them.

## Software freedom

Signing is a provenance control, not a restriction on use. Source and binaries
remain available under the free and open-source terms described in `LICENSE`.
Recipients may use, study, modify, build, and redistribute the software,
subject only to the applicable MIT, GNU LGPL, BSD, public-domain, unRAR, and
other third-party license terms.

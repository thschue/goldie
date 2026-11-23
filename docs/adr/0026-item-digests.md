# 26. Item digests

Status: proposed (the maintainer decides; #406)

## Context

A skill is a bundle of files. `goldie pull` (#16) and the loops (#17) need to tell whether a bundle
on disk is the one the server serves, from any Source: a directory, a git tree or the local Scope.
The same bytes must give the same digest wherever they were read, and no two different bundles may
give one input to the hash.

## Decision

An Item digest is computed by the engine and has the form `sha256:` followed by 64 lowercase hex
digits. It is the SHA-256 of the bundle's files in the byte order of their paths, each file
contributing

    path 0x00 x 0x00 length 0x00 bytes

- `path` is the slash-separated path relative to the skill directory, as UTF-8 bytes. It holds no NUL
  (the loader refuses names with hidden runes), so the first NUL ends it.
- `x` is one byte: `0x01` when the file is executable (a directory's mode has any x bit, a git
  entry has mode 100755), `0x00` otherwise.
- `length` is the number of content bytes as a decimal ASCII number without sign or leading zeros.
- `bytes` is the content exactly as written. Nothing is normalised: CRLF, a byte-order mark and
  case all count.

Because each file is framed by its path and its length, moving a byte between a path and a content,
or between two files, changes the input. Paths that differ only in case fail the load, so case never
makes two bundles collide.

Changing the algorithm or the encoding means a new prefix (`sha512:`, `sha256v2:`), never a
redefinition of `sha256:`: a digest a client stored must keep meaning what it meant.

## Consequences

- A digest is stable across Sources, so a client compares it with the one it computed on disk.
- Only the executable bit is covered of a file's mode; ownership, timestamps and other mode bits
  are not part of a bundle.
- A symlink, a FIFO or a device in a skill directory is not part of the bundle and not in the digest.
- Rejected: hashing a tar of the directory (depends on the tar writer and on timestamps), and a
  Merkle tree (no need for partial verification yet).

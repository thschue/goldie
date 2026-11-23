# 24. Managed-block marker format v1

Status: accepted

Amends [7. Managed instruction blocks](0007-managed-instruction-blocks.md), which named the marker
format as a compatibility surface without fixing it.

## Context

`goldie instructions apply` writes the Managed block into files people also edit by hand, and
`check` reads it back in CI. Every file ever written carries the marker, so the format cannot
change once blocks exist. The editor must also tell a block someone edited from one that is only
behind the server.

## Decision

A block is exactly these lines, with the file's line ending:

```
<!-- goldie:begin v1 digest=sha256:<hex> -->
<content>
<!-- goldie:end -->
```

- `<hex>` is 64 lower-case hex digits. A marker is a whole line, the exact begin or end form. A
  line inside a fenced code block (CommonMark rules; an unclosed fence runs to the end of the file)
  is not a marker, and neither is a marker mentioned inside a longer line, a v2 marker or a begin
  line with a malformed digest. All of these are plain text.
- The digest covers the content after CRLF is normalised to LF and one trailing LF is dropped.
- Edited means the content does not match the stamped digest. Outdated means the stamp does not
  match the server's digest. Edited wins when both hold, so a hand edit is never reported as a safe
  refresh.
- Exactly one pair per file is allowed. A begin without an end, a begin inside a block, an end
  without a begin, or a second pair is an error, and nothing is written.
- Content that holds a marker line, or leaves a code fence open, is refused: it would not read back
  as one block.
- A leading BOM and every byte outside the markers, line endings included, are kept as they are.
  New blocks use the line ending of the begin marker (or of the file's first line), and LF for a
  new file.
- A symlinked file is refused. Writes go to a temp file beside it with the old mode and are renamed
  over, and nothing is written when the bytes do not change. Errors hold no content, digest, path
  or file byte.
- Readers accept v1 forever. A later format is a v2 that is only ever added.

## Consequences

- The format is a compatibility surface: a change needs a v2 and a new ADR, and every v1 block
  keeps reading.
- A file that mentions the markers in prose or in a code sample is safe, at the cost of the fence
  rule being part of the format.
- A v1 reader sees a v2 block as plain text and would append its own block beside it. That is why a
  v2 must ship with a reader that handles both.

Rejected alternatives:

- **A digest of the server content only.** It cannot tell a hand edit from an old version.
- **Tolerating several pairs.** Which one wins is a guess, and a guess edits the wrong bytes.

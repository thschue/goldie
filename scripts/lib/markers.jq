# Loop markers (docs/agents/README.md#markers), shared by scripts/merge-pr,
# scripts/loop-poll and scripts/loop-metrics: jq -L scripts/lib 'include "markers"; …'.
#
# A marker counts only when the comment's author is the loop account and the
# marker is the comment's final line.

# own_markers($me): on a PR object with .comments, the own marker comments,
# oldest first, as {at, line, kind, body}.
def own_markers($me):
  [ (.comments // [])[]
    | select(.author.login == $me)
    | (.body | sub("\\s+$"; "") | split("\n") | last) as $last
    | select($last | test("^<!-- goldie-loop:[a-z0-9.-]+( [^>]*)? -->$"))
    | {at: .createdAt, line: $last, body: .body,
       kind: ($last | capture("^<!-- goldie-loop:(?<k>[a-z0-9.-]+)").k)} ];

# attr($k): the value of attribute <k>=<v> on a marker line, or null.
def attr($k): (capture("[ ]" + $k + "=(?<v>[^ ]+)") // {v: null}).v;

# latest($kind): the newest marker of that kind, or null.
def latest($kind): map(select(.kind == $kind)) | last;

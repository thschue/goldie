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

# answered($me): on a PR, an answer follows the latest review marker: a
# `PM decision:` comment by the product session, or a comment by the
# maintainer (the loop account without a marker).
def answered($me):
  (own_markers($me) | latest("review") | .at) as $t
  | $t != null and any((.comments // [])[];
      .author.login == $me and .createdAt > $t
      and ((.body | test("PM decision:"))
           or ((.body | sub("\\s+$"; "") | split("\n") | last) | test("goldie-loop:") | not)));

# failing: a PR with a failed check on its head (needs .statusCheckRollup).
# Only the latest run of each check counts: a run cancelled because a later
# one superseded it (marking a draft ready reruns lint-pr-title) is not red.
def failing:
  [.statusCheckRollup[]?] | group_by(.name // .context)
  | any(.[]; max_by(.startedAt // "") | (.conclusion // .state)
      | IN("FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED"));

# stream_state($me): what an open stream PR needs this tick (docs/agents/delivery-loop.md):
#   parked    held for the maintainer or waiting on an answer: nothing to do
#             (a held PR is still dirty or red first: the maintainer never
#             gets a conflicted or red PR to approve)
#   answered  its decision got an answer: act on it
#   disarm    auto-merge is armed but the head moved past the clean marker
#   dirty     reviewed clean or held, then a conflict with main
#   red       reviewed clean or held, then a failed check on the head
#   waiting   reviewed clean at the head, merging: nothing to do
#   active    in progress (no review for the head yet)
# Needs .labels, .comments, .headRefOid, .mergeStateStatus, .autoMergeRequest
# and .statusCheckRollup.
def stream_state($me):
  own_markers($me) as $m
  | ($m | latest("review") | .line // "") as $review
  | ([.labels[].name]) as $labels
  | if ($labels | index("needs-maintainer")) then
      (if .mergeStateStatus == "DIRTY" then "dirty"
       elif failing then "red"
       else "parked" end)
    elif ($review | attr("reason")) == "decision" and ($review | attr("sha")) == .headRefOid then
      (if answered($me) then "answered" else "parked" end)
    elif ($labels | index("needs-info")) then "parked"
    elif ($review | attr("reason")) == "clean" and ($review | attr("sha")) != .headRefOid
      and .autoMergeRequest != null then "disarm"
    elif ($review | attr("reason")) == "clean" and ($review | attr("sha")) == .headRefOid then
      (if .mergeStateStatus == "DIRTY" then "dirty"
       elif failing then "red"
       else "waiting" end)
    else "active" end;

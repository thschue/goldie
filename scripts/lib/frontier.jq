# Frontier ranking (docs/agents/README.md#order-of-work), used by scripts/frontier:
# jq -L scripts/lib 'include "frontier"; rank'.
#
# Input: claimable issues as [{number, createdAt, labels: [name], epic}], where
# epic is null or {number, createdAt, started}: the issue's nearest ancestor
# labelled `epic` (the spec root excluded), and whether that epic has a closed
# or in-progress sub-issue. Output: the same issues, best first:
#   0. `bug` or `priority`;
#   1. an issue of a started epic, oldest started epic first;
#   2. a standalone issue;
#   3. an issue of an unstarted epic, oldest epic first.
# Ties go to the oldest issue. `security` is a tag, not a class.

def class:
  if (.labels | index("bug") or index("priority")) then 0
  elif .epic == null then 2
  elif .epic.started then 1
  else 3 end;

def rank:
  sort_by([class,
           (if class == 1 or class == 3 then .epic.createdAt else "" end),
           .createdAt, .number]);

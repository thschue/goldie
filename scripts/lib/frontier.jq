# Frontier ranking (docs/agents/README.md#order-of-work), used by scripts/frontier:
# jq -L scripts/lib 'include "frontier"; rank'.
#
# Input: claimable issues as [{number, createdAt, labels: [name], epic}], where
# epic is null or {number, createdAt, started}: the issue's nearest ancestor
# labelled `epic` (the spec root excluded), and whether that epic has a closed
# or in-progress sub-issue. Output: the same issues, best first, by priority
# level (`priority:P1` to `priority:P3`; `bug` counts as P1, no label as P2),
# then within a level:
#   1. an issue of a started epic, oldest started epic first;
#   2. a standalone issue;
#   3. an issue of an unstarted epic, oldest epic first.
# Ties go to the oldest issue. `security` is a tag, not a class.

def level:
  if (.labels | index("bug") or index("priority:P1")) then 1
  elif (.labels | index("priority:P3")) then 3
  else 2 end;

def class:
  if .epic == null then 2
  elif .epic.started then 1
  else 3 end;

def rank:
  sort_by([level, class,
           (if class == 1 or class == 3 then .epic.createdAt else "" end),
           .createdAt, .number]);

# in_scope($current): whether the loop may work an issue at all (ADR 0029).
# Input: one issue with labels and milestone ({title} or null). True when the
# issue is in the current milestone, or carries `security`, which never waits
# for a milestone. With no open milestone, $current is "" and only security
# work is in scope: the loop idles until the maintainer opens the next one.
def in_scope($current):
  ($current != "" and .milestone != null and .milestone.title == $current)
  or (.labels | index("security") != null);

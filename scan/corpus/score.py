"""Score one helper scan event against the card it was supposed to read.

Reads the event on stdin, emits a tab-separated row for sweep.sh to aggregate:

    era  border  name-ok  number-ok  wanted  got

The name is scored leniently — the helper's job is to hand downstream something
fuzzy matching can land, not to be letter-perfect — so a read counts if it
normalizes equal, or if either is a prefix of the other (glare truncates
titles). The collector number is scored strictly, but an empty read counts as
correct for cards printed before collector numbers existed: there was nothing
on the card to read.
"""
import json
import sys


def norm(s):
    return "".join(c for c in (s or "").lower() if c.isalnum())


era, border, want_name, want_num = sys.argv[1:5]
raw = sys.stdin.readline().strip()
got_name, got_num = "", ""
if raw:
    try:
        ev = json.loads(raw)
        cards = ev.get("cards") or []
        c = cards[0] if cards else {}
        got_name = c.get("name") or ev.get("name") or ""
        got_num = c.get("collectorNumber") or ev.get("collectorNumber") or ""
    except ValueError:
        pass

a, b = norm(want_name), norm(got_name)
name_ok = bool(a) and bool(b) and (a == b or a.startswith(b) or b.startswith(a))
# Pre-1998 cards print no number; reading none is the right answer there.
num_ok = got_num == want_num or (era == "pre1998" and got_num == "")

print("\t".join([era, border, "1" if name_ok else "0", "1" if num_ok else "0",
                 f"{want_name} #{want_num}", f"{got_name} #{got_num}"]))

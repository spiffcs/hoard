#!/usr/bin/env python3
"""Sweep the p(foil) acceptance bar over the held-out fold probabilities.

Input: the PROB lines train-foil.swift emits (one file, all folds).
Output: per candidate bar, false-foils per rig (the ship-gate demands 0 on
every one) and overall foil recall — the table that decides whether the
classifier ships gated, or stays eval-only.

Usage: sweep-threshold.py /tmp/foil-probs.txt
"""

import sys
from collections import defaultdict

rows = []  # (rig, truth, p)
for line in open(sys.argv[1]):
    if not line.startswith("PROB\t"):
        continue
    _, rig, truth, p, _ = line.rstrip("\n").split("\t")
    rows.append((rig, truth, float(p)))

rigs = sorted({r[0] for r in rows})
foils = [r for r in rows if r[1] == "foil"]
nonfoils = [r for r in rows if r[1] == "nonfoil"]
print(f"{len(rows)} held-out reads: {len(foils)} foil, {len(nonfoils)} nonfoil, rigs {rigs}")
print(f"\n{'bar':>6} {'recall':>12} " + " ".join(f"FF:{r:>6}" for r in rigs))
best = None
for bar100 in range(50, 100, 2):
    bar = bar100 / 100
    ff = defaultdict(int)
    for rig, _, p in nonfoils:
        if p >= bar:
            ff[rig] += 1
    caught = sum(1 for _, _, p in foils if p >= bar)
    marker = ""
    if all(ff[r] == 0 for r in rigs):
        marker = "  <- gate met"
        if best is None:
            best = (bar, caught)
    print(f"{bar:>6.2f} {caught:>4}/{len(foils):<3}      "
          + " ".join(f"{ff[r]:>9}" for r in rigs) + marker)

print()
if best:
    bar, caught = best
    print(f"lowest gate-meeting bar: {bar:.2f} — recall {caught}/{len(foils)} "
          f"({100*caught/len(foils):.0f}%), 0 false-foils on every held-out rig")
else:
    print("NO bar meets the gate: some nonfoil outscores foils on every candidate")
# The highest nonfoil per rig is the number the chosen bar must clear.
for r in rigs:
    top = max((p for rig, _, p in nonfoils if rig == r), default=0)
    print(f"  highest held-out nonfoil p(foil) on {r}: {top:.4f}")

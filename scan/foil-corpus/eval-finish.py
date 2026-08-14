#!/usr/bin/env python3
"""Finish-accuracy scoreboard for the foil reader.

Runs bin/cardkit-probe over every labelled capture and scores the finish
verdict against physical truth, per rig:

  corpus   scan/foil-corpus/full/<id>.png     labelled by labels.tsv
  s5..sN   scan/foil-corpus/stills/<id>.jpg   labelled by stills-labels.tsv

Two numbers matter and they are printed per rig:

  verdict accuracy   the reader's own claim (foil / nonfoil / abstain)
  commit accuracy    what a holding would record under the shipping policy,
                     where an abstention commits as nonfoil

Usage: eval-finish.py [--misses] [--probe PATH] [--jobs N]
"""

import argparse
import csv
import json
import os
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))

def read_rigs():
    rigs = {}
    with open(os.path.join(HERE, "labels.tsv")) as fh:
        for row in csv.DictReader(fh, delimiter="\t"):
            img = os.path.join(HERE, "full", row["id"] + ".png")
            if row["finish"] in ("foil", "nonfoil") and os.path.exists(img):
                rigs.setdefault("corpus", []).append(
                    (row["id"], img, row["finish"], row["name"]))
    path = os.path.join(HERE, "stills-labels.tsv")
    if os.path.exists(path):
        with open(path) as fh:
            for row in csv.DictReader(fh, delimiter="\t"):
                img = os.path.join(HERE, "stills", row["id"] + ".jpg")
                if row["finish"] in ("foil", "nonfoil") and os.path.exists(img):
                    rigs.setdefault(row["session"], []).append(
                        (row["id"], img, row["finish"], row["physical"]))
    return rigs

def probe_one(probe, entry):
    sid, img, want, name = entry
    out = subprocess.run([probe, "--image", img], capture_output=True, text=True)
    card = {}
    try:
        d = json.loads(out.stdout)
        cards = d.get("cards") or [{}]
        card = cards[0]
    except (json.JSONDecodeError, IndexError):
        pass
    hint = card.get("finishHint") or ""
    committed = hint if hint else "nonfoil"
    return {
        "id": sid, "name": name, "want": want,
        "hint": hint, "source": card.get("finishSource") or "",
        "committed": committed,
        "sparkle": card.get("sparkleScore"),
        "contrast": card.get("sparkleContrast"),
        "chroma": card.get("sparkleChromaScore"),
        "chromaContrast": card.get("sparkleChromaContrast"),
    }

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--misses", action="store_true")
    ap.add_argument("--probe", default=os.path.join(HERE, "..", "..", "bin", "cardkit-probe"))
    ap.add_argument("--jobs", type=int, default=8)
    args = ap.parse_args()

    rigs = read_rigs()
    if not rigs:
        sys.exit("no labelled captures found")

    hdr = f"{'rig':<8}{'reads':>6}{'foil':>6}{'non':>6} | {'verdict-acc':>11}{'commit-acc':>11} | {'false-foil':>10}{'false-non':>10}{'abstain':>8}"
    print(hdr)
    print("-" * len(hdr))
    worst_ok = True
    for rig in sorted(rigs):
        entries = rigs[rig]
        with ThreadPoolExecutor(max_workers=args.jobs) as ex:
            results = list(ex.map(lambda e: probe_one(args.probe, e), entries))
        foil = [r for r in results if r["want"] == "foil"]
        non = [r for r in results if r["want"] == "nonfoil"]
        false_foil = [r for r in non if r["hint"] == "foil"]
        false_non = [r for r in foil if r["hint"] == "nonfoil"]
        abstain = [r for r in results if not r["hint"]]
        verdict_ok = [r for r in results if r["hint"] == r["want"]]
        commit_ok = [r for r in results if r["committed"] == r["want"]]
        print(f"{rig:<8}{len(results):>6}{len(foil):>6}{len(non):>6} | "
              f"{len(verdict_ok):>4}/{len(results):<3}    {len(commit_ok):>4}/{len(results):<3}    | "
              f"{len(false_foil):>10}{len(false_non):>10}{len(abstain):>8}")
        if args.misses:
            for r in results:
                if r["committed"] != r["want"]:
                    nums = "/".join(
                        "-" if r[k] is None else f"{r[k]:.3f}"
                        for k in ("sparkle", "contrast", "chroma", "chromaContrast"))
                    print(f"    {r['id']:<8}{r['name']:<24}want={r['want']:<8}"
                          f"read={r['hint'] or 'abstain':<8}src={r['source'] or '-':<14}{nums}")
        if len(commit_ok) != len(results):
            worst_ok = False
    sys.exit(0 if worst_ok else 1)

if __name__ == "__main__":
    main()

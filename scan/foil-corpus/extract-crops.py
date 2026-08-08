#!/usr/bin/env python3
"""Build the foil-classifier training set from the labelled corpus.

Runs cardkit-probe --emit-sparkle over every labelled capture and lays the
crops out the way CreateML's MLImageClassifier wants them, one directory per
class, kept separate per rig so training can hold a whole rig out:

    dataset/<rig>/<finish>/<id>.png

The crop is the reader's own view — the sparkle search window plus margin,
extracted by the probe's geometry — so the model trains on exactly what the
live reader would hand it. Captures where no card locates (probe exit 4) are
skipped and counted; they are the same captures the live reader abstains on.

Usage: extract-crops.py [--probe PATH] [--out DIR]
"""

import argparse
import csv
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def entries():
    with open(os.path.join(HERE, "labels.tsv")) as fh:
        for row in csv.DictReader(fh, delimiter="\t"):
            img = os.path.join(HERE, "full", row["id"] + ".png")
            if row["finish"] in ("foil", "nonfoil") and os.path.exists(img):
                yield "corpus", row["id"], img, row["finish"]
    path = os.path.join(HERE, "stills-labels.tsv")
    if os.path.exists(path):
        with open(path) as fh:
            for row in csv.DictReader(fh, delimiter="\t"):
                img = os.path.join(HERE, "stills", row["id"] + ".jpg")
                if row["finish"] in ("foil", "nonfoil") and os.path.exists(img):
                    yield row["session"], row["id"], img, row["finish"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--probe", default=os.path.join(HERE, "..", "..", "bin", "cardkit-probe"))
    ap.add_argument("--out", default=os.path.join(HERE, "dataset"))
    args = ap.parse_args()

    done, skipped = 0, 0
    for rig, sid, img, finish in entries():
        out_dir = os.path.join(args.out, rig, finish)
        os.makedirs(out_dir, exist_ok=True)
        out = os.path.join(out_dir, sid + ".png")
        if os.path.exists(out):
            done += 1
            continue
        r = subprocess.run([args.probe, "--image", img, "--emit-sparkle", out],
                           capture_output=True)
        if os.path.exists(out):
            done += 1
        else:
            skipped += 1
            print(f"  skip {sid}: no card located (exit {r.returncode})", file=sys.stderr)
    print(f"dataset: {done} crops, {skipped} skipped (no card located)")


if __name__ == "__main__":
    main()

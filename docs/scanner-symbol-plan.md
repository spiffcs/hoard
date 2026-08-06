# Reading the expansion symbol

A plan, not a description: none of this is built. Written down while the
measurements behind it are fresh — see `docs/scanner-tuning.md` for the border
reader this builds on, and `docs/scanner-limits.md` for where it sits in the
coverage picture.

## Why

Two separate cases converge on the same signal.

**Pre-1998**, year plus border pins 47% of ambiguous printings. Symbol
*identity* is equivalent to set identity, which pins **83%** — and it does it
hands-free, which is the whole point: a session set hint would pin the same 83%
and was rejected on principle, because the auto feature must not ask the user
anything. The specific confusions border cannot break are `4bb` against `ice`
(both black, both 1995 — 72 printings), `itp` against `rqs` (131), and Portal
against the Tempest block (118).

**8th Edition and its neighbours** are the sharper case, and the one a live
session actually demonstrated. 8ED cards queue because their collector number
sits in a copyright line that a 1080p desk photo cannot resolve, and border
alone cannot rescue them: Sacred Ground's white printings are 7ED, 8ED *and*
9ED. A symbol would pin all of them. Measured on a real 22-frame 8ED session,
17 cards queued "printing unverified" — Tremor across 9 printings, Canyon
Wildcat across 4, Shatter across 32 — and a set would collapse essentially all
of it.

## What is already measured

Numbers to trust; they cost a day to get.

- **Which sets print one.** Verified against corpus images, not recollection:
  4th Edition's type-line right margin is bare, Ice Age carries a snowflake,
  7th Edition a "7", 8th Edition an "8". Core sets before 6th print nothing
  there; expansions do.
- **Where it sits.** Old frame at card-space **(0.877, 0.578)**; the
  redesigned 8th Edition frame at **(0.867, 0.590)**. One patch covers both.
  The box used today is 0.055 of card width by 0.026 of height.
- **Size on a real capture.** At 1080p a card is ~880 px tall, so the symbol is
  roughly **35 × 23 px**.
- **Colour.** Pre-Exodus symbols are monochrome; from 1998 they are
  rarity-coloured (black / silver / gold), so one set has three colour
  variants. Match shape, never colour.
- **Vision does not read them.** No lone "7" or "8" ever appears as a text
  line, on clean scans or photographs. They are stylised glyphs, not text.

## What blocks it today

*On the Continuity Camera path, all of the below still stands. On the iPhone
capture head it does not.*

*Two of the three things this document is waiting on have been measured on real
captures — see "The expansion symbol is legible" and "Thirteen cards at the
operating point" in `scanner-tuning.md`. The copyright row that blocks anchoring
reads cleanly, collector number and denominator included, on every 1993-2003
frame tried. And step 2's disconfirming test is answered: patches crop at
~450x370 px rather than 35x23, and five sets — Stronghold's portcullis, Mirage's
palm tree, Fallen Empires' crown, Urza's Saga's gears and Urza's Legacy's
hammer — are separable by eye without effort.*

*What that changes: the feature is not dead, and the landmark problem is gone.
What it does not change: type-line anchoring below is still the better fix,
because its error budget is proportional rather than absolute, and the patch is
currently cropped from an axis-aligned box that assumes the card is square to
the frame. It also reframes the goal — the symbol does not need to identify one
of 986 sets, only to separate the handful of printings that share a title, which
is a feature-print distance rather than a classifier.*

**The patch cannot be located on a live 8th Edition frame.** It is positioned
from the footer landmark, and the only landmark tight enough to use on that
frame is the `™ &` copyright row — which is precisely the line a desk photo of
that frame fails to read. `CardLayout.leftU` returns nil there deliberately: the
lever from landmark (u≈0.08) to symbol (u≈0.87) is most of the card's width, so
a guessed offset throws the patch clean off the image, which is exactly what a
7th Edition card did while that was a single constant.

**And presence does not yet separate.** `symbolInk` reports coverage on every
probe and nothing consumes it. Measured: 4ed 0.003/0.005 and ice 0.193, mir
0.109, tmp 0.185, 7ed 0.188/0.266, 8ed 0.667 — all correct — but **5ed 0.229**
with no symbol and **vis 0.042** with one. Probably the same root cause: a 1.5%
height error becomes ~12 px at the symbol, comparable to its own size.

## The order to do it in

### 1. Anchor on the type line, not the footer

The unblocker, and it stands on its own: it would also give live 8ED frames a
horizontal landmark, which is nil today.

Find the type line by content — `Creature`, `Instant`, `Sorcery`, `Land`,
`Artifact`, `Enchantment`, `Summon`, or the em-dash pattern — the same
provenance-by-content rule `footerAnchor` already uses. Then measure the
symbol's offset from *that line's own box*, per frame era, on the corpus.

Why it should work where the footer does not: the type line reads reliably on
exactly the frames whose footer fails, it sits on the **same row** as the
symbol, and it is a few percent away rather than most of a card width. Most of
the accumulated scale error cancels, so this may fix presence as a side effect.

*Needs measuring:* symbol centre relative to the type-line box, old frame and
8ED frame separately.

### 2. The cheapest disconfirming test

**Crop 35 × 23 symbol patches from real desk photographs of 7ED and 8ED and see
whether anything can tell a "7" from an "8".** Under real light, real glare,
real focus. If the pixels do not carry it at that size, the feature is dead and
we have spent an hour instead of a week.

This needs step 1 to locate the patch, which is why it is second rather than
first.

### 3. Refit presence

Ground truth is free — the corpus manifest gives the set and the
core-vs-expansion partition above gives the answer. Refit box size and position
against it, and report coverage and precision separately, as `border.sh` does.

### 4. Identify which symbol

The reframe that makes this tractable: **it is not a classification over ~900
sets.** The name has already resolved to N candidate printings, each with a
known set, so it is a *match over N* — usually under 20, often under 6.

- Scryfall gives each set an `icon_svg_uri`. The catalog stores no set-level
  data at all today, so this is a schema addition plus a fetch and cache.
- Nothing in the toolchain rasterises SVG, and Scryfall offers no other format.
  WebKit or CoreImage can; it is new machinery either way.
- Match the silhouette. Scryfall's icon is a single shape, which is the right
  target given rarity colouring.

### 5. Rank it, and let it abstain

Cheaper than it looks: `it.prints` is already in memory with each printing's
set, so filtering by a read set needs **no new catalog query** — no
`PrintBySetNumber` equivalent. Name plus set pins the printing unless the card
appears twice in one set.

It needs its own rank, gated the way `scanMatchYearAndBorder` is. The safety
argument is identical to border's and must not be skipped: a wrong set commits
a wrong printing, and a symbol match always matches *something*, so it cannot
fail closed on its own. Require the best match to beat the second best
decisively, and abstain otherwise.

## The shortcut, if general matching proves too much

The ambiguity that actually hurts is **6ED / 7ED / 8ED / 9ED / 10E** — all
white-bordered core sets, all with **digit** symbols. Template-matching five
digits is far easier than 900 icons and covers the case a real session
produced. Steps 1–3 are shared either way, so deciding this later costs
nothing.

## Where the code is

- `CardLayout.symbolU` / `symbolV`, `symbolInk`, `CardGeometry.point(u:v:)`,
  `CardLayout.leftU` — `scan/hoard-scan/Sources/ScanKit/Core/Border/CardLayout.swift`
- Scoring harness and the `--dump` mode — `scan/corpus/border.sh`
- Ranking and the abstain discipline to copy — `applyBorderEvidence`,
  `internal/tui/autoscan.go`

## The rule that governs all of it

Fit constants on `scan/corpus`, where the card fills the frame so its rect is
known exactly. **Confirm every one of them on `scan/fixtures`, which are real
photographs.** A colour gate for gold looked perfect on clean scans and was
wrong the first time it met a desk lamp; an absolute luminance gate survived
231 corpus images and then went silent on six white cards under a warm bulb.
The corpus cannot tell you what a lamp does.

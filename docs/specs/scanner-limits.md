# What the scanner can and cannot read

The honest source for any accuracy claim, whether App Store copy, README or
release notes. Every number below was measured on the date given, with the command
given, against a labelled corpus that is in this repo. Where a limit is a
claim nobody has measured, it says so and is marked unverified. Nothing here
is quoted from another document; the leads came from `docs/specs/scanner-tuning.md`
and the sprint docs, and each was re-derived from the code or re-run.

**Measured: 2026-08-08**, on the working tree at `02ecda4`+, with
`cardkit-probe` rebuilt from source immediately before each run
(`swift build -c release --package-path scan/hoard-scan --product cardkit-probe`).
Re-run everything below before any release that makes a numeric claim; Vision's
text model changes with the OS, and the corpus manifest changes when it is
re-sampled.

---

## Bottom line

The scanner reads **modern English cards in good light, reliably**. On clean
scans of English printings it names 87% and gets the exact collector number on
78%. It is a card *reader*, not a card *grader*: it never assesses condition,
and it never claims a finish it did not see printed.

Three things it genuinely cannot do, all confirmed by measurement today:

1. **Foreign-language cards do not resolve.** Two independent walls, either
   of which alone is fatal. See §3.
2. **Planes and other oversized cards are read wrong, not refused.** 0 of 8
   in the corpus, and each one emitted a card entry carrying a fragment of its
   rules text as the title. See §4.
3. **Foil is detected between 23% and 69% of the time depending on the
   capture rig**, and every miss silently records the card as nonfoil. See
   §6. This is the largest single source of quiet error in the product.

The compensating property, and it is a real one: **when the scanner speaks it
is almost always right, and its normal failure is silence or a review prompt
rather than a wrong write.** Foil verdicts were 51/52 correct across three
rigs. Border verdicts were 48/53. That asymmetry is designed, and it is what
makes the safe claims in §12 defensible.

---

## 1. How the numbers were produced

Three harnesses, three different things measured. Conflating them is the
easiest way to publish a number that is not true.

| harness | command | what it is | what it does **not** cover |
| --- | --- | --- | --- |
| corpus | `./bin/cardkit-probe --score scan/corpus/manifest.tsv` | 231 clean digital scans, stratified by frame era × border | trigger, glare, focus, perspective, the camera |
| foil | `python3 scan/foil-corpus/eval-finish.py` | 50 clean captures + 74 real hand-held stills from two live sessions | everything outside the sparkle marker |
| fixtures | `./scan/fixtures/sweep.sh` | 28 real photographs, diffed against goldens | accuracy: it pins *decisions*, not correctness |

The corpus caveat is the important one and `scan/corpus/README.md` states it
plainly: these are clean digital scans, so the card fills the frame and the
detector locks onto the inner frame rather than the card edge. **A corpus
number is an upper bound on the parser under perfect capture. It says nothing
about a card on a desk under a lamp.** The two `s5`/`s9` foil rigs are the
only measurements below taken from real hand-held photographs.

One more condition that a marketing number must carry: **the corpus scores
names leniently.** `cardkit-probe`'s name test passes if either string is a
prefix of the other after stripping non-alphanumerics
(`scan/hoard-scan/Sources/cardkit-probe/main.swift:284-286`). So a read of
`Isla` scores as a correct read of `Island`, and `Curse of the Fire Penguin`
scores as a correct read of `Curse of the Fire Penguin // Curse of the Fire
Penguin Creature`. 87% is generous by construction.

Fixture sweep today: **28/28 pass.** The tree is green; nothing below is a
regression.

---

## 2. Accuracy, measured 2026-08-08

`./bin/cardkit-probe --score scan/corpus/manifest.tsv`, 231 images, 17 of them
non-English printings scored apart. Median read 115 ms.

| era | border | n | name | number |
| --- | --- | --- | --- | --- |
| 1998-2002 | black | 8 | 100% | 75% |
| 1998-2002 | gold | 8 | 100% | **0%** |
| 1998-2002 | silver | 8 | 88% | 75% |
| 1998-2002 | white | 8 | 100% | 88% |
| 2003-2014 | black | 8 | 100% | 63% |
| 2003-2014 | gold | 8 | 100% | **0%** |
| 2003-2014 | silver | 8 | 88% | 50% |
| 2003-2014 | white | 4 | 100% | 100% |
| 2015+ | black | 8 | 100% | 75% |
| 2015+ | borderless | 6 | 50% | 67% |
| 2015+ | gold | 8 | **0%** | **0%** |
| 2015+ | silver | 8 | 88% | 38% |
| 2015+ | white | 8 | 100% | 88% |
| 2015+ | yellow | 8 | 100% | 100% |
| pre1998 | black | 29 | 72% | 97% |
| pre1998 | gold | 40 | 90% | 100% |
| pre1998 | white | 39 | 95% | 100% |
| **all English** | | **214** | **87%** | **78%** |
| (non-English) | | 17 | 18% | 71% |

Two rows differ sharply from the 2026-08-03 baseline recorded in
`scan/corpus/README.md` (which was 135 images at 8 per stratum, against
today's 231): **2015+ gold has gone 62% → 0%** and borderless 14% → 50%. The
gold collapse is not a regression: that stratum is now entirely planar-layout
cards (§4). Treat the README table as historical and this one as current.

Border reader, same run: **asked 216, answered 53 (24%), correct 48/53 (90%).**
Coverage and precision are reported separately on purpose; the border is used
for ordering only and can never rule a printing out
(`internal/tui/autoscan.go:874-877`).

Language read (the two-letter code printed in the M15 set row): **asked 231,
answered 16%, and correct on 100% of what it answered.** It is silent on
five cards in six, and never wrong on this corpus.

---

## 3. Foreign-language cards: confirmed, with the mechanism refined

The claim in `docs/app-store-release.md` is true. It is also more interesting
than "OCR fails", because on Latin-script cards **the OCR succeeds**. I probed
all 17 non-English printings in the corpus individually:

```
es   Primal Forcemage      -> Magofuerza primordial
it   Strip Mine            -> Miniera a Cielo Aperto
it   Urza's Power Plant    -> Centrale Energetica di Urza
fr   Sisters of the Flame  -> Sœurs de la flamme
it   Xenic Poltergeist     -> Poltergeist di Xenica
ja   Snakeskin Veil        -> (nothing)
ja   Divine Offering       -> MY DAD
zhs  Ashes to Ashes        -> EUE:
zhs  Forest                -> © 1997 Wizards of the Coast, Inc. All Rights reserved
```

There are two independent walls, and either one alone is fatal.

**Wall 1: the recognizer is English-only, unconditionally.**
`scan/hoard-scan/Sources/CardKit/Read.swift:486` sets
`request.recognitionLanguages = [Locale.Language(identifier: "en-US")]` on
every OCR call in the package; there is no locale plumb-through and no
`customWords` anywhere in `Sources/`. Latin-script languages survive this
because the glyphs are shared: Spanish, French and Italian names read
essentially perfectly above. **Japanese and Chinese do not read at all**: one
returned nothing, one returned `MY DAD`, and the Chinese Forest returned the
copyright line as its title.

**Wall 2: the catalog is keyed on English names and holds no others.** The
bulk ingest is `default_cards` (`internal/catalog/build.go:33`, with the
reason stated at `:29-32`: "all_cards would add every language for no gain").
The fuzzy index is built from `bc.Name` alone (`:435`, `:448`). The
`bulkCard` struct has no `printed_name` field, and a repo-wide grep for
`flavor_name`/`FlavorName` in Go returns **zero hits**. So `Miniera a Cielo
Aperto`, a perfect read, matches nothing, and cannot: the string is not in
the database.

Applying the corpus scorer's own lenient test to those 17 reads, **3 pass, and
one of the three is an artifact**: `Isla` counts as `Island` only because it
is a prefix. The two genuine passes are `Red Elemental Blast` and `Web`,
Spanish printings that happen to carry the English name. **The real figure is
2 of 17.**

Is there any multilingual path? One, and it is narrow. The M15 set row prints
a two-letter language code, read as a closed-vocabulary token
(`scan/hoard-scan/Sources/CardKit/Collector.swift:376-378`, mapped at
`Wire.swift:185-193`), which the Go side can combine with set + number via
`PrintBySetNumberLang` (`internal/catalog/searcher.go:116-156`). That path
needs a modern frame, a legible set row, and a legible number. **Every one of
the 17 foreign printings in the corpus returned an empty language code**:
they are all pre-M15 frames, which print no set row at all. Note also that a
foreign printing of a card that *also* has an English printing is not a
separate catalog row, so it can only ever be recorded as the English printing.

---

## 4. Planes, oversized cards, and cards with no readable title

Confirmed, and worse than the claim: **they are not refused, they are read
wrong.**

The 2015+ gold stratum turned out to be eight planar-layout cards: `punk`
(Black Lotus Unknown Planechase) and `pssc` (Secret Lair Showcase Planes),
both confirmed `layout: planar` via Scryfall. **0 of 8 names correct.** Every
one returned a fragment of the rules box:

```
The Command Zone   -> "who controls"
Mojave Desert      -> "yer searches"
The Windy City     -> "eginning of"
Stroopwafel Cafe   -> "library at any"
```

Probing `The Command Zone` in full shows exactly what crosses the wire: a card
entry with `"name": "who controls"`, candidates `["who controls", "ayers may
put", "ne onto the"]`, and `"confidence": 0`. The `bottomLines` array contains
`"The C"`, `"Plane -"`. The title is *there*, horizontally clipped.

The mechanism, from the code:

- The aspect gate accepts them. `cardAspect = 63.0/88.0` with
  `aspectTolerance = 0.14` (`scan/hoard-scan/Sources/CardKit/Find.swift:73-74`,
  enforced `:136-137`); the plane images measure 1040×1490, ratio 0.698 against
  a normal card's 0.716. So the card *locates*, and every card-space constant
  downstream is then applied to geometry that was never fitted for it.
- The title crop is a fixed top 30% of that assumed geometry
  (`Read.swift:387`).
- `chooseTitle` always returns something: `return lines.first ?? ""`
  (`scan/hoard-scan/Sources/CardKit/Title.swift:34`).
- The type line cannot rescue it: `plausibleTitle` rejects any line whose
  first word is `plane`, `scheme`, `phenomenon`, `vanguard`, `conspiracy`,
  `token` or `emblem` (`Title.swift:17-22`). Those words are a *rejection*
  list, not a layout taxonomy.

**There is no MTG layout list anywhere in the recognition path**, Swift or Go.
The only frame taxonomy is a four-entry footer-geometry table keyed on
copyright year, `CardLayout.leftU`
(`scan/hoard-scan/Sources/BorderKit/CardLayout.swift:75-130`), covering
pre-1998, 1998-2002, 8th Edition and M15. Anything it has not measured returns
`nil` and the dependent readers go silent, which is the correct behaviour and
is documented in place at `:69-74`. Scryfall's `layout` field is used only for
collection browsing and storage migration (`internal/store/migrate.go:648`),
never for recognition.

What saves this in practice is not the reader but the resolver: `who controls`
does not fuzzy-match any card at the 0.88 auto-commit bar
(`internal/cardname/cardname.go:58`), so it queues or drops. Note that the OCR
confidence gate does **not** help here: it is written `c > 0 && c <
autoCommitOCRConfidence` (`internal/tui/autoscan.go:1013`), and a
zero-confidence read is treated as unknown rather than failing
(`:881-883`). The name gate carries the entire load.

---

## 5. Flavor names: a limit nobody had written down

`Bolas's Citadel` (set `fca` #7) read as **`Kefka's Tower`**. That is not a
misread: Scryfall gives that printing `"flavor_name": "Kefka's Tower"`. The
scanner read the printed title correctly, and the printed title is not the
card's name.

`flavor_name` is never ingested (zero Go references), so there is no route
from the printed string to the catalog row. **624 printings on Scryfall
currently carry a `flavor_name`**: every Universes Beyond reskin. The
collector number saves the read when the band is legible; when it is not, the
card is unresolvable by any path the scanner has.

---

## 6. Foil detection

`python3 scan/foil-corpus/eval-finish.py`, run 2026-08-08 against the freshly
built probe. `corpus` is 50 clean captures; `s5` and `s9` are real hand-held
stills from two live sessions.

| rig | reads | foil | nonfoil | verdict acc | commit acc | false-foil | abstain |
| --- | --- | --- | --- | --- | --- | --- | --- |
| corpus | 50 | 32 | 18 | 21/50 | 39/50 (78%) | 0 | 29 (58%) |
| s5 | 39 | 29 | 10 | 21/39 | 29/39 (74%) | 1 | 17 (44%) |
| s9 | 35 | 30 | 5 | 9/35 | 12/35 (34%) | 0 | 26 (74%) |

"Commit accuracy" is the number that matters commercially, because **an
abstention commits as nonfoil.** Derived from the same run:

- **Foil recall, the rate at which a genuinely foil card is recorded as
  foil, is 66% (corpus), 69% (s5), and 23% (s9). Overall 48 of 91 = 53%.**
- **Precision when it speaks: 51 of 52 correct (98%).** One false-foil across
  all three rigs.
- **Nonfoil cards are recorded correctly 32 times in 33 (97%).**

Project memory carried a figure of "roughly 20%" from a live all-foil pile.
That is **s9, and it is still true of s9 today at 23%**, but it is the worst
of three rigs and quoting it alone is as misleading as quoting 69% alone. The
spread between 23% and 69% on identical code is the real finding: **foil
detection is dominated by capture conditions, not by the algorithm.**

The asymmetry is deliberate and is worth understanding before writing copy
about it. The reader can say *foil* three ways: a printed separator glyph on
an M15 set row
(`scan/hoard-scan/Sources/CardKit/Collector.swift:236-241`), a luma template
correlation ≥ 0.52 with contrast ≥ 0.02, or a chroma contrast ≥ 0.08 with luma
contrast ≥ 0.012 (`BorderKit/Sparkle.swift:246-254`, constants in
`BorderKit/BorderReading.swift:122-273`). **It can never say *nonfoil* from a
low score.** Silence stays silence across the wire
(`Collector.swift:38-44`, `Wire.swift:59-63`), and then the Go side applies
the default:

```go
func finishFromEvidence(card scryfall.Card, hint string) (finish string, evidenced bool) {
	finishes := finishOptions(card)
	if len(finishes) == 1 { return finishes[0], true }
	if hint != "" && slices.Contains(finishes, hint) { return hint, true }
	return "nonfoil", false
}
```
`internal/tui/autoscan.go:1047-1056`. The comment immediately above it is the
honest statement of the limit: old frames "carry no set/language line, so no
marker ever reaches here and **every old foil records as nonfoil, silently,
and foil is worth a multiple**."

The guess is remembered: the `evidenced` flag survives as
`Result.FinishGuessed` and is persisted by `st.RecordFinishGuess`
(`add.go:197-198`), which is what the `hoard guessed` command lists. **That
command is the mitigation, and any claim about foil accuracy should mention
it.**

There is no CoreML model in the shipping path; grep for
`CoreML|mlmodel|VNCoreML|CreateML` across the Swift package returns zero. A
CreateML classifier exists only as an eval-time experiment outside the package
(`scan/foil-corpus/train-foil.swift`). Both acceptance bars are pinned by
single cards on each side, and the source says so
(`BorderReading.swift:137-139`: "0.015 above the worst known nonfoil and 0.013
below the weakest true foil… Widen the corpus before trusting it further").

---

## 7. Collector numbers, set codes, and printing variants

78% exact on clean English scans. The failures are not evenly bad, and the
distribution is what a claim needs. Of 62 failing corpus rows:

- **36 read no number at all.** Silence, and on pre-1998 cards silence is
  *correct*, since those cards print no collector number
  (`Collector.swift:19-22`, and the scorer counts empty as correct for that
  era at `main.swift:287-288`). pre-1998 scores 97-100% on number for exactly
  this reason; that era's difficulty is not reading the card, it is that the
  card carries nothing to say *which printing* it is.
- **19 stripped a prefix or a variant marker**, keeping the right numeric
  core. World Championship decks print player-prefixed numbers (`kb310`,
  `shh347`, `mb62sb`) and every one lost its prefix, which is why both gold
  rows score 0% on number. Variant markers vanish likewise: `18★ → 18`,
  `130p → 130`, `12f → 12`, `113d → 113`, `82a → 82`. **The scanner cannot
  distinguish a printing whose only distinguishing mark is a suffix.** This is
  a real money limit: `war/97` and `war/97★` are $8.06 and $113.45
  (`internal/catalog/searcher.go:103-105`).
- **5 read a confidently wrong number**: `Skittering Skirge` 4 → 312,
  `Mox Ruby` 2008 → 819, `Elemental` 7 → 73, `Goblin Mime` 78★ → 2120,
  `Pearled Unicorn` 31 → 212. **That is 5 in 214, or 2.3%**, and it is the
  only category here that can attach a card to the wrong printing without any
  other signal disagreeing.

Old-frame vs M15: the modern frame is the easier read on *numbers* but the
corpus does not show a clean modern advantage on *names*, because the modern
strata contain the hard layouts (planes, borderless, Un-sets). pre-1998 name
misses are character-level OCR errors on the old typeface:
`Frenetic Efreet → Frenetic Chee`, `Vesuvan → Yesuvan`,
`Prismatic Boon → Prismatic Door`, `Samite Healer → Samnite healer`. Note that
`Prismatic Door` is a plausible-looking string that is not a real card; the
fuzzy matcher's 0.7 similarity floor and 0.88 auto-commit bar are what stop it.

Character repair is deliberately conservative: `digitLookalikes`
(`CardKit/Text.swift:19-26`) folds `O→0`, `l→1`, `S→5` and so on, but **only in
positions the grammar has already decided are numeric**, guarded by
`dropped <= 1 && real * 2 >= considered` (`:67`). The measured reason is in the
source: without that guard, "cards that print no collector number at all
acquire one, measured, on 18% of the corpus's pre-1998 gold-bordered
stratum." A lost *leading* digit is repaired (`numberTailMatches`,
`internal/tui/autoscan.go:1370-1387`); substitutions and insertions are
deliberately not, and those cards queue.

---

## 8. Condition and grading: assessed nowhere

**The scanner does not assess a card's condition. It does not assess wear,
edges, centering, surface, or damage, and it assigns no grade.** This is not a
tuning gap, it is an absence: there is no such field on the wire.

- `scan.Card` and `scan.Event` (`internal/scan/scan.go:35-127`, `:185-282`)
  carry name, candidates, confidence, set code, collector number, number
  source, copyright year, border colour, frame style, finish hint, sparkle
  telemetry, language, and trigger reasons. Nothing physical.
- `tui.Result`, the struct handed to the adder, has no condition field
  (`internal/tui/tui.go:101-124`).
- Everything scanned lands as `condition = 'unknown'`, the column default
  (`schema/sqlite/schema-latest.sql:26`).
- The code says it outright, at the one place the scan path names condition at
  all: "The scan only ever corrects what it just wrote, which is unassessed by
  construction: **a camera cannot judge wear**" (`add.go:177-178`).

The condition vocabulary that exists (`unknown|nm|lp|mp|hp|dmg`) is reachable
only through import files and manual editing.

**Grading is a different noun and is unbuilt.** `docs/graded-cards.md` is
marked "Status: proposal, nothing built"; hoard currently reads a grade from an
import file and discards it. Never describe a card's condition as a "grade" in
user-facing copy, and never imply the scanner produces either.

---

## 9. Capture quality: lighting, glare, sleeves, focus

The scanner has real capture gates, but **none of them is a glare or blur
metric on the still.** What exists:

- **Focus and motion freeze the trigger** rather than degrade it:
  `focusSettled` and `rigMoving` halt the stability machine
  (`CardKit/Trigger/Trigger.swift:507`, `:614-619`). The comment at `:60-61`
  records why: "with no focus state, so a hunting lens fed blur into the streak
  as if it were stillness."
- **Scene detail floor** `sceneDetail = 12.0` (`Trigger.swift:131`) suppresses
  the shutter on a bare desk.
- **Aspect and size gates** reject a bad flatten (`Find.swift:73-74`, `:94`).
  When they trip, the reader falls back to OCR on the raw frame with
  `located = false` and does **no** positional work: no band, no border, no
  sparkle (`Read.swift:103-113`).
- **The border reader has nine named abstention gates**, including
  `minCardHeightPx = 500` and `maxThetaDegrees = 25`
  (`BorderKit/BorderReading.swift:78-116`, judged in `ReadBorder.swift:200-262`).
  It declines rather than guesses.
- **Clipping is measured but never gates.** `clipHigh`/`clipLow`
  (`BorderKit/BorderSampling.swift:130-131`) are the nearest thing to a glare
  check in the tree and they are telemetry only. There is a one-shot −2EV
  retake verb on the wire (`ScanWire/ScanCommand.swift:42-45`), remediation
  rather than a gate.
- **Sparkle has a capture-quality floor**: `acceptLumaContrast = 0.02`,
  described in source as "the capture-quality floor below which the patch is
  washed out" (`BorderReading.swift:222-224`).

**Sleeves: unverified.** Nothing in the tree mentions sleeves, and no corpus or
fixture is labelled for them. A sleeve's specular reflection would plausibly
hit the same washed-out-patch path that costs foil detection, but that is a
hypothesis and has never been measured. **Do not claim sleeve support either
way.**

The corpus README records one hard-won lesson worth repeating to anyone
tempted to extrapolate from clean scans: a border-saturation gate that
measured perfectly on scans (gold 0.36 vs ≤0.20) "was wrong the first time it
met a photograph, where a *white* border under warm light reads 0.40."

---

## 10. Double-faced cards, tokens, oversized cards

| case | status | evidence |
| --- | --- | --- |
| **Double-faced** | Front face only. No concept of a back exists in the Swift package. | Zero DFC references in `scan/hoard-scan/`; art index stores only `CardFaces[0]` (`internal/scryfall/scryfall.go:495-505`) |
| **Borderless DFC** | Measured failure. Both borderless DFCs in the corpus (`Mondo Gecko // Mondo Gecko`, `Rhystic Study // Rhystic Study`) returned an **empty** title. | corpus run, 2026-08-08 |
| **Tokens** | Not handled. `token` and `emblem` are title *rejection* words, so a token whose top line is its type loses its title and `chooseTitle` falls through to whatever line is first. | `Title.swift:17-22`, `:34` |
| **Oversized (planes, schemes, Vanguard)** | Read wrong, not refused. See §4. | corpus: 0/8 |
| **Un-sets / silver border** | 88% name across eras, since the layouts break the usual assumptions on purpose. `B.F.M.` returned an empty title; `B.O.B. (Bevy of Beebles)` read `Bery of Beebles)`. | corpus run |
| **World Championship (gold border)** | Names read 100%, numbers 0%, every era. | corpus run |

The one guard that keeps junk off the wire is `cardEntry`
(`CardKit/Wire.swift:97-105`), which refuses to emit a card unless the band
read something or some line is a plausible title. It stopped a capture whose
"title" was `"0/1"`. It did not stop the planes.

---

## 11. What happens on a miss

The short answer for copy: **most misses are silent drops, uncertain reads go
to a review queue, and a small deliberate set auto-commits a guess.** All three
happen, under conditions worth knowing.

**Silent drops** are the most common outcome: eight distinct drop paths in
`onResolveDone` (`internal/tui/model.go:1601-2236`), each writing only a
terminal status line. Notably a capture that named nothing is killed even when
the footer read a set code (`:2132-2138`); it buys a receipt line, not a queue
slot.

**Review** is the queue append at `model.go:2193`. During a hands-free session
the *flash to the phone* is deliberately deferred and expires after
`decisionCeiling` = 1000 ms (`:2186-2205`), so a review is not always a
prompt the operator sees in the moment.

**Auto-commit of a guess** happens in three named places, each documented as a
trade rather than an accident: an ambiguous collector number commits the head
printing (`autoscan.go:938-964`, "a deliberate trade… on the operator's
preference for false positives over false negatives"); an unread finish
commits the nonfoil default (`:1017-1035`); and an exact name on a
sole-printing card commits in spite of a collector number that matched nothing
(`:791-817`, `:858-861`). Both guesses are flagged (`FinishGuessed` and
`PrintingGuessed`) and surfaced by `hoard guessed`.

**The title-steal bug is fixed.** Live on 2026-08-07, `"Gliding"`, debris of a
*Glowrider* sliding past the lens, resolved to the real card *Gliding Licid*
and that stolen name keyed every downstream dedupe. The fix landed in
`ae10fb3` ("fix: pre-launch red list"), and the working tree matches HEAD for
those files. `Plausible` no longer accepts a prefix as an identity
(`internal/cardname/cardname.go:110-121`, which names the incident); both
searchers now return `Match{PrefixOnly: true}` with an explicit "callers MUST
check" contract (`internal/catalog/searcher.go:226-231`); `resolveName` refuses
a prefix and only *nominates* from line 0 (`internal/tui/autoscan.go:645-650`);
and a nomination becomes an identity only if a band-sourced collector number
verifies it (`:725-737`).

**It is not hermetically closed**, and copy should not imply it is. Three
residual paths, all narrow:

1. A confirmed nomination will still commit if debris from card A nominates a
   card whose printing happens to match digits read from card B's sliver
   (`autoscan.go:725-737`).
2. The reverse-prefix allowance, `len(c) >= 8 && strings.HasPrefix(o, c)`
   (`cardname.go:127`), accepts an OCR line that *begins with* a full card
   name, bypassing both the length-ratio veto and the similarity floor.
3. A sub-0.88 fragment that carries a collector number skips the slide-window
   drop (which requires `CollectorNumber == ""`, `model.go:2071-2073`), and if
   that number verifies, `corroboratedPrinting` waives the name gate entirely
   (`autoscan.go:1002`).

---

## 12. Claims that are safe to make

Each of these is defensible against a measurement in this document. Prefer the
sentence as written; the qualifiers are load-bearing.

- "Reads modern English cards in good light, in about a tenth of a second."
  *(median read 115 ms, §2)*
- "On a labelled corpus of 214 English printings spanning every frame era,
  the reader identified the card name on 87% and the exact collector number on
  78%." *(§2: always state the corpus and that these are clean scans)*
- "Recognises cards from every frame era, from 1993 originals to current
  sets." *(§2: no era scores below 72% on name)*
- "When the scanner is unsure it asks rather than guesses: uncertain reads go
  to a review queue instead of into your collection." *(§11)*
- "The foil detector is conservative by design: across three test rigs it was
  correct on 51 of the 52 cards it gave a verdict on." *(§6)*
- "Cards it records on a guess are flagged, and `hoard guessed` lists every
  one." *(§6, `add.go:197-198`)*
- "It never invents a collector number for a card that does not print one."
  *(§7: pre-1998 scores 97-100% on number precisely because silence is the
  right answer, and the guard is measured)*
- "Everything runs on device: no card image leaves your phone and your Mac."
  *(orthogonal to accuracy, but true; see `docs/data-licensing.md`)*

## 13. Claims to avoid

- ~~"Scans any Magic card."~~ Planes and oversized cards are read wrong, not
  refused (§4), and foreign-language cards cannot resolve at all (§3).
- ~~"Works with cards in any language."~~ / ~~"Multilingual."~~ Two
  independent walls; the catalog physically does not contain non-English names
  (§3).
- ~~"98% accurate"~~ or any single headline percentage without the corpus and
  the capture conditions attached. The same code measured 23% and 69% foil
  recall on two rigs on the same day (§6).
- ~~"Detects foil automatically."~~ True only as a one-way claim. It detects
  *some* foils and silently records the rest as nonfoil (§6). If foil is
  mentioned at all, mention `hoard guessed` in the same breath.
- ~~"Grades your cards"~~ / ~~"assesses condition"~~ / ~~"tells you what
  condition your cards are in."~~ Assessed nowhere; a camera cannot judge wear
  (§8). "Grade" is a different noun again and is unbuilt.
- ~~"Identifies the exact printing."~~ It cannot distinguish printings that
  differ only by a variant marker (`97` vs `97★`), and it strips World
  Championship player prefixes entirely (§7).
- ~~"Reads both sides of double-faced cards."~~ Front face only, and the two
  borderless DFCs in the corpus returned empty titles (§10).
- ~~"Works through sleeves."~~ Never measured, either way (§9).
- ~~"Never records the wrong card."~~ 2.3% of clean-scan reads produced a
  confidently wrong collector number (§7), and three narrow paths can still
  commit a card that was not in frame (§11).

---

## 14. Unverified, and what it would take

Recorded so a later reader knows these are gaps in the evidence, not in the
product.

| claim | status | to verify |
| --- | --- | --- |
| Sleeve behaviour (any kind) | **Unverified.** Nothing in the tree references sleeves; no labelled captures. | Add a sleeved stratum to `scan/foil-corpus/stills-labels.tsv` and re-run `foil-eval` |
| Glare tolerance | **Unmeasured as a variable.** `clipHigh`/`clipLow` are computed and thrown away. | Gate or bucket by clip fraction and re-score |
| What separates rig `s5` (69% foil recall) from `s9` (23%) | **Unknown.** The 3× spread is the largest unexplained effect in this document. | The rigs' capture conditions are not recorded in `stills-labels.tsv`; they would have to be reconstructed from session telemetry |
| Borderless cards in the wild | **Corpus is not valid for this.** The 50% figure in §2 is a full-bleed-scan artifact: a borderless scan has no card-against-desk edge. `docs/specs/scanner-tuning.md` reports a live borderless session reading names fine. Not re-verified today. | A live borderless pile with telemetry |
| Real-world (non-corpus) name accuracy | **No end-to-end labelled measurement exists.** The 28 fixtures pin decisions, not correctness. | A labelled live pile scored against ground truth |
| Japanese/Chinese OCR | **Refuted rather than unverified**, but on n=4. | A larger non-Latin stratum, if it ever matters |

---

## Re-running everything in this document

```sh
swift build -c release --package-path scan/hoard-scan --product cardkit-probe
task cardkit-score                 # §2, ~30s   (add -- --misses for §7's breakdown)
task foil-eval                     # §6, ~20s   (add -- --misses)
task scan-check                    # the 28-fixture regression sweep
```

`task` is not on the default PATH here; the binary lives at `.tool/task` after
`task tools`, or run the underlying commands from `Taskfile.yaml:201-256`
directly. All three are read-only, so do **not** pass `--update` to
`scan/fixtures/sweep.sh`, which regenerates goldens.

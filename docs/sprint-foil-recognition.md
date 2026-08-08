# Sprint: foil recognition — round two, and the untried levers

PLANNED (future sprint). The state of the problem, measured across four live
sessions and 124 labelled captures (ledger: `docs/scanner-tuning.md`): the
template gates ship at ~1/14 recall on hand-held foil piles; the CoreML
classifier hit 79/89 held-out recall but was *certainly wrong* (p=1.0000) on
one nonfoil per hand-held rig, so it is parked eval-only by the standing
zero-false-foil gate; art-match settles finish only for single-finish
printings, of which the test pile has none. Refuted — do not re-propose:
whole-region chroma spread, wider sparkle windows, chroma-score cross-rig
voting, torch illumination.

## Stage A — classifier round two (the known path)

What the post-mortem says to change, in order of expected value:

1. **More nonfoil data.** 33 nonfoil crops total, 5 on the hand-held rig —
   the model was certain-and-wrong exactly on the class it barely saw. A
   deliberate nonfoil pile session per rig (20+ cards) before any retraining.
2. **Per-card voting.** The fatal s9 error was one of three Ornithopter
   crops; its siblings classified correctly. Score the eval per *card*
   (2-of-N across a card's crops), matching what the live pipeline actually
   sees. Note the limit: s5's two errors are single-crop cards — voting
   without data fixes nothing there.
3. **Chroma-plane input.** Train on the warm-cool channel crops beside RGB —
   the template work measured it 5-13× more angle-stable than luma on
   exactly these markers.
4. Same harness (`extract-crops.py`, `train-foil.swift`, `sweep-threshold.py`),
   same gate, unchanged: zero false-foils on every held-out rig or eval-only.

## Stage B — temporal shimmer: TRIED AND CLOSED (2026-08-07, three live fits)

Built as measurement-only telemetry and fitted against three labelled pile
runs on the same rig; removed after the third negative, by prior agreement.
The record, so nobody rebuilds it without new physics:

1. **Raw accumulation** (12-frame window, point-sampled grids): chroma range
   tracked luma range ~linearly — the window spanned the placement slide.
   Nonfoils on top (Hollow Specter 33 = run max).
2. **Stillness-gated** (adds only at ≥90% box overlap): magnitudes fell
   (max luma 134→86), correlation stayed. Diagnosed cause: the grid
   point-samples one pixel per cell, so 1-2px in-hand micro-motion aliases
   into both channels — a mechanism the codebase had already documented for
   `holdScene`.
3. **Area-averaged** (4×4 per cell, the aliasing fix): still no separation —
   nonfoil chroma to 19 vs foils 2-22; only Dress Down (20) and Meltdown
   (22) above the best nonfoil, margins 1-3 points; chroma/luma ratio best
   margin 0.02 on one session.

The conclusion with the physics: in-hand micro-motion moves *content* through
the cells faster than diffraction moves *colour* at any sampling this side of
optical flow. The channel would need per-pixel registration between frames —
a different project entirely. The remaining foil levers are Stage A's data
work, Stage C's cheap physics (polarizer first), and the EV bracket below.

## (removed) Stage B design notes — superseded by the record above

Deferred from the ML sprint, and both single-frame approaches have now failed
on exactly what it measures: a foil's diffraction changes *color* with angle;
ink does not. The hand-held pile supplies micro-motion; the 30fps tap frames
already flow through `TriggerRunner` and are discarded. Accumulate per-region
hue variance across ~10 frames; report `shimmerScore` on the wire; fit its
gate on a live labelled session. Hue-shift, not brightness, is the
discriminator — hand-shake gloss on nonfoils varies in brightness only.

## EV-bracketed retake: TRIED AND CLOSED (2026-08-07, 20:09 run)

Built end-to-end (`evbias` verb → one-shot exposure bias, restored after the
capture; auto-fired on retro finish-guessed commits) and run live: 16/16
dark retakes fired, **zero corrections**. The −2EV frames scored *lower* —
foil luma −0.08..0.46 all under the 0.52 bar, chroma contrast ≤0.039 under
0.08 — refuting the clipped-highlight hypothesis: the glare wash is light
*geometry*, not sensor saturation, and no exposure recovers a signal that
never reaches the lens. The gates held at zero false positives throughout
(nonfoil Ornithopter's retake read 0.497, the run's highest). The `evbias`
verb and `Session.EVBias` remain for future exposure experiments; the
auto-trigger was removed.

Three independent closures now agree — shimmer (3 fits), EV bracket, and the
template's own cross-setup history: **the marker's light is not arriving at
the lens on this rig. Software cannot read what optics do not deliver.**

## The remaining levers, ranked (2026-08-07, end of sprint)

1. **Session finish default** (workflow, ~an hour, exact on sorted piles).
   The operator knows the pile is foil before the first card; a session
   `--finish foil` default-on-silence with one-key per-card override beats
   every detector for sorted inputs. Detectors are for mixed piles. This is
   the pragmatic answer to the standing 10-false-negative complaint.
2. **Lamp geometry** (free, then hardware). Position dominates the record:
   Glowrider read 0.50 / 0.83 / −0.01 across the three setups. One session
   with the lamp deliberately repositioned answers whether the markers can
   be lit again; the principled fix is a **ring light** at the lens —
   coaxial illumination returns diffraction to the camera by construction.
3. **Polarizing filter** (~$15). Specular glare is polarized, diffraction is
   not; a clip-on should strip the wash while passing the sheen — cleaning
   the input for every detector already built, including the parked
   classifier. Highlight-masked hue (below) stays gated on this data.
4. **Exit-sweep sparkle** (the strongest remaining software idea). Every
   card *leaves* by tilting through an angle sweep the trigger watches and
   discards. Run the already-fitted marker correlation on tap frames during
   the removal window, take the max across the sweep, feed the existing
   guess→evidence re-key as late finish evidence. Not a new statistic — the
   proven detector sampled at a dozen angles instead of one.
5. **Burst-at-shutter** (weaker cousin of 4): three stills over ~150ms, max
   sparkle across hand-tremor angles.
6. **Optical-flow-registered temporal** (heavy; the shimmer record's
   "different project" — only if 1-5 all disappoint).

Closed by measurement — do not re-propose without new physics: single-frame
statistics beyond the fitted gates (template history), whole-region and
score-channel chroma (corpus), temporal shimmer at any grid sampling (three
fits), exposure bracketing (this entry), torch illumination.

## Stage C — untried levers, cheapest physics first

1. **Polarizing filter on the phone lens** (~$15, zero code). Specular gloss
   is polarized; diffraction is not. A polarizer should strip the white
   glare that saturates the marker while the foil's own colored sheen
   survives — potentially transforming *every* existing detector's input.
   One labelled session with a clip-on filter answers it.
2. **Exposure-response probing on the rescue retake.** The retake exists;
   make its second still darker (EV bracketing verb to the phone).
   Diffractive highlights collapse differently than ink under -2EV — two
   exposures of one card give a response curve no single frame carries.
   (Distinct from the refuted torch: no added light, just measured response.)
3. **Foil curl geometry.** Foils physically bow; the segmentation quad and
   flatten aspect already measure the card's shape. A bowed-edge signal is
   angle-independent and free at capture time. Weak on a pressed pile,
   possibly decisive in hand — measure before believing.
4. **Highlight-masked hue** (carefully — near refuted territory). The
   refuted measure was hue spread over whole regions. The variant: hue *of
   the brightest band only* — foil glare bands are rainbow, nonfoil glare is
   the lamp's neutral. Only worth trying with the polarizer data in hand.

## Non-detector mitigation (cheap, this-sprint-eligible)

Price-asymmetric confirm: when a finish is guessed on a printing whose foil
price is a large multiple of nonfoil, hold the one-key `pendingDup`-style
affordance instead of silently writing the default — the cost of a wrong
guess is not symmetric, and the operator said the wrong rows are the expense.

## Verification

Every stage: labelled live session (s9 protocol — operator supplies ground
truth per card), gates fitted held-out, zero false-foils standing, ledger
entries with the numbers. Stage outcomes are independent; any one can ship
alone.

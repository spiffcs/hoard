# Pre-public review — the last gate before flipping visibility

Where the launch stands and what remains. A public repo is a permanent
artifact: the history, the first impression, and the licensing exposure all
start the moment visibility flips. Everything below is ordered so nothing
irreversible happens before it has been read by a human.

## Where we are (2026-08-08)

- **Release pipeline: proven.** `v0.1.0-rc2` went green end to end — build,
  Developer ID signing, Apple notarization, cosign/sigstore provenance, GitHub
  release, and the `install.sh` upload to R2 behind `tools.aithirne.com/hoard`.
  The signed binary was verified to pass `codesign --verify` and
  `cosign verify-blob`. See `docs/release-engineering.md` (rc2 section) for the
  full record and the cert gotcha behind it.
- **Secrets: done.** Five Apple secrets in the `production` environment, three
  R2 secrets, all validated by a real run.
- **History: team ID scrubbed** from all commits (`git log --all -S` is clean).
  One cleanup still owed — see the flip steps below.
- **Repo: still private.** The installer's `curl | sh` cannot work yet, by
  design: `/releases/latest` ignores prereleases and a private repo 404s
  unauthenticated. Both resolve at public + a non-prerelease tag.

## Step 1 — full repo walkthrough + docs refresh (next)

Read **every line of code and every doc** before the repo is permanent. Tools
catch some of this; a person reading catches the rest. Specific things to hunt,
each grounded in something already known about this codebase:

- **Secrets and local paths.** Re-run the Stage 0.2 sweep
  (`git ls-files | grep -iE '\.db$|\.env|secret|token|credential|Signing\.xcconfig'`)
  and eyeball for absolute home paths, machine names, and internal URLs in
  comments and fixtures.
- **Licensing correctness.** The Fan Content notice and credits are in
  `README.md`, `LICENSE`, and `hoard version` — confirm they still read as
  `docs/data-licensing.md` §7 specifies, and that the LICENSE keeps its
  code-vs-card-imagery split.
- **Card imagery.** `scan/fixtures/*.png` and `scan/foil-corpus/cards/*.png`
  are WotC-owned photographs kept on purpose under the Fan Content Policy
  (`docs/data-licensing.md` §6). Confirm the tracked set is only what the audit
  cleared, and that the bulky corpora (`scan/corpus/images`,
  `scan/foil-dataset/images`, `scan/foil-corpus/full|stills`) are still
  gitignored.
- **Dead doc links.** 16 references to nonexistent `docs/*.md` were removed from
  shipped code this cycle. This walkthrough is where the docs themselves get
  rewritten (or the remaining stubs get created) — nothing in code should point
  at a doc that will 404 on a public repo.
- **README accuracy.** The install one-liner only works post-flip; the section
  is written for that state. Confirm the short URL, the cosign incantation, and
  the "release binary is the CLI; scanning needs the Swift helper" sentence all
  match reality.
- **Comment hygiene.** TODO/FIXME/HACK, placeholder names, anything written for
  an audience of one that reads wrong in public.
- **Deferred risk is documented, not hidden.** `docs/audit-yellow-backlog.md`
  records what was consciously left for later and why — confirm it is current.

### The flip itself (owner-only, after the walkthrough)

1. **Cached-SHA cleanup.** Force-pushing scrubbed the live history, but GitHub
   can still serve the pre-scrub commits (with the raw team ID) by direct SHA
   until it garbage-collects. Recreate the repo from the clean local history, or
   ask GitHub support to gc, before flipping. Low-stakes for a team ID, but it
   is the clean move.
2. `gh repo edit spiffcs/hoard --visibility public --accept-visibility-change-consequences`
   — only with explicit go-ahead in the same session.
3. `git tag v0.1.0 && git push origin v0.1.0` — a non-prerelease tag, so
   `make_latest` applies and the installer's `/releases/latest` resolves. The
   pipeline signs, notarizes, publishes, and pushes the install script from
   there.

## Step 2 — Hoardling (the Swift app) available at launch

The README promises card scanning, so the scanner must exist the day the repo
goes public — a dead promise on the landing page is worse than no promise.

- **Read every line of `scan/`,** same standard as Step 1 — the macOS helper
  (`scan/hoard-scan`) and the iPhone app (`scan/hoard-scan-ios`). This is a
  second walkthrough, not a skim; the Swift side carries the camera, the
  network link, and the pairing gate.
- **iPhone app distribution.** The app goes through the App Store, not a GitHub
  release — see `docs/app-store-release.md` for the submission chores (bundle
  id, deployment target, versions). The Developer ID work just completed
  unblocks the *macOS helper's* signing; the iPhone app still needs its own App
  Store provisioning.
- **macOS helper.** Built from source on a Mac (`docs/ios-development.md`); it
  ships in no release archive. The README must say so plainly.
- **Open Swift risk to triage against launch.** The audit's deferred Swift
  items — capture-queue performance (C1/C2), English-only OCR and hand-fitted
  geometry (C8), and the CoreML/pHash recognition sprint — are recorded in
  `docs/audit-yellow-backlog.md`. Decide which, if any, block a public launch
  versus ship as known post-launch work.

## Order of operations

Step 1 (walkthrough + docs + flip) and Step 2 (Swift parity) converge on one
moment: the repo goes public and Hoardling is installable at the same time. Do
not flip visibility until Step 2 has a credible availability date — otherwise
the public README advertises a scanner nobody can get.

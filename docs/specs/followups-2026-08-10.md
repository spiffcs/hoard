# Follow-ups — 2026-08-10

**Status: open, none blocking.** What is left after a day that landed the
JSON card-detail work, percent watches, the three-table watches screen, the
pairing-guess deletion, the flag audit's fixes, and the live browse refresh.

Each item says where it came from, because several were found sideways while
measuring something else and that is usually the sign they are real.

Line numbers drift; verify before editing.

## Code, small and specified

**`Card`'s doc comment never reaches a schema consumer.** `AddGoComments`
truncates a TYPE's comment to its first sentence via `go/doc.Synopsis`, while
FIELD comments survive whole. So `$defs/Card`'s published description is
"Card identifies one printing in one finish." and the three sentences after it
— including *"SetCode and Number are the ecosystem's own values, so a document
joins directly against Scryfall bulk data or MTGJSON's AllPrintings"* — have
never been seen by anyone reading the schema. That sentence is the join
guidance an agent most needs.

Repairing it means moving prose onto fields, which changes the emitted schema,
so it wants its own diff. Several other types are in the same position; the
sweep is part of the work. Found while implementing card facts, where the same
truncation nearly swallowed a disclosure that `facts` is absent in a `hoard`
document.

Rule for whoever writes schema prose next: **rationale on a type is for the
reader of the Go file; anything a consumer must know goes on a field.**

## Release engineering

**The merge content hash moved twice and wants a release note.** Two changes
this round each moved it independently: emitting `colorIdentity: []` where the
key had been absent, and resetting `schemaVersion` from 1.1.5 to 1.0.0. Both
are inside the bytes `ContentHash` covers.

The consequence is one-time and at this version boundary: a source database
merged under an older binary hashes differently under the new one, so
`RefuseReimport` finds no ledger row and a re-merge of an *unchanged* source is
permitted, doubling every quantity.

Bounded, and measured: `merge.go`'s own comment already concedes the guard is
this fragile, and the owner's database has 23 ledger rows, **none of them a
`.db`** — no merge has ever been recorded there, so today's blast radius is
zero. The note is for anyone else.

`merge --dry-run -o FILE` writes exactly the bytes `ContentHash` consumes, so
a hash question here can be measured rather than argued.

## Performance, highest leverage and unowned

**`AllByFinish` is 35ms and mostly page-walks `raw_json` it never reads.**
Measured at 34.3ms against a real hoard, linear in entries — 218ms at five
times the size, 404ms at ten. The cards table carries about 9MB of stored
document that this query does not touch.

It surfaced twice from unrelated directions: as the window that forced the live
refresh's quiescence gate, and again as the reason that window is the size it
is. Shrinking it would make the live refresh cheaper, `r` faster, the
analytical views snappier and the CLI's own reads quicker — a broader win than
anything else on this list.

Not attempted. The obvious shapes are a covering index, or moving `raw_json`
out of the row the hot queries scan.

## Flaky, and it will bite CI

**`internal/scan/link` fails intermittently under load.** Different tests on
different runs — `TestBrowseForANamedPhoneStopsWhenItAppears`,
`TestBrowseForAnAbsentPhoneServesTheWindow`. The package binds mDNS and is
timing-sensitive.

Established as ambient rather than caused, by an interleaved fair comparison:
a lane ran its own tree and a `git archive` of its base alternating on the same
machine under the same load, and got 3/3 clean on both. So it is not a
regression — but it is real, reproducible under contention, and every lane this
round has had to be told "re-run before concluding, it is not yours."

## Deferred deliberately

**Extracting the filter grammar.** `internal/browse/filter.go`'s parser could
serve `export` and others; the audit costed it at about a day and found the
expensive-sounding half already done — `store.TraitFilter` and
`store.MatchingCardIDs` have been exported all along, so what remains is a
pure ~200-line parser and a struct field set.

Deferred because demand is one declined proposal, because `export` would end up
with a grammar for rows and flags for columns once field selection lands, and
because `filter.go:57-59` records that OR was omitted on purpose for a bar you
type one-handed — exporting the grammar exports that constraint into a place
where it stops applying.

Revisit when the next `--min-price` arrives. It stays cheap.

## Needs hardware, not code

Three unrelated items, one sitting:

- **iOS reconnect** (`14a78cd`) has never run on a phone. The test is concrete:
  pair, scan, leave the add flow, re-enter, `ctrl+o` — expect a reconnect with
  no code prompt.
- **Tier sounds** (`718da8a`) likewise, and the feature as a whole has never
  had a live run.
- **The pairing route.** Deleting the `NeedsPairing` guess is proven at the
  model layer and by in-process handshake tests, but no real refused handshake
  has been observed. Worth pairing with a **factory-reset phone**, since that
  is the misread direction that used to reassure.

## Needs a terminal, not a test

- **The three-table watches screen** has never rendered live. Test stdout
  resolves to Ascii, so every lipgloss render in the suite is a no-op and the
  cursor bar's appearance against real ANSI is unexercised.
- **The live browse refresh.** Whether 500ms and 750ms *feel* right, whether
  the status delta is legible where it lands, and whether overwriting a
  fired-watches banner with a refresh notice is acceptable are product
  judgements made without looking at them.

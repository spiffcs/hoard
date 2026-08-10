# Scryfall rate limits: can hoard get itself blocked?

Audited 2026-08-09, prompted by a `503 Service Unavailable` seen during a live
box scan. Everything below is measured or read out of the code — no claim here
rests on a comment.

Companion to [`data-licensing.md`](data-licensing.md) §2.3, which recorded the
limits and two violations against them. Those violations are now **fixed**; this
document is the re-audit.

## Bottom line

**hoard did not cause that 503, and hoard is not blocked.** A live probe during
the audit returned `200` in 63 ms.

Scryfall answers rate limiting with **429**, not 503 — and hoard already handles
429 distinctly (`scryfall.go:453`). A 503 is their origin, not our traffic.

The one structural gap worth closing: the pacer budgets **per endpoint class**
with no ceiling on `api.scryfall.com` as a whole, which under the stricter
reading of Scryfall's guidance permits **16 req/s**. No workflow hoard ships
today reaches it, but nothing in the code prevents it either.

## 1. The choke point holds

Every Scryfall API call in the repo funnels through `apiDo`
(`scryfall.go:233`), which calls `apiPacer.wait` before it does anything else.
All seven call sites route through it:

| Function | Endpoint | Callers |
| --- | --- | --- |
| `FetchCard` | `GET /cards/{set}/{num}` | `action/add.go:160` |
| `FetchCollectionProgress` | `POST /cards/collection` | `action/import.go:36`, `action/updateprices.go:19`, `resolve/resolve.go:74` |
| `Autocomplete` | `GET /cards/autocomplete` | `command/searcher.go:121` |
| `NamedFuzzy` | `GET /cards/named?fuzzy=` | `command/searcher.go:138` |
| `SearchPrints` | `GET /cards/search` | `command/searcher.go:124` |

There are **no goroutine pools, no `errgroup`, no `WaitGroup`** anywhere in
`internal/scryfall`, `internal/resolve`, `internal/catalog`, `internal/action`
or `internal/tui`. Concurrency reaches the API only through Bubble Tea's
per-`Cmd` goroutines, and the pacer serialises those under one mutex.

Every `Resolve` call site batches a whole file or deck into one
`FetchCollection`, which chunks at 75 identifiers. Nothing issues one request
per card.

**The phone never talks to Scryfall.** There is no `URLSession` and no outbound
`https://` in the entire Swift tree; the iOS app's only network peer is the Mac.

### §2.3's violations are closed

The `chunkPause` constant that audit flagged (150 ms against a 500 ms limit) no
longer exists — `grep` finds no occurrence. The unpaced `named`/`autocomplete`
paths it flagged now go through `apiDo` like everything else. The pacer replaced
all four findings.

## 2. Measured behaviour

Probe: local `httptest` server, real gaps restored (`slowGap` 500 ms,
`defaultGap` 100 ms), 40 concurrent callers mixed across all four endpoint
kinds. Worst sliding one-second window the server saw:

| Class | Requests | Worst 1 s window | Published limit |
| --- | --- | --- | --- |
| `/cards/search` | 10 | 3 | 2/s |
| `/cards/named` | 10 | 3 | 2/s |
| default (`/cards/{set}/{num}`, autocomplete) | 20 | 11 | 10/s |
| **all of `api.scryfall.com`** | **40** | **16** | 10/s (global reading) |

The per-class overshoots (3 against 2, 11 against 10) are a half-open-window
artifact: an N-request run at gap *g* spans exactly `(N-1)×g`, so a `< 1s`
window catches one extra. The **averages are exactly at the limit**. That is
compliant with no headroom, which is the real observation.

The 16 is not an artifact. See §3.

### The scan path throttles itself to near-zero

Second probe: 20 back-to-back scan-path lookups, each under the real 250 ms
escalation budget (`autoscan.go:567`), against the real 500 ms gap.

> 20 scan lookups over 4.771 s: **1 actually reached the server (0.21 req/s)**

The mechanism is worth understanding, because it is load-bearing in two
directions. `pacer.wait` reserves its slot *under the lock* and sleeps *outside*
it, so a caller that gives up mid-wait has already advanced `next[class]`:

- call 1 — no wait, sends, reservation → `now+500`
- call 2 — owes 500 ms, deadline fires at 250 ms, **nothing sent**, reservation → `now+1000`
- call 3 — owes 750 ms, deadline fires at 250 ms, nothing sent, reservation → `now+1500`

The reservation runs away from wall-clock and every subsequent call dies in the
queue. **Safe for Scryfall by construction.**

The cost is a product behaviour nobody chose: the network fallthrough that keeps
a *newly-released* card scannable is effectively **dead after the first card of
any burst**. It works when you scan one card and think; it never fires during a
box. That is not a rate-limit defect, but it should be recorded as the reason
the fallback appears not to work.

## 3. Finding: no global budget for `api.scryfall.com`

`pacer.wait` keys its map on the endpoint class (`scryfall.go:204`, with
`endpointClass` at `:179`). `/cards/search`, `/cards/named`,
`/cards/collection` and `""` (everything else) each hold an **independent**
budget. Nothing bounds their sum.

Whether that is a violation depends on which reading of Scryfall's guidance
governs, and the two readings genuinely disagree:

- **Per-bucket reading** — the published table is a list of per-endpoint limits
  and "All other methods — 10/second" is simply the catch-all bucket. Under
  this reading hoard is compliant as written.
- **Global reading** — their prose asks you to "insert 50–100 milliseconds of
  delay between the requests you send to the server at `api.scryfall.com`",
  which is a statement about the server, with the per-endpoint limits as
  *additional* constraints. Under this reading 2+2+2+10 = **16 req/s**, 60%
  over.

Scryfall does not resolve the ambiguity, so the conservative reading is the one
to build to. The exposure is theoretical today — reaching 16 needs a sustained
default-class flood, and the two default-class endpoints are both one-shot
(`hoard add <url>`; autocomplete fires once when a submitted name returns zero
prints, **not** per keystroke — `model.go:1147`). But the ceiling should exist
regardless of whether anything currently approaches it.

## 4. Smaller findings

| # | Finding | Location | Severity |
| --- | --- | --- | --- |
| 4.1 | `defaultGap` is exactly 100 ms — the outer edge of the requested 50–100 ms, zero headroom for jitter | `scryfall.go:169` | low |
| 4.2 | `fetchListing` (`GET /bulk-data`) and the bundle download bypass `apiDo` and the pacer entirely | `catalog/build.go:25`, `:142`, `:352` | low |
| 4.3 | Three comments cite `docs/data-licensing.md`; the file moved to `docs/specs/` and no citation followed it | `scryfall.go:156`, `version.go:17`, `buildinfo.go:64` | trivial |

On **4.2**: volume is genuinely tiny — the freshness check is guarded by a 6 h
timestamp persisted in the catalog DB and the listing is memoised per process.
The residual risk is that a catalog whose `setMeta` fails never persists the
guard, giving one listing call per invocation. Still one request per process, so
this is hygiene rather than exposure.

## 5. Why the box scan was innocent

Corroborating state at the time of the audit:

- Local catalog present and healthy — **107,331 cards**, `source_updated`
  2026-08-07, i.e. two days stale. Scans resolve locally.
- `layeredSearcher` only escalates on an *empty* local result
  (`command/searcher.go:37–74`). At 107k printings, `SearchPrints` essentially
  never misses.
- `NamedFuzzy` escalation is additionally gated on line 0, `titleLikely`, and
  the 250 ms budget — measured above at 0.21 req/s.
- Collection is 1,670 cards, so a full `update-prices` is 23 chunks ≈ 12 s at
  2/s.

## 6. Proposed fix

Not yet applied. Contained to two files:

1. **Global slot in `pacer.wait`** — reserve in both the class budget and a new
   `api.scryfall.com` budget, then sleep until the later of the two. Preserves
   the existing per-class guarantees and adds the ceiling.
2. **`defaultGap` 100 ms → 120 ms** — buys headroom against jitter at a cost of
   ~2 s on a full collection refresh.
3. **Route `fetchListing` through the pacer** — closes 4.2.

Verification for (1) and (2) is the probe in §2: assert the worst one-second
window across *all* classes stays at or under 10, not merely each class against
its own limit. That assertion is what no existing test makes, which is why the
gap survived.

## 7. What I could not verify

- Whether Scryfall intends the per-endpoint limits as additive or as the whole
  policy. §3 builds to the stricter reading rather than resolving it.
- The 503 itself. It was not reproduced, and no request log from that session
  survives. The reasoning in §5 is circumstantial — strong, but it is inference
  from what the code can emit, not a recording of what it did emit.
- Behaviour under a genuinely stale or absent catalog. Every measurement here
  was taken against the healthy 107k-row catalog; a first-run user with no
  catalog escalates far more, and that path is untested for rate.

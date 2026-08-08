# Data sources: licensing, terms, and good-faith review

A pre-open-source audit of every third-party service hoard talks to, and every
third-party byte the repo ships. Terms were read from the primary sources on
the date below, not from recollection — the relevant pages change, and several
had changed from what we assumed in code comments.

**Verified: 2026-08-05.** Re-verify before any release that is more than a few
months after this date; the Scryfall rate-limit page in particular is versioned
by nothing and changes silently.

---

## Bottom line

Nothing here blocks an open-source release, and nothing we are doing is in bad
faith. There are **two genuine rate-limit violations** against Scryfall's
current published limits, and **one missing attribution notice** that Wizards
of the Coast specifies verbatim and we do not carry anywhere. Both are cheap
to fix. Everything else is either already compliant or a risk to note rather
than a defect to repair.

The one thing worth a decision rather than a patch is the 60 MB of card
photographs committed under `scan/fixtures/` (§6).

---

## 1. What hoard actually talks to

| Host | Used for | Auth | Where |
| --- | --- | --- | --- |
| `api.scryfall.com` | card lookup, search, autocomplete, prices, bulk-data index | none | `internal/scryfall/`, `internal/catalog/build.go` |
| `*.scryfall.io` | card images (detail overlay), bulk-data bundles | none | `cardimage.go`, `internal/catalog/build.go` |
| `mtgjson.com/api/v5` | price fallback + 90-day price history | none | `internal/mtgjson/` |
| `tcgcsv.com` | treated-foil / etched prices MTGJSON lacks | none | `internal/tcgcsv/` |
| `archidekt.com/api` | deck import by URL | none | `internal/decksource/archidekt.go` |

Not contacted: TCGplayer directly (no API access — see §4), Moxfield (their API
is Cloudflare-blocked; hoard imports Moxfield *files* instead), Card Kingdom and
Manapool (reached only as deep links the user clicks, and as price *providers*
inside the MTGJSON feed).

Every outbound request sets `User-Agent: hoard/0.1` from
`internal/buildinfo/buildinfo.go`.

---

## 2. Scryfall

Primary sources, all fetched 2026-08-05:
[API overview](https://scryfall.com/docs/api) ·
[Rate limits](https://scryfall.com/docs/api/rate-limits) ·
[Card objects](https://scryfall.com/docs/api/cards) ·
[Terms of Service](https://scryfall.com/docs/terms)

### 2.1 The usage rules, verbatim

From the API overview, under *Use of Scryfall Data and Images*:

> As part of the Wizards of the Coast Fan Content Policy, Scryfall provides our
> card data and image database free of charge for the primary purpose of
> creating additional Magic software, performing research, or creating
> community content [...]
>
> - You may not use Scryfall logos or use the Scryfall name in a way that
>   implies Scryfall has endorsed you, your work, or your product.
> - You may not "paywall" access to Scryfall data. [...]
> - You may not use Scryfall data to create new games, or to imply the
>   information and images are from any other game besides *Magic: The
>   Gathering*.
> - **You may not simply repackage, republish, or proxy Scryfall data. Your
>   software must create additional value for end-users.**

On images:

> - Do not cover, crop, or clip off the copyright or artist name on card images.
> - Do not distort, skew, or stretch card images.
> - Do not blur, sharpen, desaturate, or color-shift card images.
> - Do not add your own watermarks, stamps, or logos to card images.

And the enforcement note:

> Repeated mishandling or misrepresentation of data or images in your project
> may result in Scryfall restricting or blocking your API access.

**Verdict: compliant.** hoard is MIT-licensed and free (no paywall), doesn't use
the Scryfall name or logo as an endorsement, is unambiguously *Magic* software,
and adds substantial value over the raw feed — a local SQLite collection, binder
and deck modelling, price history, movers, and a camera scanner. It is not a
proxy or a repackaging. Images are displayed whole and unmodified in the detail
overlay (`cardimage.go` decodes and renders; it does not crop or filter).

### 2.2 The "pricing service" question

This was the specific worry that prompted the audit, so, precisely: **the
current Scryfall documentation contains no clause prohibiting positioning
yourself as a pricing service.** I read the API overview, the rate-limit page,
the card-object page, and the Terms of Service looking for one.

What exists instead is the repackaging clause quoted above — *"You may not
simply repackage, republish, or proxy Scryfall data. Your software must create
additional value for end-users"* — which is the rule the concern really lands
on, and which hoard satisfies comfortably.

There is also a disclaimer Scryfall attaches to price data:

> Card prices and promotional offers represent daily estimates and/or market
> values provided by our affiliates. Absolutely no guarantee is made for any
> price information. See stores for final prices and details.

hoard presents prices as portfolio valuations. Carrying an equivalent
"estimates, not quotes" line is honest and costs one sentence — see §7.

Worth noting in our favour: hoard reaches for MTGJSON precisely *because*
Scryfall's USD prices come from TCGplayer alone and leave whole printings
unpriced (`internal/mtgjson/mtgjson.go`). Filling that gap is the opposite of
repackaging.

### 2.3 Rate limits — **two violations**

The limits are now **per-endpoint**, and stricter than the global figure our
code comments cite:

> - `/cards/search` — 2/second (500ms)
> - `/cards/named` — 2/second (500ms)
> - `/cards/random` — 2/second (500ms)
> - `/cards/collection` — 2/second (500ms)
> - `/cards/manifest` — 10/minute (10,000ms)
> - All other methods — 10/second (100ms)
>
> The direct file origins located at `*.scryfall.io` do not have rate limits.

> It is not acceptable to ignore HTTP 429 responses. You must act to reduce
> your application's overages.

Against that:

| Call site | Endpoint | Limit | hoard's gap | Status |
| --- | --- | --- | --- | --- |
| `internal/scryfall/scryfall.go:249` | `POST /cards/collection` | 500 ms | **150 ms** | ❌ 3.3× over |
| `internal/scryfall/scryfall.go:497` | `GET /cards/search` | 500 ms | **100 ms** | ❌ 5× over |
| `internal/scryfall/scryfall.go:451` | `GET /cards/named?fuzzy=` | 500 ms | unpaced | ⚠️ single-shot, but unbounded if looped |
| `internal/scryfall/scryfall.go:430` | `GET /cards/autocomplete` | 100 ms | unpaced | ⚠️ keystroke-driven |
| `internal/scryfall/scryfall.go:177` | `GET /cards/{set}/{num}` | 100 ms | n/a | ✅ |
| `internal/catalog/build.go:24` | `GET /bulk-data` | 100 ms | 6 h cache | ✅ |

The `chunkPause` comment is explicit about the assumption it was written under,
and that assumption is now stale:

```go
// chunkPause is the gap between collection requests. Scryfall asks for fewer
// than 10 requests a second and suggests 50–100ms; 150 leaves margin, and on a
// 1,500-card catalog it costs about three seconds across the whole run.
const chunkPause = 150 * time.Millisecond
```

`/cards/collection` is no longer a 10/second endpoint. On a 1,256-card refresh
that is 17 batches — small in absolute terms, which is likely why we have never
been throttled, but it is over the published limit on every run.

Fix: raise both to ≥500 ms. Cost on that same refresh is about six seconds, and
it only applies to cards the local catalog could not answer.

The retry path is already correct — `fetchCollectionChunkRetrying` honours
`Retry-After` on 429 with bounded retries (`scryfall.go:275–316`), which is
exactly the "act to reduce your overages" obligation.

### 2.4 Caching and bulk data — compliant, and worth keeping

> We encourage you to cache the data you download from Scryfall or process it
> locally in your own system, at least for 24 hours. [...] If you need to
> rapidly look up card names, prices, or resolve a large number of card images,
> you must use the bulk data files.

Note that "must". hoard does the right thing here already:

- `internal/catalog/` builds a local catalog from the `default_cards` bulk
  bundle and checks freshness at most every 6 hours.
- `UpdatePrices` prefers the catalog and reports the split — *"%d from the local
  catalog, %d from Scryfall"* (`pricing.go:53–55`).
- Card name search is layered local-first (`searcher.go:37–52`), so the API is
  only reached when the catalog has no answer.
- Images are cached immutably in the user's cache dir (`cardimage.go`).

This is the single strongest good-faith signal in the codebase and it is worth
saying so explicitly in the README.

### 2.5 User-Agent — adequate

> Your User-Agent header must be accurate to your usage context. If you are
> running a script or app, the header should be the name of your application,
> such as `MTGExampleApp/1.0` [...]. Do not allow HTTP libraries to choose the
> header for you.

`hoard/0.1` matches their example's shape exactly. Compliant. Adding a contact
URL (`hoard/0.1 (+https://github.com/spiffcs/hoard)`) is not required but makes
us reachable before we get blocked rather than after.

An `Accept` header is also required, and both clients set one —
`scryfall.go:145` sends `application/json`, `cardimage.go:56` sends an image
preference list. ✅

---

## 3. MTGJSON

Primary source: [mtgjson.com/license](https://mtgjson.com/license/) (fetched
2026-08-05), corroborated by the [GitHub repo](https://github.com/mtgjson/mtgjson)
(GitHub API reports `spdx_id: MIT`).

The site's license page is the MIT License in full:

> Copyright © 2018 – Present, Zach Halpern
>
> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies [...] subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in
> all copies or substantial portions of the Software.

**Verdict: the most permissive source we use.** MIT explicitly permits
redistribution, including commercial. The only obligation is retaining the
copyright notice with any substantial portion redistributed.

hoard downloads MTGJSON files at runtime into the user's cache and ships none of
them, so the notice requirement is arguably not triggered — but it is one line
in a credits section and removes the question entirely.

Two notes:

- **No published rate limit.** The files are large static downloads; hoard
  fetches them at most daily and caches (`internal/mtgjson/cache*`). Fine.
- **Price provenance is downstream.** MTGJSON aggregates vendor prices
  (TCGplayer, Card Kingdom, Manapool, Cardmarket). MTGJSON's MIT grant covers
  MTGJSON's compilation. It is not a warranty that every upstream vendor has
  blessed redistribution of their quotes. This is MTGJSON's exposure far more
  than ours, but it is the reason the "estimates, not quotes" disclaimer in §7
  is worth carrying.

---

## 4. TCGplayer, via tcgcsv.com

### 4.1 tcgcsv — compliant, and explicitly invited

Primary source: [tcgcsv.com](https://tcgcsv.com/) and
[tcgcsv.com/faq](https://tcgcsv.com/faq) (fetched 2026-08-05).

The site describes itself as *"a public entrypoint for information from
TCGplayer's API including categories, groups, products, and prices."* Its FAQ
answers "Can I scrape this website?" with *"go ahead!"* and a sample that sets

> `'User-Agent': 'YourApplication/X.Y.Z'`

and paces with `time.sleep(0.25)`.

hoard matches that guidance exactly — a descriptive User-Agent and a 250 ms
`requestGap` pacer (`internal/tcgcsv/tcgcsv.go:66–80`), plus day-stamped caching
so a fully cached day costs zero requests. The package comment already reasons
about not bursting volunteer infrastructure. **Nothing to fix.**

### 4.2 The upstream risk worth recording

TCGplayer's own API is not an option: per TCGplayer's developer materials,
**new API access is not currently being granted**, and access historically
required an application that TCGplayer approved at its sole discretion.

That means our TCGplayer-derived prices reach us through a volunteer
republisher whose own right to redistribute that catalog is not something we can
verify. This is not a defect in hoard and not something we can resolve — but it
is the least stable dependency we have, on both legal and continuity grounds.

The mitigation is already designed in, and should stay that way:

> Failures here must stay soft at the call site: this is an overlay over the
> primary feed, and a missing overlay means a dash, not a broken price update.
> — `internal/tcgcsv/tcgcsv.go`

If tcgcsv disappears, hoard degrades to a dash on treated foils. That is the
correct posture. Do not let this data become load-bearing.

**Caveat on verification:** TCGplayer's
[API Terms & Conditions](https://help.tcgplayer.com/hc/en-us/articles/360061115874-TCGplayer-API-Terms-Conditions)
is behind Cloudflare and returned 403 to every automated fetch I attempted. The
characterisation above comes from search-result summaries of that page, **not
from reading it directly.** Since hoard holds no TCGplayer API credentials and
contacts no TCGplayer host, those terms do not bind us — but if we ever consider
applying for real API access, read that page manually first.

---

## 5. Archidekt

`internal/decksource/archidekt.go:53` calls
`https://archidekt.com/api/decks/{id}/` — an undocumented public endpoint, no
key, one request per user-initiated deck import.

I found no published API terms of service for Archidekt. The usage pattern is
benign (user-initiated, single request, descriptive User-Agent, no bulk
harvesting), but this is **unverified rather than verified-clean**, and it is the
one source where I cannot point at a document.

`internal/decksource/testdata/archidekt_7319967.json` is a captured API response
committed to the repo as a test fixture. It is one public deck list; low risk,
but it is third-party data we redistribute, so it is listed here for
completeness.

---

## 6. What the repository itself ships

This matters as much as what we call at runtime, because open-sourcing publishes
it all.

| Path | Content | Tracked? | Assessment |
| --- | --- | --- | --- |
| `scan/corpus/images/` | third-party card images, fetched | **no** — gitignored | ✅ correctly excluded |
| `scan/fixtures/*.png` | 29 × 1920×1080 capture frames, ~60 MB | **yes** | ⚠️ see below |
| `scan/fixtures/*.golden.json` | card *readings* (name, set, number, year) | yes | ✅ facts, hoard's own format |
| `internal/decksource/testdata/` | one captured Archidekt response | yes | ✅ negligible |

`.gitignore` already reasons about this correctly for the corpus:

```
# The parser corpus: third-party card images, fetched not authored.
scan/corpus/images/
```

**The fixtures are the open question.** They are our own photographs — authored,
not fetched — which is a materially different position from the corpus. But each
frame contains Magic card art reproduced legibly, and publishing them
redistributes that art. Wizards' Fan Content Policy is permissive about fan
creations while drawing a hard line at *"the verbatim copying and reposting of
Wizards' IP."* Photographs of cards on a desk sit between those, closer to the
permitted side, and the surrounding practice in the MTG tooling community is to
publish such fixtures without incident.

Three options, in the order I'd weigh them:

1. **Keep them, add the Fan Content notice** (§7). Lowest friction, keeps
   `make scan-check` working for contributors out of the box, and matches what
   comparable projects do. My recommendation.
2. **Move them out of the repo** to a release asset or separate download, with
   `sweep.sh` fetching on demand. Removes the question entirely and drops
   ~60 MB from every clone — but breaks the zero-setup test story.
3. **Downscale or crop** to the regions each fixture actually pins. Reduces the
   reproduction but risks invalidating the goldens, which were re-baselined only
   on 2026-08-05 — expensive for the benefit.

This is a judgment call about acceptable risk, not a compliance question with a
correct answer. It should be yours.

---

## 7. Wizards of the Coast Fan Content Policy — the umbrella

Primary source:
[company.wizards.com/en/legal/fancontentpolicy](https://company.wizards.com/en/legal/fancontentpolicy)
(fetched 2026-08-05).

Every source above ultimately derives from Wizards' IP, and Scryfall explicitly
grants its data *"As part of the Wizards of the Coast Fan Content Policy."* So
the policy binds hoard directly. The obligations that apply to us:

- **The notice is specified verbatim and is mandatory.** We carry it nowhere —
  not in `README.md`, not in `LICENSE`, not in the TUI. This is the clearest
  single gap in the audit.
- **Fan Content must be free**, may not be sold or licensed for compensation.
  hoard is MIT and free. ✅
- **Don't use Wizards' logos and trademarks** without written permission.
  hoard uses the words "Magic: The Gathering" descriptively to say what the tool
  manages, and ships no Wizards logo or mana symbol art. Nominative use of this
  kind is universal across MTG tooling; the disclaimer is what makes the
  unofficial status unambiguous. ✅ with the notice in place.
- **Don't remove existing legal notices** from Wizards IP you incorporate —
  which aligns exactly with Scryfall's "do not crop the copyright or artist
  name" rule, and is another reason not to crop the scan fixtures (§6, option 3).

The required text, to be pasted as-is:

> hoard is unofficial Fan Content permitted under the Fan Content Policy. Not
> approved/endorsed by Wizards. Portions of the materials used are property of
> Wizards of the Coast. ©Wizards of the Coast LLC.

---

## 8. Actions before release

**P0 — do these first**

1. ✅ **DONE 2026-08-07** (superseded by a stronger fix): per-call-site pauses were
   replaced by a shared per-endpoint pacer in `apiDo`
   (`internal/scryfall/scryfall.go`, `apiPacer`) — 500 ms for
   `/cards/search|named|random|collection`, 100 ms otherwise, enforced across
   goroutines and every call site. `chunkPause` and the pagination sleep were
   deleted as redundant.
2. ✅ **DONE 2026-08-07** — covered by the same pacer.
3. ✅ **DONE 2026-08-07** — §7 text now in `README.md`, `LICENSE` (with a
   code-vs-card-imagery scope split), and `hoard version` / `--version`
   (`internal/buildinfo.FanContentNotice`). The User-Agent also carries a
   contact URL now (P1.7).

**P1 — cheap, clearly right**

4. **Add a credits/attribution section** to the README naming Scryfall, MTGJSON
   (with Zach Halpern's MIT copyright line), and tcgcsv, and stating that hoard
   is not affiliated with any of them.
5. **Add the price disclaimer** — that prices are daily estimates from
   third-party aggregators, carry no guarantee, and are not quotes. One line in
   the README and in `docs/pricing.md`.
6. **Debounce or locally-gate remote autocomplete** —
   `internal/scryfall/scryfall.go:430`. It is layered behind the local catalog
   (`searcher.go:37`) so it rarely fires, but a keystroke-driven path against a
   10/second endpoint has no pacing of its own today.
7. **Extend the User-Agent** to `hoard/0.1 (+https://github.com/spiffcs/hoard)`
   so Scryfall can reach us before blocking us.

**P2 — record the decision, no code change required**

8. **Decide on `scan/fixtures/*.png`** (§6). My recommendation is keep + notice.
9. **Note the tcgcsv dependency risk** in `docs/pricing.md`: volunteer mirror,
   soft-fail by design, must not become load-bearing.

---

## 9. What I could not verify

Stated plainly so nobody treats this document as more settled than it is:

- **TCGplayer's API Terms & Conditions** — Cloudflare-blocked (403) to both the
  fetch tool and curl with a browser User-Agent. Characterised from search
  summaries only. Does not currently bind hoard (no credentials, no contact with
  TCGplayer hosts); read it manually before ever applying for access.
- **Archidekt** — no published API terms located (§5).
- **Upstream vendor consent for prices redistributed through MTGJSON** — not
  something we can establish from outside (§3).
- **Whether Scryfall considers our current call pattern acceptable in practice.**
  We have not been throttled, but §2.3 shows we are over the published limits,
  so absence of a 429 is not evidence of compliance.

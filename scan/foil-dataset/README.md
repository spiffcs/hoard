# The foil-marker corpus

Labelled card images for building a detector that can tell a foil from a
nonfoil when the printed text cannot.

## Why this exists

The scanner reads finish from one signal: the star that modern frames print as
the set/language separator (`MSC ★ EN`), classified by `finishFromSeparator`
in `ScanKit`. Pre-8th-Edition frames have no set/language line at all, so
old foils carry no finish evidence and commit as **nonfoil** — silently, and
foil is worth a multiple of nonfoil.

Those frames do carry a marker: a four-pointed starburst with a comet trail at
the lower left of the text box. It is printed ink at a fixed spot, so unlike
the diffraction sheen it does not move with the light. Finding it is a vision
problem, and vision problems need labelled data before they need code.

## What was already ruled out

Recorded so nobody re-runs these:

- **OCR cannot see it.** Cropped so the star dominated the frame, Vision's
  recognizer still returned only the text lines around it. It is a decorative
  graphic straddling a border, not a glyph, and never gets proposed as one.
- **Colour statistics cannot find it.** Measured over the star's patch, a red
  *nonfoil* scored higher than a known foil (chroma 58.1 vs 56.4), and a
  star-free control patch on the same foil scored the same as the star patch.
  The card's own art dominates any regional colour measure.
- **Hand-written structure detectors did not separate.** Oriented gradient
  energy, spoke counting, and a polarity-agnostic radial-consistency score all
  put the known foil mid-pack among unlabelled negatives; a modern snow land
  that *cannot* carry the marker out-scored every foil capture, because the
  detector was locking onto its set symbol.

Two findings from that work constrain anything built next:

- **The marker's polarity flips.** The same physical card photographed twice
  read as bright ink on a dark frame once and as a dark star on a bright wash
  the other time. Any detector assuming "bright spokes" is wrong half the time.
- **Resolution was the likely binding constraint.** Those captures were
  1920x1080, leaving the marker about 60px across and focus-softened. The
  helper now opts into the format's largest still; the capability line reports
  `still=WxH`. Samples collected before that are worth far less — check the
  line before trusting a negative result.

## Layout

    images/        the pixels. Gitignored: personal card photos and
                   third-party image-search results.
    labels.tsv     one row per image, tracked. The corpus is reproducible
                   from it.

## labels.tsv

Tab-separated, one header line. Columns:

| column | meaning |
| --- | --- |
| `id` | filename stem under `images/`, no extension |
| `source` | `session` (our camera) or `web` (image search) |
| `provenance` | session date, or the page/URL the image came from |
| `name` | card name |
| `set` | set code, lowercase, `-` if unknown |
| `number` | collector number, `-` if unknown |
| `finish` | `foil`, `nonfoil`, or `unknown` — **never guess** |
| `marker` | `old-star`, `modern-separator`, `none`, or `unknown` |
| `frame` | `old`, `modern`, or `retro` (a modern printing wearing the old frame) |
| `notes` | lighting, angle, wear — anything that explains an outlier |

`finish` and `marker` are separate on purpose. A foil can be present with the
marker unreadable, and the retro-frame reprints are the interesting case: a
modern printing that carries the old-style star. If those turn out to mark
foils the same way, the detector generalizes past the old frame and the
frame-era gate would have been a mistake.

## Collecting

From a scanning session:

    HOARD_SCAN_LOG=/tmp/scan-telemetry.log HOARD_SCAN_AUTO=1 HOARD_SCAN_MULTI=1 \
      HOARD_SCAN_DEBUG_DIR=/tmp/scan-fixtures ./hoard --db /tmp/scan-live.db
    ./scan/foil-dataset/collect.sh /tmp/scan-fixtures session 2026-08-03

That writes a normalized card crop and a cropped marker patch per capture, and
appends draft rows with `finish=unknown`. **Fill the labels in by eye** — the
scanner's own `finishHint` is evidence for modern frames only, and it is
exactly the signal we are trying to replace on old ones.

From image search, save the file into `images/` and add its row by hand,
with the URL in `provenance`.

## Using web images carefully

They are worth having, and they are not interchangeable with our captures.
A vendor scan is sharp, square, evenly lit and often idealized; our captures
are soft, angled, and washed with whatever the desk lamp does. A threshold
tuned on the first will not hold on the second.

So: web images are for the **shape prior** — what the marker looks like, where
it sits, which sets and treatments carry it. Any threshold, and any
false-positive rate worth quoting, has to be measured on `source=session`
rows. Keep the two apart when evaluating, and never report a score over the
pooled set.

Do not commit the images. Third-party pixels have owners, and the card photos
are of someone's desk.

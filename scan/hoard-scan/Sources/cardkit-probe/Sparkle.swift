// The foil-sparkle corpus harness: fit the template, and score the corpus with it.
//
// Both modes run against scan/foil-corpus/cards, which holds the marker's
// neighbourhood already flattened into card space — u 0.00-0.45, v 0.72-1.00 of
// a 630x880 card. Working from those rather than from the original stills is
// deliberate: the question here is whether the *detector* separates foil from
// nonfoil, and rerunning card location and perspective correction on every fit
// would fold their failures into that answer. `--sparkle` is the end-to-end
// mode when the whole chain is what needs checking.
//
// The scoring path is BorderKit's own `sparkleScan`, driven through the same
// `CardSampler` the live read uses. Only the *alignment* search is implemented
// here, because brute-forcing every offset is a corpus-time luxury that has no
// business in a shipping read.

import BorderKit
import CardKit
import CoreGraphics
import Foundation
import ImageIO

/// Where scan/foil-corpus/cards sits in card space. Fixed by how the crops were
/// cut; changing it means recutting them.
enum CorpusCrop {
    static let u0: CGFloat = 0
    static let u1: CGFloat = 284.0 / 630.0
    static let v0: CGFloat = 634.0 / 880.0
    static let v1: CGFloat = 1.0
}

/// An RGBA copy of a corpus crop, sampled in card space.
///
/// RGBA rather than the grayscale it used to be, because the reader now judges
/// two channels and the corpus is where the second one's threshold is argued
/// from. A grayscale harness could not score the channel it exists to validate.
struct CorpusCard {
    let width: Int, height: Int
    private let data: [UInt8]

    init?(_ url: URL) {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { return nil }
        let w = cg.width, h = cg.height
        guard w > 0, h > 0 else { return nil }
        var buf = [UInt8](repeating: 0, count: w * h * 4)
        let ok = buf.withUnsafeMutableBytes { raw -> Bool in
            guard let ctx = CGContext(
                data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8,
                bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        guard ok else { return nil }
        width = w; height = h; data = buf
    }

    /// Pixel at a card-space position, or nil outside the crop. Nearest
    /// neighbour, matching how the reader samples the live frame.
    private func rgb(_ u: CGFloat, _ v: CGFloat) -> (CGFloat, CGFloat, CGFloat)? {
        let x = Int((u - CorpusCrop.u0) / (CorpusCrop.u1 - CorpusCrop.u0) * CGFloat(width))
        let y = Int((v - CorpusCrop.v0) / (CorpusCrop.v1 - CorpusCrop.v0) * CGFloat(height))
        guard x >= 0, x < width, y >= 0, y < height else { return nil }
        let o = (y * width + x) * 4
        return (CGFloat(data[o]) / 255, CGFloat(data[o + 1]) / 255, CGFloat(data[o + 2]) / 255)
    }

    /// The luma sampler, matching `PixelReader.luma`'s weights exactly — a
    /// harness that weighted its channels differently would fit the template to
    /// a slightly different image than the one the reader sees.
    var sampler: CardSampler {
        { u, v in
            guard let c = rgb(u, v) else { return nil }
            return 0.2126 * c.0 + 0.7152 * c.1 + 0.0722 * c.2
        }
    }

    /// The warm-cool sampler, matching `PixelReader.warmCool`.
    var chromaSampler: CardSampler {
        { u, v in
            guard let c = rgb(u, v) else { return nil }
            return c.2 - c.0
        }
    }
}

struct CorpusRow {
    let id, session, frame, era, finish, physical, name: String
}

func readLabels(_ path: String) -> [CorpusRow] {
    guard let text = try? String(contentsOfFile: path, encoding: .utf8) else {
        die("no labels at \(path)")
    }
    return text.split(whereSeparator: \.isNewline).dropFirst().compactMap { line in
        // Trimmed per field: this file is hand-edited, and a stray carriage
        // return turns every `finish` into "foil\r", which matches nothing and
        // silently empties the corpus.
        let f = line.split(separator: "\t", omittingEmptySubsequences: false)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
        guard f.count >= 7 else { return nil }
        return CorpusRow(id: f[0], session: f[1], frame: f[2], era: f[3],
                         finish: f[4], physical: f[5], name: f[6])
    }
}

/// alignedPatch brute-forces the best offset for one card against a template,
/// and returns the full-resolution patch sampled there.
///
/// Corpus-time only. The shipping reader uses `sparkleScan`'s coarse-to-fine
/// search, which reaches the same verdicts for 2% of the work — but a template
/// is fitted once, offline, and there is no reason for it to inherit that
/// approximation.
func alignedPatch(_ card: CorpusCard, _ template: [CGFloat]) -> [CGFloat]? {
    let sample = card.sampler
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestI = 0, bestJ = 0
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, template)
            if s > best { best = s; bestI = i; bestJ = j }
        }
    }
    guard best > -CGFloat.infinity else { return nil }
    return sparklePatch(du: CGFloat(bestI) * cellU, dv: CGFloat(bestJ) * cellV, step: 1, sample)
}

/// sparkleFit builds the template and writes it out as Swift.
///
/// Four passes. The first averages every foil at the nominal centre, which is
/// blurry because the captures are not aligned with each other; each pass after
/// it re-locates every card against the running average and rebuilds. Three
/// re-alignments is where it stopped moving.
///
/// `--only-session` exists because a template fitted on the cards it is then
/// scored against reports a number that will not survive a third session. The
/// honest measurement is fit on one, score on the other.
func sparkleFit(dir: String, out: String, onlySession: String?, chroma: Bool = false) -> Never {
    let labels = readLabels("\(dir)/labels.tsv")
    let foils = labels.filter {
        $0.frame == "retro" && $0.finish == "foil"
            && (onlySession == nil || $0.session == onlySession!)
    }
    guard !foils.isEmpty else { die("no foil rows in \(dir)/labels.tsv") }
    let cards = foils.compactMap { row -> CorpusCard? in
        CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png"))
    }
    guard cards.count == foils.count else {
        die("read \(cards.count) of \(foils.count) foil crops from \(dir)/cards")
    }

    let cells = SparkleTemplate.cols * SparkleTemplate.rows
    var template = [CGFloat](repeating: 0, count: cells)

    // Pass 0: unaligned mean, which is only a starting point for the search.
    var acc = [CGFloat](repeating: 0, count: cells)
    var used = 0
    for card in cards {
        let sample = chroma ? card.chromaSampler : card.sampler
        guard let p = sparklePatch(du: 0, dv: 0, step: 1, sample),
              let n = sparkleNormalise(p) else { continue }
        for i in 0..<cells { acc[i] += n[i] }
        used += 1
    }
    guard used > 0, let seeded = sparkleNormalise(acc.map { $0 / CGFloat(used) }) else {
        die("could not seed a template")
    }
    template = seeded

    for pass in 1...3 {
        acc = [CGFloat](repeating: 0, count: cells)
        used = 0
        for card in cards {
            let p0 = chroma ? alignedChromaPatch(card, template) : alignedPatch(card, template)
            guard let p = p0, let n = sparkleNormalise(p) else { continue }
            for i in 0..<cells { acc[i] += n[i] }
            used += 1
        }
        guard used > 0, let next = sparkleNormalise(acc.map { $0 / CGFloat(used) }) else {
            die("pass \(pass) produced no template")
        }
        // How far it moved. Once this is ~1.0 the alignment has settled and
        // more passes are just arithmetic.
        FileHandle.standardError.write(Data(
            "pass \(pass): agreement with previous \(String(format: "%.4f", Double(sparkleCorrelate(next, template))))\n".utf8))
        template = next
    }

    var swift = """
    // The foil sparkle's \(chroma ? "warm-cool" : "luma") template. GENERATED — do not hand-edit.
    //
    //     make cardkit
    //     ./bin/cardkit-probe --sparkle-fit scan/foil-corpus \\
    //         scan/hoard-scan/Sources/BorderKit/Sparkle\(chroma ? "Chroma" : "")TemplateData.swift\(chroma ? " --chroma" : "")
    //
    // \(SparkleTemplate.cols)x\(SparkleTemplate.rows) cells spanning
    // SparkleTemplate.spanU x spanV of the card, zero-mean and unit-norm, so
    // correlating a normalised patch against it is a plain dot product. See
    // Sparkle.swift for what it is and why it is shaped this way.

    import CoreGraphics

    /// How many foil captures were averaged into this. Recorded because the
    /// threshold beside it is only as good as the corpus behind both.
    public let \(chroma ? "sparkleChromaTemplateFittedFrom" : "sparkleTemplateFittedFrom") = \(used)

    public let \(chroma ? "sparkleChromaTemplate" : "sparkleTemplate"): [CGFloat] = [\n
    """
    for j in 0..<SparkleTemplate.rows {
        let row = (0..<SparkleTemplate.cols)
            .map { String(format: "%.6f", Double(template[j * SparkleTemplate.cols + $0])) }
            .joined(separator: ", ")
        swift += "    \(row),\n"
    }
    swift += "]\n"

    do { try swift.write(toFile: out, atomically: true, encoding: .utf8) }
    catch { die("could not write \(out): \(error)") }
    print("fitted from \(used) foils\(onlySession.map { " (session \($0) only)" } ?? "") -> \(out)")
    exit(0)
}

/// sparkleScoreCorpus runs the shipping reader over every labelled card and
/// prints the table the threshold is argued from.
func sparkleScoreCorpus(dir: String, verbose: Bool, onlySession: String?) -> Never {
    let labels = readLabels("\(dir)/labels.tsv")
        .filter { onlySession == nil || $0.session == onlySession! }
    var byClass: [String: [(CGFloat, CorpusRow)]] = [:]
    var maxSamples = 0

    var chromaByClass: [String: [(CGFloat, CorpusRow)]] = [:]
    for row in labels {
        guard let card = CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png")),
              let r = sparkleScan(card.sampler) else {
            print("\(row.id)\tNO READING")
            continue
        }
        let c = sparkleScan(card.chromaSampler, template: sparkleChromaTemplate)
        maxSamples = max(maxSamples, r.samples)
        let key = row.frame == "modern" ? "modern" : "retro \(row.finish)"
        byClass[key, default: []].append((r.score, row))
        chromaByClass[key, default: []].append((c?.score ?? 0, row))
        if verbose {
            print(String(format: "%-7@ %-14@ %-8@ %7.3f  du %+.4f dv %+.4f  mad %.3f  chroma %7.3f  cmad %.3f",
                         row.id as NSString, row.name.prefix(14) as NSString,
                         row.finish as NSString, Double(r.score),
                         Double(r.offsetU), Double(r.offsetV), Double(r.contrast),
                         Double(c?.score ?? 0), Double(c?.contrast ?? 0)))
        }
    }

    print("\nthreshold \(SparkleGate.accept), template from \(sparkleTemplateFittedFrom) foils")
    print(String(format: "%-16@ %4@ %8@ %8@ %8@ %10@",
                 "class" as NSString, "n" as NSString, "min" as NSString,
                 "median" as NSString, "max" as NSString, "accepted" as NSString))
    for key in ["retro foil", "retro nonfoil", "modern"] {
        guard let rows = byClass[key], !rows.isEmpty else { continue }
        let v = rows.map(\.0).sorted()
        let accepted = v.filter { $0 >= SparkleGate.accept }.count
        print(String(format: "%-16@ %4d %8.3f %8.3f %8.3f %10d",
                     key as NSString, v.count, Double(v[0]),
                     Double(v[v.count / 2]), Double(v[v.count - 1]), accepted))
    }

    // The warm-cool channel, scored the same way. Printed beside luma rather
    // than merged into it: the two channels are kept because they fail on
    // different cards, and a single combined row would hide exactly that.
    print("\nwarm-cool channel, threshold \(SparkleGate.acceptChroma)")
    for key in ["retro foil", "retro nonfoil", "modern"] {
        guard let rows = chromaByClass[key], !rows.isEmpty else { continue }
        let v = rows.map(\.0).sorted()
        let accepted = v.filter { $0 >= SparkleGate.acceptChroma }.count
        print(String(format: "%-16@ %4d %8.3f %8.3f %8.3f %10d",
                     key as NSString, v.count, Double(v[0]),
                     Double(v[v.count / 2]), Double(v[v.count - 1]), accepted))
    }

    // What the pair *would* do together. Not the shipping rule: the colour
    // channel does not vote — see SparkleVerdict.isFoil, and the fixture-set
    // measurement that stopped it. Printed because this is the number that
    // would justify letting it, once a second rig's corpus agrees.
    print("\neither channel (measured only — chroma does not vote)")
    for key in ["retro foil", "retro nonfoil", "modern"] {
        guard let lum = byClass[key], let chr = chromaByClass[key], !lum.isEmpty else { continue }
        let accepted = zip(lum, chr).filter {
            $0.0 >= SparkleGate.accept || $1.0 >= SparkleGate.acceptChroma
        }.count
        print(String(format: "%-16@ %4d %30@ %10d",
                     key as NSString, lum.count, "" as NSString, accepted))
    }

    // Every card that came out on the wrong side, named. A rate without the
    // names is not diagnosable.
    for (key, rows) in byClass.sorted(by: { $0.key < $1.key }) {
        guard let chr = chromaByClass[key] else { continue }
        let paired = Array(zip(rows, chr))
        let wrong = key == "retro foil"
            ? paired.filter { $0.0.0 < SparkleGate.accept && $0.1.0 < SparkleGate.acceptChroma }
            : paired.filter { $0.0.0 >= SparkleGate.accept || $0.1.0 >= SparkleGate.acceptChroma }
        for (l, c) in wrong.sorted(by: { max($0.0.0, $0.1.0) > max($1.0.0, $1.1.0) }) {
            let what = key == "retro foil" ? "MISSED foil" : "FALSE POSITIVE"
            print(String(format: "  %@: %@ %@ (luma %.3f, chroma %.3f)", what as NSString,
                         l.1.id as NSString, l.1.name as NSString,
                         Double(l.0), Double(c.0)))
        }
    }

    print("\npixel samples per card: \(maxSamples) (ceiling \(SparkleGate.maxSamples))")
    if maxSamples > SparkleGate.maxSamples {
        print("OVER BUDGET")
        exit(3)
    }
    exit(0)
}

// MARK: - Fitting CardLayout.leftU

/// anchorFit sweeps scan/corpus and reports where each card's footer anchor sat.
///
/// This is the measurement `CardLayout.leftU`'s table is made of. It runs on the
/// raw corpus images, which *are* cards, so the anchor's box is card space with
/// nothing in between — no card location, no perspective flatten, no margin.
///
/// It reports the two candidate discriminators alongside, because the era alone
/// provably cannot separate the frames: 8th Edition shipped in July 2003 and
/// Legions and Scourge are 2003 printings of the frame it replaced, so a table
/// keyed on the year alone puts both in one bucket and is wrong for one of them.
@available(macOS 15, *)
func anchorFit(manifest: String) async -> Never {
    let dir = URL(fileURLWithPath: manifest).deletingLastPathComponent()
    guard let text = try? String(contentsOfFile: manifest, encoding: .utf8) else {
        die("no manifest at \(manifest)")
    }
    print("era\tframe\tkind\tprefix\tleftU\tsetCode\tnumberSource\tname")
    for row in text.split(whereSeparator: \.isNewline).dropFirst() {
        let f = row.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
        guard f.count >= 6 else { continue }
        let (sid, era, name) = (f[0], f[1], f[3])
        let img = dir.appendingPathComponent("images/\(sid).png")
        guard let src = CGImageSourceCreateWithURL(img as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { continue }

        guard let fit = await measureAnchorOnFlatCard(cg) else { continue }
        let m = fit.anchor
        print("\(era)\t\(fit.year)\t\(m.kind)\t\(m.prefix)"
            + "\t\(String(format: "%.4f", Double(m.leftU)))"
            + "\t\(fit.setCode.isEmpty ? "-" : fit.setCode)\t\(fit.numberSource)\t\(name)")
    }
    exit(0)
}

/// sparkleWhere answers the one question the shipping reader cannot: is a low
/// score a faint marker, or a marker outside the window?
///
/// It runs the same search twice — once at `SparkleGate`'s fitted window, once
/// over a window wide enough to contain any plausible registration error — and
/// prints both peaks. A wide peak that is much stronger and sits well outside
/// the fitted window says the search never reached the marker, which is a
/// registration problem; a wide peak in the same place at the same height says
/// the marker really is faint, which is a threshold or a capture problem.
///
/// The wide window is a *measuring instrument* and must never become the
/// shipping one: at ±0.037/±0.042 the corpus's highest nonfoil goes 0.470 to
/// 0.676 and two false positives appear. See docs/scanner-foil-registration.md.
func sparkleWhere(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else {
        die("no card located in \(path)")
    }
    // The same pixels `readCard` gives the sparkle reader: the perspective-
    // corrected card, where card space and image space are the same thing.
    let flat = located.image

    // Four times the fitted half-width in each axis, at the same cell size, so
    // one cell still means one pixel of a 630x880 card.
    let wide = SparkleWindow(
        u: SparkleGate.searchU * 4, v: SparkleGate.searchV * 4,
        cellsU: SparkleGate.searchCellsU * 4, cellsV: SparkleGate.searchCellsV * 4)

    let fitted = sparkleInCard(flat)
    let found = sparkleInCard(flat, window: wide)
    // Luma only here. This mode answers "could a wider search have reached the
    // marker", which is a question about registration; the colour channel
    // searches the same window and would not change the answer.
    let fittedScore: Double = fitted?.luma.map { Double($0.score) } ?? -9
    let fittedU: Double = fitted?.luma.map { Double($0.offsetU) } ?? 0
    let fittedV: Double = fitted?.luma.map { Double($0.offsetV) } ?? 0
    let wideScore: Double = found?.luma.map { Double($0.score) } ?? -9
    let wideU: Double = found?.luma.map { Double($0.offsetU) } ?? 0
    let wideV: Double = found?.luma.map { Double($0.offsetV) } ?? 0
    // True when the wide search found its peak outside the shipping window —
    // the marker was never reachable.
    let outside: Bool = abs(wideU) > Double(SparkleGate.searchU)
        || abs(wideV) > Double(SparkleGate.searchV)
    var out: [String: Any] = [:]
    out["file"] = (path as NSString).lastPathComponent
    out["fittedScore"] = fittedScore
    out["fittedU"] = fittedU
    out["fittedV"] = fittedV
    out["wideScore"] = wideScore
    out["wideU"] = wideU
    out["wideV"] = wideV
    out["outside"] = outside
    // The structure gate, on both patches. `sparkleScan` reports a score of
    // exactly 0 when the patch it settled on has no structure to judge, so a
    // 0.000 is an abstention and not a correlation — telling the two apart is
    // the difference between "wrong place" and "blown highlight".
    out["fittedContrast"] = fitted?.luma.map { Double($0.contrast) } ?? -9
    out["wideContrast"] = found?.luma.map { Double($0.contrast) } ?? -9
    out["fittedChroma"] = fitted?.chroma.map { Double($0.score) } ?? -9
    out["fittedChromaContrast"] = fitted?.chroma.map { Double($0.contrast) } ?? -9
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

// MARK: - Is the colour channel worth a template of its own?

/// sparkleChromaTrial fits a template on the warm-cool channel and scores it
/// held out, which is the fair test of the second channel.
///
/// It exists because the first, cheaper version of that idea failed for a reason
/// that did not rule the idea out: the *luma* template was correlated against
/// colour pixels, and a template of the wrong channel's marker is not a test of
/// whether the channel carries one. This fits on one session and scores the
/// other, the same discipline `--sparkle-fit --only-session` imposes on luma.
///
/// Reports both classes' spread, because the number that matters is separation
/// and not the foils' scores alone — the warm-cool axis is full of card
/// furniture (the text box's edge, the border) that every card has, foil or not,
/// and a template can score high on all of it while telling the two apart not at
/// all.
func sparkleChromaTrial(dir: String) -> Never {
    let labels = readLabels("\(dir)/labels.tsv").filter { $0.frame == "retro" }
    func load(_ row: CorpusRow) -> CorpusCard? {
        CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png"))
    }
    let cells = SparkleTemplate.cols * SparkleTemplate.rows

    for (fitOn, scoreOn) in [("s1", "s2"), ("s2", "s1")] {
        let fitRows = labels.filter { $0.session == fitOn && $0.finish == "foil" }
        let cards = fitRows.compactMap(load)
        guard !cards.isEmpty else { continue }

        // Same four passes the luma fit uses: an unaligned mean, then three
        // re-align-and-rebuild rounds.
        var acc = [CGFloat](repeating: 0, count: cells)
        var used = 0
        for c in cards {
            guard let p = sparklePatch(du: 0, dv: 0, step: 1, c.chromaSampler),
                  let n = sparkleNormalise(p) else { continue }
            for i in 0..<cells { acc[i] += n[i] }
            used += 1
        }
        guard used > 0, var template = sparkleNormalise(acc.map { $0 / CGFloat(used) })
        else { continue }
        for _ in 1...3 {
            acc = [CGFloat](repeating: 0, count: cells)
            used = 0
            for c in cards {
                guard let p = alignedChromaPatch(c, template),
                      let n = sparkleNormalise(p) else { continue }
                for i in 0..<cells { acc[i] += n[i] }
                used += 1
            }
            guard used > 0, let next = sparkleNormalise(acc.map { $0 / CGFloat(used) })
            else { break }
            template = next
        }

        var byClass: [String: [CGFloat]] = [:]
        var perCard: [(CorpusRow, CGFloat, CGFloat)] = []
        for row in labels where row.session == scoreOn {
            guard let c = load(row),
                  let r = sparkleScanTemplate(c.chromaSampler, template: template) else { continue }
            byClass[row.finish, default: []].append(r.score)
            let l = sparkleScan(c.sampler)?.score ?? -9
            perCard.append((row, l, r.score))
        }
        for (row, l, ch) in perCard.sorted(by: { $0.0.finish + $0.0.name < $1.0.finish + $1.0.name }) {
            print(String(format: "    %-8@ %-22@ luma %6.3f  chroma %6.3f",
                         row.finish as NSString, row.name.prefix(22) as NSString,
                         Double(l), Double(ch)))
        }
        print("\nchroma template fitted on \(fitOn) (\(cards.count) foils), scored on \(scoreOn):")
        for k in ["foil", "nonfoil"] {
            guard let v = byClass[k]?.sorted(), !v.isEmpty else { continue }
            print(String(format: "  %-8@ n=%2d  min %6.3f  median %6.3f  max %6.3f",
                         k as NSString, v.count, Double(v[0]),
                         Double(v[v.count / 2]), Double(v[v.count - 1])))
        }
        if let f = byClass["foil"]?.sorted(), let n = byClass["nonfoil"]?.sorted(),
           !f.isEmpty, !n.isEmpty {
            // The only figure that decides anything: how many foils sit above
            // every nonfoil. Zero means no threshold exists.
            let clean = f.filter { $0 > n[n.count - 1] }.count
            print("  foils above the highest nonfoil (\(String(format: "%.3f", Double(n[n.count - 1])))): \(clean) of \(f.count)")
        }
    }
    exit(0)
}

/// alignedChromaPatch is alignedPatch over the warm-cool sampler.
func alignedChromaPatch(_ card: CorpusCard, _ template: [CGFloat]) -> [CGFloat]? {
    let sample = card.chromaSampler
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestI = 0, bestJ = 0
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, template)
            if s > best { best = s; bestI = i; bestJ = j }
        }
    }
    guard best > -CGFloat.infinity else { return nil }
    return sparklePatch(du: CGFloat(bestI) * cellU, dv: CGFloat(bestJ) * cellV, step: 1, sample)
}

/// sparkleScanTemplate is the brute-force score against an arbitrary template.
/// Corpus-time only; the shipping reader searches coarse-to-fine against the
/// one template it ships with.
func sparkleScanTemplate(_ sample: @escaping CardSampler,
                         template: [CGFloat]) -> (score: CGFloat, contrast: CGFloat)? {
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestPatch: [CGFloat]? = nil
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, template)
            if s > best { best = s; bestPatch = p }
        }
    }
    guard best > -CGFloat.infinity, let p = bestPatch else { return nil }
    // Spread as mean absolute deviation about the mean: BorderKit's median
    // version is internal, and the trial only needs a rough structure figure.
    let mean = p.reduce(0, +) / CGFloat(p.count)
    let spread = p.reduce(0) { $0 + abs($1 - mean) } / CGFloat(p.count)
    return (best, spread)
}

/// sparkleControl scores the marker patch and two control patches — the same
/// machinery, the same window, anchored ±0.10 card-widths along the same row —
/// so a capture's target score can be read against what this exposure gives a
/// patch with no marker in it. The question it exists to answer, per rig: is
/// (target − control) separable where the raw score measurably is not?
@available(macOS 15, *)
func sparkleControl(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else {
        die("no card located in \(path)")
    }
    let flat = located.image

    func fields(_ v: SparkleVerdict?, into out: inout [String: Any], prefix: String) {
        out[prefix + "Score"] = v?.luma.map { Double($0.score) } ?? -9
        out[prefix + "Contrast"] = v?.luma.map { Double($0.contrast) } ?? -9
        out[prefix + "Chroma"] = v?.chroma.map { Double($0.score) } ?? -9
        out[prefix + "ChromaContrast"] = v?.chroma.map { Double($0.contrast) } ?? -9
    }

    var out: [String: Any] = ["file": (path as NSString).lastPathComponent]
    fields(sparkleInCard(flat), into: &out, prefix: "target")
    fields(sparkleInCard(flat, anchorShiftU: 0.10), into: &out, prefix: "controlR")
    fields(sparkleInCard(flat, anchorShiftU: -0.10), into: &out, prefix: "controlL")
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

/// creditAnchor measures, for one still, where the band's credit and copyright
/// rows sit against where the sparkle search finds its peak — the fitting data
/// for anchoring the marker search on the card's own text rather than on the
/// quad. Matchers are deliberately looser than CardKit's: this mode wants to
/// know whether a mangled credit row is still *locatable*, which is a different
/// question from whether it is trustworthy.
@available(macOS 15, *)
func creditAnchor(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else { die("no card located in \(path)") }
    let flat = located.image

    var out: [String: Any] = ["file": (path as NSString).lastPathComponent,
                              "aspect": Double(flat.width) / Double(flat.height)]

    if let band = cropCard(flat, CardGeometry.band) {
        let lines = await recognizeLines(band, correctLanguage: false)
        func squash(_ s: String) -> String {
            String(s.lowercased().filter { $0.isLetter })
        }
        // Card space is top-down; Vision's box is bottom-up inside the band
        // crop, which spans v 0.82-1.0 of the card.
        func cardBox(_ b: CGRect) -> [String: Double] {
            ["u0": Double(b.minX), "u1": Double(b.maxX),
             "vMid": Double(0.82 + (1 - b.midY) * 0.18),
             "vTop": Double(0.82 + (1 - b.maxY) * 0.18)]
        }
        if let credit = lines.first(where: { squash($0.text).contains("llu")
            || squash($0.text).hasPrefix("lus") || squash($0.text).hasPrefix("flu") }) {
            out["credit"] = cardBox(credit.box)
            out["creditText"] = credit.text
        }
        if let company = lines.first(where: {
            let s = squash($0.text)
            return s.contains("wizard") || s.contains("coast") || $0.text.contains("©")
        }) {
            out["company"] = cardBox(company.box)
            out["companyText"] = company.text
        }
    }

    let wide = SparkleWindow(
        u: SparkleGate.searchU * 4, v: SparkleGate.searchV * 4,
        cellsU: SparkleGate.searchCellsU * 4, cellsV: SparkleGate.searchCellsV * 4)
    if let fitted = sparkleInCard(flat)?.luma {
        out["fittedScore"] = Double(fitted.score)
        out["fittedU"] = Double(fitted.offsetU); out["fittedV"] = Double(fitted.offsetV)
        out["fittedContrast"] = Double(fitted.contrast)
    }
    if let wideR = sparkleInCard(flat, window: wide)?.luma {
        out["wideScore"] = Double(wideR.score)
        out["wideU"] = Double(wideR.offsetU); out["wideV"] = Double(wideR.offsetV)
    }
    // The A/B: the same fitted window with V re-centred on the copyright
    // row's own middle, offset by the fitted constant. U stays the quad's.
    if let company = out["company"] as? [String: Double], let vMid = company["vMid"] {
        let shiftV = CGFloat(vMid - 0.0671) - CardLayout.sparkleV
        if let anchored = sparkleInCard(flat, anchorShiftV: shiftV)?.luma {
            out["anchoredScore"] = Double(anchored.score)
            out["anchoredU"] = Double(anchored.offsetU)
            out["anchoredV"] = Double(anchored.offsetV)
            out["anchoredContrast"] = Double(anchored.contrast)
            out["anchorShiftV"] = Double(shiftV)
        }
    }
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

/// FlatCard samples a flattened card image in full-card coordinates — the
/// probe-side twin of the corpus crop sampler, for experiments that run on
/// whole stills rather than pre-cut crops.
struct FlatCard {
    let width: Int, height: Int
    private let data: [UInt8]

    init?(_ cg: CGImage) {
        let w = cg.width, h = cg.height
        guard w > 0, h > 0 else { return nil }
        var buf = [UInt8](repeating: 0, count: w * h * 4)
        let ok = buf.withUnsafeMutableBytes { raw -> Bool in
            guard let ctx = CGContext(
                data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8,
                bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        guard ok else { return nil }
        width = w; height = h; data = buf
    }

    // The same 709 luma the live PixelReader uses, so scores compare.
    func luma(_ u: CGFloat, _ v: CGFloat) -> CGFloat? {
        let x = Int((u * CGFloat(width)).rounded(.down))
        let y = Int((v * CGFloat(height)).rounded(.down))
        guard x >= 0, x < width, y >= 0, y < height else { return nil }
        let o = (y * width + x) * 4
        let r = CGFloat(data[o]) / 255, g = CGFloat(data[o + 1]) / 255,
            b = CGFloat(data[o + 2]) / 255
        return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
}

/// sparkleShape scores the binarised marker patch against the binarised
/// template — the shape channel experiment. Correlation dies when washout
/// compresses the patch's dynamic range; a median split only asks *which*
/// cells are the brighter ones, which survives any monotone lighting change
/// by construction. Whether it survives the real captures is what this mode
/// measures.
@available(macOS 15, *)
func sparkleShape(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else { die("no card located in \(path)") }
    guard let flat = FlatCard(located.image) else { die("no pixels in \(path)") }
    let sample: CardSampler = { u, v in flat.luma(u, v) }

    // Locate exactly the way the shipping reader would, then judge the shape
    // at the same spot the correlation judged.
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestP: [CGFloat]? = nil
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, sparkleTemplate)
            if s > best { best = s; bestP = p }
        }
    }
    var out: [String: Any] = ["file": (path as NSString).lastPathComponent]
    if let p = bestP {
        out["score"] = Double(best)
        let sorted = p.sorted()
        let median = sorted[p.count / 2]
        out["contrast"] = Double(medianAbsDev(p))
        let binP = p.map { $0 > median }
        let binT = sparkleTemplate.map { $0 > 0 }
        var agree = 0, interB = 0, unionB = 0
        for (a, b) in zip(binP, binT) {
            if a == b { agree += 1 }
            if a && b { interB += 1 }
            if a || b { unionB += 1 }
        }
        out["shapeAgree"] = Double(agree) / Double(p.count)
        out["shapeIoU"] = unionB > 0 ? Double(interB) / Double(unionB) : 0
        // Row profile: a marker patch is brightest in its middle rows (the
        // star core and tail run through the centre); a patch straddling the
        // text box's bottom edge is a monotone ramp, bright rows on one side
        // and dark on the other. rowRamp is the correlation of row means with
        // row index — near ±1 for an edge, near 0 for a star.
        let cols = SparkleTemplate.cols, rowsN = SparkleTemplate.rows
        var rowMeans = [CGFloat](repeating: 0, count: rowsN)
        for j in 0..<rowsN {
            var sum: CGFloat = 0
            for i in 0..<cols { sum += p[j * cols + i] }
            rowMeans[j] = sum / CGFloat(cols)
        }
        let mean = rowMeans.reduce(0, +) / CGFloat(rowsN)
        let idxMean = CGFloat(rowsN - 1) / 2
        var num: CGFloat = 0, dR: CGFloat = 0, dI: CGFloat = 0
        for j in 0..<rowsN {
            let a = rowMeans[j] - mean, b = CGFloat(j) - idxMean
            num += a * b; dR += a * a; dI += b * b
        }
        let denom = (dR * dI).squareRoot()
        out["rowRamp"] = denom > 0 ? Double(num / denom) : 0
        let mid = rowsN / 3
        let midMean = rowMeans[mid..<(rowsN - mid)].reduce(0, +) / CGFloat(rowsN - 2 * mid)
        out["centerBump"] = Double(midMean - mean)
    }
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

/// Median absolute deviation, probe-local: BorderKit's is internal and the
/// experiment only needs the same number, not the same symbol.
func medianAbsDev(_ xs: [CGFloat]) -> CGFloat {
    func median(_ v: [CGFloat]) -> CGFloat {
        let s = v.sorted()
        return s.isEmpty ? 0 : s[s.count / 2]
    }
    let m = median(xs)
    return median(xs.map { abs($0 - m) })
}

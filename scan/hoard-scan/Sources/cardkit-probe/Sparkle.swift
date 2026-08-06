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

/// A grayscale copy of a corpus crop, sampled in card space.
struct CorpusCard {
    let width: Int, height: Int
    private let data: [UInt8]

    init?(_ url: URL) {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { return nil }
        let w = cg.width, h = cg.height
        guard w > 0, h > 0 else { return nil }
        var buf = [UInt8](repeating: 0, count: w * h)
        let ok = buf.withUnsafeMutableBytes { raw -> Bool in
            guard let ctx = CGContext(
                data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8,
                bytesPerRow: w, space: CGColorSpaceCreateDeviceGray(),
                bitmapInfo: CGImageAlphaInfo.none.rawValue) else { return false }
            ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        guard ok else { return nil }
        width = w; height = h; data = buf
    }

    /// The card-space sampler. Nearest neighbour, matching how the reader
    /// samples the live frame.
    var sampler: CardSampler {
        { u, v in
            let x = Int((u - CorpusCrop.u0) / (CorpusCrop.u1 - CorpusCrop.u0) * CGFloat(width))
            let y = Int((v - CorpusCrop.v0) / (CorpusCrop.v1 - CorpusCrop.v0) * CGFloat(height))
            guard x >= 0, x < width, y >= 0, y < height else { return nil }
            return CGFloat(data[y * width + x]) / 255
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
func sparkleFit(dir: String, out: String, onlySession: String?) -> Never {
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
        guard let p = sparklePatch(du: 0, dv: 0, step: 1, card.sampler),
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
            guard let p = alignedPatch(card, template), let n = sparkleNormalise(p) else { continue }
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
    // The foil sparkle's template. GENERATED — do not hand-edit.
    //
    //     make cardkit
    //     ./bin/cardkit-probe --sparkle-fit scan/foil-corpus \\
    //         scan/hoard-scan/Sources/BorderKit/SparkleTemplateData.swift
    //
    // \(SparkleTemplate.cols)x\(SparkleTemplate.rows) cells spanning
    // SparkleTemplate.spanU x spanV of the card, zero-mean and unit-norm, so
    // correlating a normalised patch against it is a plain dot product. See
    // Sparkle.swift for what it is and why it is shaped this way.

    import CoreGraphics

    /// How many foil captures were averaged into this. Recorded because the
    /// threshold beside it is only as good as the corpus behind both.
    public let sparkleTemplateFittedFrom = \(used)

    public let sparkleTemplate: [CGFloat] = [\n
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

    for row in labels {
        guard let card = CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png")),
              let r = sparkleScan(card.sampler) else {
            print("\(row.id)\tNO READING")
            continue
        }
        maxSamples = max(maxSamples, r.samples)
        let key = row.frame == "modern" ? "modern" : "retro \(row.finish)"
        byClass[key, default: []].append((r.score, row))
        if verbose {
            print(String(format: "%-7@ %-14@ %-8@ %7.3f  du %+.4f dv %+.4f  mad %.3f",
                         row.id as NSString, row.name.prefix(14) as NSString,
                         row.finish as NSString, Double(r.score),
                         Double(r.offsetU), Double(r.offsetV), Double(r.contrast)))
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

    // Every card that came out on the wrong side, named. A rate without the
    // names is not diagnosable.
    for (key, rows) in byClass.sorted(by: { $0.key < $1.key }) {
        let wrong = key == "retro foil"
            ? rows.filter { $0.0 < SparkleGate.accept }
            : rows.filter { $0.0 >= SparkleGate.accept }
        for (score, row) in wrong.sorted(by: { $0.0 > $1.0 }) {
            let what = key == "retro foil" ? "MISSED foil" : "FALSE POSITIVE"
            print(String(format: "  %@: %@ %@ (%.3f)", what as NSString,
                         row.id as NSString, row.name as NSString, Double(score)))
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

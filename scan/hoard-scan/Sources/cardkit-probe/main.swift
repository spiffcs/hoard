// cardkit-probe — run CardKit over an image file and print the result.
//
// This exists so the new pipeline can be scored against the same 231 labelled
// images the old one is, with the harness that already exists:
//
//     make cardkit && HELPER=./bin/cardkit-probe ./scan/corpus/sweep.sh
//
// It therefore speaks the old helper's event shape rather than CardKit's own.
// That is not a wire contract here — it is a test adapter, and the shape it
// emits is whatever scan/corpus/score.py reads: a `scan` event carrying a name
// and a collector number.
//
// Rewriting a pipeline from scratch is only defensible if the rewrite can be
// compared to what it replaces, on ground truth, in the same table.

import CardKit
import CoreGraphics
import Foundation
import ImageIO
import ScanWire

func die(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(2)
}

/// The whole program, in a function so its availability can be annotated. Top
/// level code cannot be, and a `guard #available` there does not narrow what
/// follows it.
@available(macOS 15, *)
func run() async -> Never {
    let args = Array(CommandLine.arguments.dropFirst())
    // --bench reads a directory in one process. The per-image mode launches a
    // process per card, which is right for scoring but useless for timing: the
    // launch costs more than the read it is trying to measure.
    if let b = args.firstIndex(of: "--bench"), b + 1 < args.count {
        await bench(dir: args[b + 1])
    }
    if let f = args.firstIndex(of: "--flatten"), f + 2 < args.count {
        await flatten(dir: args[f + 1], out: args[f + 2])
    }
    if let m = args.firstIndex(of: "--score"), m + 1 < args.count {
        await score(manifest: args[m + 1], misses: args.contains("--misses"))
    }
    guard let i = args.firstIndex(of: "--image"), i + 1 < args.count else {
        die("usage: cardkit-probe --image <path> | --bench <dir>")
    }
    let path = args[i + 1]
    var rotation = 0
    if let r = args.firstIndex(of: "--rotate"), r + 1 < args.count {
        rotation = Int(args[r + 1]) ?? 0
    }

    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }

    // The orientation tag is baked into the pixels before anything reads them,
    // the same normalisation the live path does — applying it twice puts the
    // title at the bottom.
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up

    let reading = await readCard(uprighted(cg, orientation))
    // --border prints what the border reader saw, verdict or not.
    //
    // The scan event carries only the colour, which is right for the wire and
    // useless for diagnosis: a card that abstains looks identical to one never
    // asked. Every gate's number rides along here so a fixture can be argued
    // about from the numbers rather than from a hypothesis — which is what the
    // white-on-white failures needed and did not have.
    if args.contains("--border") {
        let b = reading.border
        let out: [String: Any] = [
            "file": (path as NSString).lastPathComponent,
            "name": reading.title,
            "color": b.color ?? "-", "abstain": b.abstain,
            "source": b.source ?? "-", "anchorKind": b.anchorKind,
            "t": b.t, "standoff": b.standoff,
            "scaleAgreement": b.scaleAgreement,
            "cardHeightPx": b.cardHeightPx,
            "borderMS": reading.timings.border,
        ]
        if let d = try? JSONSerialization.data(withJSONObject: out),
           let line = String(data: d, encoding: .utf8) {
            print(line)
        }
        exit(b.color == nil ? 3 : 0)
    }
    // Through ScanWire's emit, so this speaks the identical dialect the helper
    // does rather than a probe-shaped approximation of it.
    emit(reading.scanEvent(rotation: rotation))
    exit(reading.title.isEmpty ? 3 : 0)
}

/// score replays the labelled corpus in one process.
///
/// Same rules as `scan/corpus/score.py` — lenient on the name because the job
/// is to hand fuzzy matching something it can land, strict on the number,
/// with an empty number counting as correct before 1998 when cards printed
/// none. Kept in step with that file deliberately: this is the fast path, not
/// a second opinion.
///
/// It exists because `sweep.sh` spends its time launching 231 processes rather
/// than reading 231 cards, which made the one loop that guards against
/// accuracy regressions the slowest loop in the project.
@available(macOS 15, *)
func score(manifest: String, misses: Bool) async -> Never {
    let dir = URL(fileURLWithPath: manifest).deletingLastPathComponent()
    guard let text = try? String(contentsOfFile: manifest, encoding: .utf8) else {
        die("no manifest at \(manifest)")
    }
    func norm(_ s: String) -> String {
        s.lowercased().filter { $0.isLetter || $0.isNumber }
    }

    var foreignTotal = 0, foreignNameOK = 0, foreignNumOK = 0
    var borderAsked = 0, borderAnswered = 0, borderRight = 0
    var borderWrong: [String] = [], borderFalse: [String] = []
    var coverRight: [Double] = [], coverWrong: [Double] = []
    var agg: [String: (n: Int, name: Int, num: Int)] = [:]
    var totals = (n: 0, name: 0, num: 0)
    var missed: [String] = []
    var times: [Double] = []

    for row in text.split(separator: "\n").dropFirst() {
        let f = row.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
        // Manifest columns: sid, era, border, name, set, number, rel, lang —
        // the set sits between the name and the number, which is easy to skip
        // past, and lang was appended later so older manifests lack it.
        guard f.count >= 6 else { continue }
        let (sid, era, border, wantName, wantNum) = (f[0], f[1], f[2], f[3], f[5])
        let lang = f.count >= 8 ? f[7] : "en"
        let img = dir.appendingPathComponent("images/\(sid).png")
        guard let src = CGImageSourceCreateWithURL(img as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { continue }
        let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
            .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
            .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up

        let r = await readCard(uprighted(cg, orientation))
        times.append(r.timings.total)
        // Border scoring, against the manifest's own border column. The reader
        // may only claim white/black/gold/silver, so anything else in the
        // ground truth (borderless, yellow) is counted as "not asked" rather
        // than as a miss it could never have avoided.
        if ["white", "black", "gold", "silver"].contains(border) {
            borderAsked += 1
            let cover = r.cardBox.map { Double($0.width * $0.height) } ?? 0
            if let c = r.border.color {
                borderAnswered += 1
                if c == border { borderRight += 1; coverRight.append(cover) }
                else { coverWrong.append(cover) }
                if c == border {}
                else {
                    let bx = r.cardBox.map {
                        String(format: "box=%.3f,%.3f %.3fx%.3f",
                               $0.minX, $0.minY, $0.width, $0.height)
                    } ?? "box=none"
                    borderWrong.append(String(
                        format: "%@: said %@, is %@  t=%.2f standoff=%.2f anchor=%@ %@",
                        wantName as NSString, c as NSString, border as NSString,
                        r.border.t, r.border.standoff,
                        r.border.anchorKind as NSString, bx as NSString))
                }
            }
        } else if let c = r.border.color {
            borderFalse.append("\(wantName): said \(c), is \(border)")
        }
        let a = norm(wantName), b = norm(r.title)
        let nameOK = !a.isEmpty && !b.isEmpty
            && (a == b || a.hasPrefix(b) || b.hasPrefix(a))
        let numOK = r.printing.number == wantNum
            || (era == "pre1998" && r.printing.number.isEmpty)

        // Non-English printings are scored apart, not counted as failures.
        //
        // Their images are Italian, Spanish, Japanese cards; the manifest holds
        // the *English* name because that is what the catalog is keyed on. A
        // perfect read of "Miniera a Cielo Aperto" was being marked a failure
        // to read "Strip Mine" — a fifth of all name misses, and most of why
        // pre-1998 black looked 35 points worse than its white sibling.
        //
        // Reading them properly is its own piece of work. Until then the
        // headline answers "how well does this read the cards it is for".
        if lang != "en" {
            foreignTotal += 1
            if nameOK { foreignNameOK += 1 }
            if numOK { foreignNumOK += 1 }
            continue
        }

        let key = "\(era)\t\(border)"
        var e = agg[key] ?? (0, 0, 0)
        e.n += 1; e.name += nameOK ? 1 : 0; e.num += numOK ? 1 : 0
        agg[key] = e
        totals.n += 1; totals.name += nameOK ? 1 : 0; totals.num += numOK ? 1 : 0
        if !nameOK || !numOK {
            missed.append("  [\(era)/\(border)] want '\(wantName) #\(wantNum)' "
                + "got '\(r.title) #\(r.printing.number)'")
        }
    }

    // Plain padding rather than String(format:) — "%s" there expects a C
    // string and hands a Swift String straight to a segfault.
    func pad(_ s: String, _ n: Int) -> String {
        s.count >= n ? s : s + String(repeating: " ", count: n - s.count)
    }
    func pct(_ a: Int, _ b: Int) -> String {
        let v = b == 0 ? 0 : Int((100.0 * Double(a) / Double(b)).rounded())
        return String(repeating: " ", count: max(0, 5 - "\(v)%".count)) + "\(v)%"
    }
    print(pad("era", 12) + pad("border", 12) + pad("n", 6) + pad("name", 8) + "number")
    print(String(repeating: "-", count: 46))
    for key in agg.keys.sorted() {
        let e = agg[key]!, p = key.split(separator: "\t").map(String.init)
        print(pad(p[0], 12) + pad(p[1], 12) + pad("\(e.n)", 6)
            + pad(pct(e.name, e.n), 8) + pct(e.num, e.n))
    }
    print(String(repeating: "-", count: 46))
    let med = times.isEmpty ? 0 : times.sorted()[times.count / 2]
    if foreignTotal > 0 {
        print(pad("(non-English)", 24) + pad("\(foreignTotal)", 6)
            + pad(pct(foreignNameOK, foreignTotal), 8) + pct(foreignNumOK, foreignTotal)
            + "   scored apart; see docs/scanner-accuracy.md")
    }
    print(pad("ALL", 24) + pad("\(totals.n)", 6) + pad(pct(totals.name, totals.n), 8)
        + pct(totals.num, totals.n)
        + "   (median read " + String(format: "%.0f", med) + "ms)")
    // The two numbers that decide whether a border may ever settle a printing:
    // how often it answers at all, and how often it is right when it does.
    let cover = borderAsked == 0 ? 0 : 100 * borderAnswered / borderAsked
    let acc = borderAnswered == 0 ? 0 : 100 * borderRight / borderAnswered
    print("border: asked \(borderAsked), answered \(borderAnswered) (\(cover)%), "
        + "correct \(borderRight)/\(borderAnswered) (\(acc)%)")
    func mid(_ v: [Double]) -> Double { v.isEmpty ? 0 : v.sorted()[v.count / 2] }
    print(String(format: "  card-box coverage: correct %.3f (n=%d), wrong %.3f (n=%d)",
                 mid(coverRight), coverRight.count, mid(coverWrong), coverWrong.count))
    let tight = coverRight.filter { $0 > 0.95 }.count
    let tightW = coverWrong.filter { $0 > 0.95 }.count
    print("  where the box kept the border (>0.95): "
        + "\(tight)/\(tight + tightW) correct")
    if !borderFalse.isEmpty {
        print("  claimed a colour on \(borderFalse.count) card(s) it was not asked about")
    }
    if misses {
        borderWrong.forEach { print("  WRONG  " + $0) }
        borderFalse.forEach { print("  EXTRA  " + $0) }
        missed.forEach { print($0) }
    }
    exit(0)
}

/// flatten writes what the reader actually sees: the perspective-corrected
/// card, not the photograph. Every border question is asked of these pixels, so
/// looking at the photograph instead is looking at the wrong image.
@available(macOS 15, *)
func flatten(dir: String, out: String) async -> Never {
    try? FileManager.default.createDirectory(
        atPath: out, withIntermediateDirectories: true)
    let urls = ((try? FileManager.default.contentsOfDirectory(
        at: URL(fileURLWithPath: dir), includingPropertiesForKeys: nil)) ?? [])
        .filter { ["jpg", "jpeg", "png"].contains($0.pathExtension.lowercased()) }
        .sorted { $0.lastPathComponent < $1.lastPathComponent }
    for (i, url) in urls.enumerated() {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { continue }
        // Phone stills arrive in sensor orientation with no EXIF tag, so the
        // tag says .up and the pixels are a quarter turn out. The live read
        // applies .right before anything looks at them; a fixture that does not
        // is a picture of a sideways card, which no aspect check will accept.
        let tagged = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
            .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
            .flatMap { CGImagePropertyOrientation(rawValue: $0) }
        var up = uprighted(cg, tagged ?? .up)
        var card = locateCard(up)
        if card == nil, tagged == nil {
            up = uprighted(cg, .right)
            card = locateCard(up)
        }
        guard let card else {
            print("\(i + 1)\tNO CARD LOCATED\t\(url.lastPathComponent)")
            continue
        }
        let dst = URL(fileURLWithPath: out)
            .appendingPathComponent(String(format: "%02d.png", i + 1))
        if let d = CGImageDestinationCreateWithURL(
            dst as CFURL, "public.png" as CFString, 1, nil) {
            CGImageDestinationAddImage(d, card.image, nil)
            CGImageDestinationFinalize(d)
        }
        // Both readers on the same pixels, so they can be scored side by side
        // against the same labels rather than across separate runs.
        let r = await readCard(up)
        print(String(format: "%d\tset=%@\tnum=%@\tfinish=%@\tyear=%@\trecovered=%@\t%@",
                     i + 1,
                     (r.printing.setCode.isEmpty ? "-" : r.printing.setCode) as NSString,
                     (r.printing.number.isEmpty ? "-" : r.printing.number) as NSString,
                     (r.printing.finish.isEmpty ? "-" : r.printing.finish) as NSString,
                     (r.printing.year.map(String.init) ?? "-") as NSString,
                     (r.footerRecovered ? "YES" : "no") as NSString,
                     r.title as NSString))
    }
    exit(0)
}

@available(macOS 15, *)
func bench(dir: String) async -> Never {
    let urls = ((try? FileManager.default.contentsOfDirectory(
        at: URL(fileURLWithPath: dir), includingPropertiesForKeys: nil)) ?? [])
        .filter { ["jpg", "jpeg", "png", "heic"].contains($0.pathExtension.lowercased()) }
        .sorted { $0.lastPathComponent < $1.lastPathComponent }
    guard !urls.isEmpty else { die("no images in \(dir)") }

    var locate: [Double] = [], whole: [Double] = [], band: [Double] = [], total: [Double] = []
    var titled = 0, numbered = 0
    for url in urls {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { continue }
        let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
            .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
            .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
        let r = await readCard(uprighted(cg, orientation))
        locate.append(r.timings.locate); whole.append(r.timings.whole)
        band.append(r.timings.band); total.append(r.timings.total)
        if !r.title.isEmpty { titled += 1 }
        if !r.printing.number.isEmpty { numbered += 1 }
    }
    func med(_ v: [Double]) -> Double { v.sorted()[v.count / 2] }
    print(String(
        format: "n=%d  locate=%.0f  whole=%.0f  band=%.0f  total=%.0f  named=%d  numbered=%d",
        total.count, med(locate), med(whole), med(band), med(total), titled, numbered))
    exit(0)
}

if #available(macOS 15, *) {
    await run()
} else {
    fail("cardkit-probe needs macOS 15")
}

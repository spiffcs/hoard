import CardKit
import CoreGraphics
import Foundation
import ImageIO
import ScanWire

func die(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(2)
}

func flagValue(_ args: [String], _ name: String) -> String? {
    guard let i = args.firstIndex(of: name), i + 1 < args.count else { return nil }
    return args[i + 1]
}

func writePNG(_ image: CGImage, to path: String) {
    let url = URL(fileURLWithPath: path) as CFURL
    guard let dest = CGImageDestinationCreateWithURL(url, "public.png" as CFString, 1, nil)
    else { die("could not create \(path)") }
    CGImageDestinationAddImage(dest, image, nil)
    guard CGImageDestinationFinalize(dest) else { die("could not write \(path)") }
}

@available(macOS 15, *)
func run() async -> Never {
    let args = Array(CommandLine.arguments.dropFirst())
    if let b = args.firstIndex(of: "--bench"), b + 1 < args.count {
        await bench(dir: args[b + 1])
    }
    if let f = args.firstIndex(of: "--flatten"), f + 2 < args.count {
        await flatten(dir: args[f + 1], out: args[f + 2])
    }
    if let m = args.firstIndex(of: "--score"), m + 1 < args.count {
        await score(manifest: args[m + 1], misses: args.contains("--misses"))
    }
    if let f = args.firstIndex(of: "--sparkle-fit"), f + 2 < args.count {
        var only: String? = nil
        if let s = args.firstIndex(of: "--only-session"), s + 1 < args.count {
            only = args[s + 1]
        }
        sparkleFit(dir: args[f + 1], out: args[f + 2], onlySession: only,
                   chroma: args.contains("--chroma"))
    }
    if let a = args.firstIndex(of: "--anchor-fit"), a + 1 < args.count {
        await anchorFit(manifest: args[a + 1])
    }
    if let s = args.firstIndex(of: "--sparkle-score"), s + 1 < args.count {
        var only: String? = nil
        if let o = args.firstIndex(of: "--only-session"), o + 1 < args.count {
            only = args[o + 1]
        }
        sparkleScoreCorpus(dir: args[s + 1], verbose: args.contains("--cards"),
                           onlySession: only)
    }
    if let s = args.firstIndex(of: "--sparkle-chroma-trial"), s + 1 < args.count {
        sparkleChromaTrial(dir: args[s + 1])
    }
    if let s = args.firstIndex(of: "--sparkle-where"), s + 1 < args.count {
        await sparkleWhere(path: args[s + 1])
    }
    if let s = args.firstIndex(of: "--credit-anchor"), s + 1 < args.count {
        await creditAnchor(path: args[s + 1])
    }
    if let s = args.firstIndex(of: "--sparkle-shape"), s + 1 < args.count {
        await sparkleShape(path: args[s + 1])
    }
    if let s = args.firstIndex(of: "--sparkle-control"), s + 1 < args.count {
        await sparkleControl(path: args[s + 1])
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

    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up

    let emitSparkle = flagValue(args, "--emit-sparkle")
    if emitSparkle != nil {
        guard let card = locateCard(uprighted(cg, orientation)) else { exit(4) }
        if let path = emitSparkle {
            let w = CGFloat(card.image.width), h = CGFloat(card.image.height)
            let du = SparkleGate.searchU + SparkleTemplate.spanU / 2 + 0.012
            let dv = SparkleGate.searchV + SparkleTemplate.spanV / 2 + 0.030
            let rect = CGRect(x: (CardLayout.sparkleU - du) * w,
                              y: (CardLayout.sparkleV - dv) * h,
                              width: 2 * du * w, height: 2 * dv * h)
                .intersection(CGRect(x: 0, y: 0, width: w, height: h))
            guard let patch = card.image.cropping(to: rect) else { exit(4) }
            writePNG(patch, to: path)
        }
    }

    let reading = await readCard(uprighted(cg, orientation))
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
            "horizontalAnchor": b.horizontalAnchor,
            "sparkleScore": reading.sparkle?.luma.map { Double($0.score) } ?? -9,
            "sparkleOffsetU": reading.sparkle?.luma.map { Double($0.offsetU) } ?? 0,
            "sparkleOffsetV": reading.sparkle?.luma.map { Double($0.offsetV) } ?? 0,
            "sparkleSamples": reading.sparkle?.luma.map(\.samples) ?? 0,
            "sparkleChroma": reading.sparkle?.chroma.map { Double($0.score) } ?? -9,
            "sparkleChannel": reading.sparkle?.channel ?? "",
            "symbolCoverage": b.symbolCoverage,
            "symbolContrast": b.symbolContrast,
            "sparkleMS": reading.timings.sparkle,
            "finish": reading.printing.finish,
            "finishSource": reading.printing.finishSource,
            "retroFooter": retroFrameFooter(reading.bandLines + reading.lines),
        ]
        if let d = try? JSONSerialization.data(withJSONObject: out),
           let line = String(data: d, encoding: .utf8) {
            print(line)
        }
        exit(b.color == nil ? 3 : 0)
    }
    emit(reading.scanEvent(rotation: rotation))
    exit(reading.title.isEmpty ? 3 : 0)
}

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
    var langAsked = 0, langAnswered = 0, langRight = 0
    var langWrong: [String] = []
    var borderAsked = 0, borderAnswered = 0, borderRight = 0
    var borderWrong: [String] = [], borderFalse: [String] = []
    var coverRight: [Double] = [], coverWrong: [Double] = []
    var agg: [String: (n: Int, name: Int, num: Int)] = [:]
    var totals = (n: 0, name: 0, num: 0)
    var missed: [String] = []
    var times: [Double] = []

    for row in text.split(separator: "\n").dropFirst() {
        let f = row.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
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

        langAsked += 1
        if let read = scryfallLanguage(r.printing.language) {
            langAnswered += 1
            if read == lang { langRight += 1 }
            else { langWrong.append("  \(wantName): said \(read), is \(lang)") }
        }

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
    print("")
    print(pad("LANGUAGE", 24) + pad("n", 6) + pad("read", 8) + "correct")
    print(pad("", 24) + pad("\(langAsked)", 6)
        + pad(pct(langAnswered, langAsked), 8) + pct(langRight, langAnswered))
    if !langWrong.isEmpty {
        print("wrong language:")
        for l in langWrong.sorted() { print(l) }
    }
    if foreignTotal > 0 {
        print(pad("(non-English)", 24) + pad("\(foreignTotal)", 6)
            + pad(pct(foreignNameOK, foreignTotal), 8) + pct(foreignNumOK, foreignTotal)
            + "   scored apart")
    }
    print(pad("ALL", 24) + pad("\(totals.n)", 6) + pad(pct(totals.name, totals.n), 8)
        + pct(totals.num, totals.n)
        + "   (median read " + String(format: "%.0f", med) + "ms)")
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

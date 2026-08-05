// Derive the iOS app icon from the macOS helper's .icns.
//
// One artwork, two platforms, and they want opposite things from it:
//
//   macOS ships the rounded squircle itself, with transparent margin around it,
//   because the Dock draws exactly the pixels given.
//   iOS wants a full-bleed opaque square and applies its own mask. Handing it
//   the macOS icon would render a small rounded card inside another rounded
//   card, with the transparent corners flattened to whatever is behind them.
//
// So this finds the artwork inside the squircle, takes the largest square that
// contains no transparent pixel, and writes it opaque at 1024.
//
// Cropping to the squircle's *bounding box* instead was tried, to keep more
// resolution — 408px of the 512 rather than 356 — on the reasoning that Apple
// rounds by about the same proportion on both platforms, so the transparent
// corners would land under the iOS mask. Measured, they do not: the macOS
// template's radius is visibly larger than the iOS mask cuts, and the filled
// corners show as four dark wedges. Full-bleed artwork with more upscaling
// looks better than more pixels with visible corners.
//
// A script rather than a checked-in second copy: the .icns is the source of
// truth for this artwork, and two PNGs that must not drift is a worse problem
// than a build step.

import AppKit
import CoreGraphics
import Foundation

let args = CommandLine.arguments
guard args.count == 3 else {
    FileHandle.standardError.write(Data("usage: make-icon <in.png> <out.png>\n".utf8))
    exit(2)
}
guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: args[1]) as CFURL, nil),
      let img = CGImageSourceCreateImageAtIndex(src, 0, nil) else {
    FileHandle.standardError.write(Data("could not read \(args[1])\n".utf8))
    exit(2)
}

// Read the alpha channel so the crop is measured rather than guessed. The
// margin and corner radius of Apple's icon template have changed before.
let w = img.width, h = img.height
var px = [UInt8](repeating: 0, count: w * h * 4)
px.withUnsafeMutableBytes { raw in
    let ctx = CGContext(data: raw.baseAddress, width: w, height: h,
                        bitsPerComponent: 8, bytesPerRow: w * 4,
                        space: CGColorSpaceCreateDeviceRGB(),
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: 0, y: 0, width: w, height: h))
}
func opaque(_ x: Int, _ y: Int) -> Bool { px[(y * w + x) * 4 + 3] > 250 }

// Grow an inset inward until every pixel on the square's border is opaque.
// Testing the border alone is enough: the artwork is solid, so the only
// transparency is the template's margin and its rounded corners.
var inset = 0
while inset < w / 2 {
    let lo = inset, hi = w - 1 - inset
    var clean = true
    for t in stride(from: lo, through: hi, by: 1) {
        if !opaque(t, lo) || !opaque(t, hi) || !opaque(lo, t) || !opaque(hi, t) {
            clean = false
            break
        }
    }
    if clean { break }
    inset += 1
}
let side = w - inset * 2
guard side > w / 2 else {
    FileHandle.standardError.write(Data("no opaque square found\n".utf8))
    exit(2)
}

// 1024 is what the App Store asks for. The source is 512, so this upscales —
// worth stating plainly: it is the best the tracked artwork allows, and a
// larger original would be a straight improvement.
let out = 1024
let ctx = CGContext(data: nil, width: out, height: out, bitsPerComponent: 8,
                    bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                    // No alpha at all. An icon with an alpha channel is
                    // rejected at submission, and it is the kind of rejection
                    // that arrives after everything else is ready.
                    bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
ctx.interpolationQuality = .high
let crop = img.cropping(to: CGRect(x: inset, y: inset, width: side, height: side))!
ctx.draw(crop, in: CGRect(x: 0, y: 0, width: out, height: out))

let dest = CGImageDestinationCreateWithURL(
    URL(fileURLWithPath: args[2]) as CFURL, "public.png" as CFString, 1, nil)!
CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
guard CGImageDestinationFinalize(dest) else { exit(2) }
print("cropped \(w)x\(h) inset \(inset) -> \(side)x\(side) -> \(out)x\(out) opaque")

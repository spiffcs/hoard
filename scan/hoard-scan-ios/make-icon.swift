// Build the iOS app icon from the project logo.
//
//   swift make-icon.swift ../../docs/assets/logo-master.png \
//       Resources/Assets.xcassets/AppIcon.appiconset/icon-1024.png
//
// One artwork for the README and the app, because two copies of a logo are two
// things that drift. The README wants it on transparency at whatever size the
// page asks for; iOS wants the opposite — a full-bleed, fully opaque square,
// which it then masks into a squircle itself. This is the conversion.
//
// Alpha is the part that is not negotiable. An icon submitted with an alpha
// channel is rejected, and that rejection arrives at the end, after everything
// else is ready. So the output context has no alpha component at all rather
// than an alpha channel that happens to be full: there is no way to get it
// subtly wrong.
//
// The earlier version of this script solved the reverse problem — it read the
// macOS helper's .icns, which was already a squircle with a transparent margin,
// and cut the largest opaque square out of the middle. That .icns is gone, and
// with it the assumption that the input arrives pre-composed. Artwork on a
// transparent field has no opaque square to find, so that algorithm would have
// looked for one until it ran out of image and exited.

import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

// The square's edge, and what the App Store asks for.
let out = 1024

// How much of that edge the mark's longest side may occupy. iOS masks the
// corners at roughly 22% of the side, so a mark pushed to the edges loses its
// corners; the remaining margin is also what stops the icon reading as a sticker
// crammed into its own frame. 0.82 is a judgement, not a requirement — it is the
// one number here worth re-rendering and eyeballing if the artwork changes.
let coverage = 0.82

// Two alpha thresholds, because the artwork's ink and its bounds are not the
// same question.
//
//   solid  — what a viewer calls "the logo". Frames the icon: this box is what
//            gets centered and scaled.
//   bleed  — every pixel that must still be drawn, including the drop shadow's
//            outer falloff. Cutting at `solid` would slice the shadow and leave
//            a hard edge where it was truncated.
//
// One threshold for both was the bug this replaces. The shadow falls to the
// right and not the left, so bounding the whole image and centering that box
// pushed the chest measurably left of center — the icon looked off and the
// cause was invisible, because the empty space doing the pushing is transparent.
let solid: UInt8 = 128
let bleed: UInt8 = 8

// The field behind the artwork. Near-black rather than a color from the mark
// itself: the chest is red and gold, and both need something dark and neutral
// to stay legible against, on a home screen the owner has covered in wallpaper.
let background = CGColor(red: 0x15 / 255.0, green: 0x16 / 255.0, blue: 0x1C / 255.0, alpha: 1)

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

// Measure the artwork rather than trusting the file's edges. A logo exported
// with whatever padding the drawing tool felt like would otherwise decide the
// icon's margins for it, and the result would change every time the artwork was
// re-exported. Reading the alpha channel makes the framing a property of the
// mark, not of the export.
let w = img.width, h = img.height
var px = [UInt8](repeating: 0, count: w * h * 4)
px.withUnsafeMutableBytes { raw in
    let ctx = CGContext(data: raw.baseAddress, width: w, height: h,
                        bitsPerComponent: 8, bytesPerRow: w * 4,
                        space: CGColorSpaceCreateDeviceRGB(),
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: 0, y: 0, width: w, height: h))
}

struct Box { var minX: Int, minY: Int, maxX: Int, maxY: Int
    var width: Int { maxX - minX + 1 }
    var height: Int { maxY - minY + 1 }
}

func bounds(over threshold: UInt8) -> Box? {
    var b = Box(minX: w, minY: h, maxX: -1, maxY: -1)
    for y in 0..<h {
        for x in 0..<w where px[(y * w + x) * 4 + 3] > threshold {
            if x < b.minX { b.minX = x }
            if x > b.maxX { b.maxX = x }
            if y < b.minY { b.minY = y }
            if y > b.maxY { b.maxY = y }
        }
    }
    return b.maxX >= b.minX && b.maxY >= b.minY ? b : nil
}

guard let ink = bounds(over: solid), let all = bounds(over: bleed) else {
    FileHandle.standardError.write(Data("input has no opaque artwork\n".utf8))
    exit(2)
}

let crop = img.cropping(to: CGRect(x: all.minX, y: all.minY,
                                   width: all.width, height: all.height))!

// Fit the *mark* inside the coverage box on its long side, so a wide logo and a
// tall one are framed by the same rule and neither is stretched.
let scale = Double(out) * coverage / Double(max(ink.width, ink.height))

// Place the crop so the mark's center lands on the icon's center, letting the
// shadow hang off wherever it falls. Both offsets are measured from the crop's
// top-left, because that is the origin CGImage.cropping and the pixel buffer
// agree on — but CGContext's y runs the other way, so the vertical placement is
// expressed through the rect's top edge (maxY) rather than its origin.
let dx = Double(ink.minX - all.minX), dy = Double(ink.minY - all.minY)
let drawW = Double(all.width) * scale, drawH = Double(all.height) * scale
let originX = Double(out) / 2 - (dx + Double(ink.width) / 2) * scale
let topY = Double(out) / 2 + (dy + Double(ink.height) / 2) * scale
let rect = CGRect(x: originX, y: topY - drawH, width: drawW, height: drawH)

let ctx = CGContext(data: nil, width: out, height: out, bitsPerComponent: 8,
                    bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                    bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
ctx.interpolationQuality = .high
ctx.setFillColor(background)
ctx.fill(CGRect(x: 0, y: 0, width: out, height: out))
ctx.draw(crop, in: rect)

let dest = CGImageDestinationCreateWithURL(
    URL(fileURLWithPath: args[2]) as CFURL, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
guard CGImageDestinationFinalize(dest) else { exit(2) }
let upscale = Double(out) * coverage / Double(max(ink.width, ink.height))
print("""
mark \(ink.width)x\(ink.height) (bleed \(all.width)x\(all.height)) from \(w)x\(h) \
-> \(Int(Double(ink.width) * scale))x\(Int(Double(ink.height) * scale)) \
on \(out)x\(out) opaque, \(String(format: "%.2f", upscale))x
""")

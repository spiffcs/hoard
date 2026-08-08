#!/usr/bin/env swift
// Train the foil/nonfoil marker-patch classifier, held-out by rig.
//
//   swift train-foil.swift s9            train on the other rigs, score on s9
//   swift train-foil.swift all out.mlmodel   train on everything, save the artifact
//
// The held-out form is the ship-gate: the chroma score looked perfect until it
// was scored on a rig it was not fitted on, and then it ranked the classes
// backwards. A model only ships if every rig, held out in turn, comes back
// with zero false-foils — the operator's standing constraint. Accuracy on the
// training rigs proves memorisation, not the model.
//
// CreateML rather than anything heavier: transfer learning off the OS's
// feature extractor works at this corpus size (~120 crops), trains in minutes
// on this Mac, needs no services, and emits a CoreML artifact both the phone
// and cardkit-probe can run in single-digit milliseconds.

import CreateML
import Foundation

let args = CommandLine.arguments
guard args.count >= 2 else {
    FileHandle.standardError.write(Data("usage: train-foil.swift <heldout-rig|all> [out.mlmodel]\n".utf8))
    exit(2)
}
let heldOut = args[1]
let here = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
let dataset = here.appendingPathComponent("dataset")
let fm = FileManager.default

let rigs = try fm.contentsOfDirectory(atPath: dataset.path).filter {
    var dir: ObjCBool = false
    return fm.fileExists(atPath: dataset.appendingPathComponent($0).path, isDirectory: &dir) && dir.boolValue
}
guard heldOut == "all" || rigs.contains(heldOut) else {
    FileHandle.standardError.write(Data("no rig \(heldOut) in \(rigs)\n".utf8))
    exit(2)
}

// Merge the training rigs into one class-per-directory tree via symlinks —
// MLImageClassifier reads one root, the corpus keeps rigs separate on disk so
// holding one out stays possible.
let work = fm.temporaryDirectory.appendingPathComponent("hoard-foil-train-\(UUID().uuidString)")
defer { try? fm.removeItem(at: work) }
for cls in ["foil", "nonfoil"] {
    try fm.createDirectory(at: work.appendingPathComponent(cls), withIntermediateDirectories: true)
}
for rig in rigs where heldOut == "all" || rig != heldOut {
    for cls in ["foil", "nonfoil"] {
        let src = dataset.appendingPathComponent(rig).appendingPathComponent(cls)
        guard let files = try? fm.contentsOfDirectory(atPath: src.path) else { continue }
        for f in files where f.hasSuffix(".png") {
            try? fm.createSymbolicLink(
                at: work.appendingPathComponent(cls).appendingPathComponent("\(rig)-\(f)"),
                withDestinationURL: src.appendingPathComponent(f))
        }
    }
}

var params = MLImageClassifier.ModelParameters()
// Augmentation stands in for the corpus the rigs have not photographed yet:
// exposure and blur are exactly the axes the rigs differ on.
params.augmentationOptions = [.exposure, .blur, .rotation]
params.maxIterations = 25

print("training (held out: \(heldOut))…")
let model = try MLImageClassifier(
    trainingData: .labeledDirectories(at: work), parameters: params)

if heldOut == "all" {
    let out = args.count >= 3 ? args[2] : "foil-marker.mlmodel"
    try model.write(to: URL(fileURLWithPath: out),
                    metadata: MLModelMetadata(
                        author: "hoard",
                        shortDescription: "retro-frame foil marker patch, foil/nonfoil",
                        version: "corpus-\(rigs.sorted().joined(separator: "+"))"))
    print("wrote \(out)")
    exit(0)
}

// Score the held-out rig, the only number that matters printed last: how many
// nonfoils the model would have called foil.
let held = dataset.appendingPathComponent(heldOut)
var falseFoil = 0, missedFoil = 0, right = 0, total = 0
for cls in ["foil", "nonfoil"] {
    let dir = held.appendingPathComponent(cls)
    guard let files = try? fm.contentsOfDirectory(atPath: dir.path) else { continue }
    for f in files where f.hasSuffix(".png") {
        total += 1
        let got = (try? model.prediction(from: dir.appendingPathComponent(f))) ?? "?"
        if got == cls {
            right += 1
        } else if got == "foil" {
            falseFoil += 1
            print("  FALSE-FOIL \(heldOut)/\(f)")
        } else {
            missedFoil += 1
        }
    }
}
print("\(heldOut) held out: \(right)/\(total) right, \(missedFoil) foils missed, \(falseFoil) FALSE-FOILS")
exit(falseFoil == 0 ? 0 : 1)

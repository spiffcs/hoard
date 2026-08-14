#!/usr/bin/env swift
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

import CoreML
import Vision

let tmpModel = fm.temporaryDirectory.appendingPathComponent("fold-\(heldOut).mlmodel")
try model.write(to: tmpModel)
let compiled = try MLModel.compileModel(at: tmpModel)
let ml = try MLModel(contentsOf: compiled)
let vn = try VNCoreMLModel(for: ml)

func pFoil(_ url: URL) -> Double? {
    let req = VNCoreMLRequest(model: vn)
    let handler = VNImageRequestHandler(url: url)
    guard (try? handler.perform([req])) != nil,
          let obs = req.results as? [VNClassificationObservation] else { return nil }
    return Double(obs.first(where: { $0.identifier == "foil" })?.confidence ?? 0)
}

let held = dataset.appendingPathComponent(heldOut)
var falseFoil = 0, missedFoil = 0, right = 0, total = 0
for cls in ["foil", "nonfoil"] {
    let dir = held.appendingPathComponent(cls)
    guard let files = try? fm.contentsOfDirectory(atPath: dir.path) else { continue }
    for f in files where f.hasSuffix(".png") {
        total += 1
        guard let p = pFoil(dir.appendingPathComponent(f)) else { continue }
        print(String(format: "PROB\t%@\t%@\t%.4f\t%@", heldOut, cls, p, f))
        let got = p >= 0.5 ? "foil" : "nonfoil"
        if got == cls {
            right += 1
        } else if got == "foil" {
            falseFoil += 1
        } else {
            missedFoil += 1
        }
    }
}
print("\(heldOut) held out: \(right)/\(total) right at p≥0.5, \(missedFoil) foils missed, \(falseFoil) false-foils")
exit(0)

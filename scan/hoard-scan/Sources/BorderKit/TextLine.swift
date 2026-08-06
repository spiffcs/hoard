// The border reader's input: recognized text with its geometry.
//
// These live here, with the reader, rather than in whichever OCR pipeline
// happens to produce them. Two pipelines do — ScanKit on the Mac and CardKit on
// the phone — and the reader is the only thing both share. Owning its own input
// type is what lets it be shared without either pipeline depending on the
// other, or on a common OCR layer that does not exist.

import CoreGraphics
import Foundation

/// A recognized text line with its full normalized bounding box (Vision origin
/// is bottom-left) and confidence. The box matters beyond ranking: multi-card
/// clustering needs horizontal position to tell which card a line belongs to,
/// which top-and-width alone cannot say.
public struct Line: Sendable {
    public let text: String
    public let box: CGRect
    public let confidence: Float
    /// The line's four corners. Vision hands these over already —
    /// VNRecognizedTextObservation is a VNRectangleObservation — and the axis-
    /// aligned box above throws away the one thing they add: how far the text
    /// is turned. The border reader needs that, because a tilted card's border
    /// ring is not a horizontal strip. nil only for lines built by hand.
    public var quad: Quad?

    public var top: CGFloat { box.maxY }
    public var width: CGFloat { box.width }

    public init(text: String, box: CGRect, confidence: Float = 1, quad: Quad? = nil) {
        self.text = text
        self.box = box
        self.confidence = confidence
        self.quad = quad
    }
}

/// Quad is a Vision rectangle's corners in normalized frame coordinates
/// (origin bottom-left), kept apart from CGRect because the whole point is
/// that it need not be axis-aligned.
public struct Quad: Sendable {
    public let topLeft: CGPoint
    public let topRight: CGPoint
    public let bottomLeft: CGPoint
    public let bottomRight: CGPoint

    public init(topLeft: CGPoint, topRight: CGPoint,
                bottomLeft: CGPoint, bottomRight: CGPoint) {
        self.topLeft = topLeft
        self.topRight = topRight
        self.bottomLeft = bottomLeft
        self.bottomRight = bottomRight
    }
}

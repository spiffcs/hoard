import CoreGraphics
import Foundation

public struct Line: Sendable {
    public let text: String
    public let box: CGRect
    public let confidence: Float
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

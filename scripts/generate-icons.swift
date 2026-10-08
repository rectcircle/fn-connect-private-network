// Deterministic AppKit artwork; run with: swift scripts/generate-icons.swift <output-directory>
import AppKit

let output = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
func png(_ size: Int, width: Int? = nil, draw: (CGFloat) -> Void) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: width ?? size, pixelsHigh: size,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    draw(CGFloat(size))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}
let blue = NSColor(srgbRed: 0, green: 0.4, blue: 1, alpha: 1)
let pale = NSColor(srgbRed: 224.0/255, green: 237.0/255, blue: 1, alpha: 1)
// Project-owned storage cylinder; no third-party logo artwork is used.
func storageLogo(_ color: NSColor) -> NSImage {
    let image = NSImage(size: NSSize(width: 256, height: 256))
    image.lockFocus()
    color.setStroke()
    let body = NSBezierPath()
    body.move(to: NSPoint(x: 42, y: 190))
    body.line(to: NSPoint(x: 42, y: 66))
    body.curve(to: NSPoint(x: 214, y: 66), controlPoint1: NSPoint(x: 42, y: 20), controlPoint2: NSPoint(x: 214, y: 20))
    body.line(to: NSPoint(x: 214, y: 190))
    body.lineWidth = 18
    body.lineCapStyle = .round
    body.stroke()
    let top = NSBezierPath(ovalIn: NSRect(x: 42, y: 158, width: 172, height: 64))
    top.lineWidth = 18
    top.stroke()
    let middle = NSBezierPath()
    middle.move(to: NSPoint(x: 42, y: 126))
    middle.curve(to: NSPoint(x: 214, y: 126), controlPoint1: NSPoint(x: 42, y: 80), controlPoint2: NSPoint(x: 214, y: 80))
    middle.lineWidth = 14
    middle.stroke()
    image.unlockFocus()
    return image
}
let blueLogo = storageLogo(blue)
let blackLogo = storageLogo(.black)
// Optical balance: center the LAN vertically and keep the external route compact.
func connectionMark(_ color: NSColor, drawEndpoint: Bool = true, strokeWidth: CGFloat = 5) {
    let boundary = NSBezierPath()
    boundary.move(to: NSPoint(x: 28, y: 62))
    boundary.line(to: NSPoint(x: 28, y: 69))
    boundary.curve(to: NSPoint(x: 36, y: 77), controlPoint1: NSPoint(x: 28, y: 74), controlPoint2: NSPoint(x: 31, y: 77))
    boundary.line(to: NSPoint(x: 74, y: 77))
    boundary.curve(to: NSPoint(x: 82, y: 69), controlPoint1: NSPoint(x: 79, y: 77), controlPoint2: NSPoint(x: 82, y: 74))
    boundary.line(to: NSPoint(x: 82, y: 31))
    boundary.curve(to: NSPoint(x: 74, y: 23), controlPoint1: NSPoint(x: 82, y: 26), controlPoint2: NSPoint(x: 79, y: 23))
    boundary.line(to: NSPoint(x: 36, y: 23))
    boundary.curve(to: NSPoint(x: 28, y: 31), controlPoint1: NSPoint(x: 31, y: 23), controlPoint2: NSPoint(x: 28, y: 26))
    boundary.line(to: NSPoint(x: 28, y: 38))
    boundary.lineWidth = strokeWidth; boundary.lineCapStyle = .round; boundary.stroke()
    // Short orthogonal route enters the middle of the left-side opening.
    let route = NSBezierPath()
    route.move(to: NSPoint(x: drawEndpoint ? 10 : 15, y: 35))
    route.line(to: NSPoint(x: drawEndpoint ? 13 : 15, y: 35))
    route.curve(to: NSPoint(x: 18, y: 40), controlPoint1: NSPoint(x: 16, y: 35), controlPoint2: NSPoint(x: 18, y: 37))
    route.line(to: NSPoint(x: 18, y: 45))
    route.curve(to: NSPoint(x: 23, y: 50), controlPoint1: NSPoint(x: 18, y: 48), controlPoint2: NSPoint(x: 20, y: 50))
    route.line(to: NSPoint(x: 37, y: 50))
    route.lineWidth = strokeWidth; route.lineCapStyle = .round; route.stroke()
    if drawEndpoint {
        NSBezierPath(roundedRect: NSRect(x: 5, y: 30, width: 10, height: 10), xRadius: 3, yRadius: 3).fill()
    }
    if strokeWidth > 5 {
        // Menu-bar artwork stays vector-based and shares the boundary stroke.
        let cylinder = NSBezierPath()
        cylinder.move(to: NSPoint(x: 43, y: 61))
        cylinder.line(to: NSPoint(x: 43, y: 38))
        cylinder.curve(to: NSPoint(x: 71, y: 38), controlPoint1: NSPoint(x: 43, y: 32), controlPoint2: NSPoint(x: 71, y: 32))
        cylinder.line(to: NSPoint(x: 71, y: 61))
        cylinder.lineWidth = strokeWidth
        cylinder.lineCapStyle = .round
        cylinder.stroke()
        let top = NSBezierPath(ovalIn: NSRect(x: 43, y: 57, width: 28, height: 8))
        top.lineWidth = strokeWidth
        top.stroke()
        let middle = NSBezierPath()
        middle.move(to: NSPoint(x: 43, y: 49))
        middle.curve(to: NSPoint(x: 71, y: 49), controlPoint1: NSPoint(x: 43, y: 43), controlPoint2: NSPoint(x: 71, y: 43))
        middle.lineWidth = strokeWidth
        middle.stroke()
    } else {
        let logo = color == blue ? blueLogo : blackLogo
        logo.draw(in: NSRect(x: 39, y: 33, width: 34, height: 34),
            from: NSRect(x: 24, y: 20, width: 208, height: 220), operation: .sourceOver, fraction: 1)
    }
}
func appIcon(_ s: CGFloat) {
    let transform = NSAffineTransform()
    transform.scale(by: s / 1024); transform.concat()
    // fnOS default icon palette: pale blue tile and vivid blue foreground.
    NSColor(srgbRed: 224.0/255, green: 237.0/255, blue: 1, alpha: 1).setFill()
    NSBezierPath(roundedRect: NSRect(x: 52, y: 52, width: 920, height: 920), xRadius: 208, yRadius: 208).fill()
    let mark = NSAffineTransform()
    mark.translateX(by: 88.8, yBy: 88.8); mark.scale(by: 8.464); mark.concat()
    blue.setStroke(); blue.setFill()
    connectionMark(blue)
}
let states = ["offline", "connected", "working", "attention"]
for state in states {
    for density in [1, 2] {
        let data = png(18 * density, width: 22 * density) { _ in
            let scale = NSAffineTransform(); scale.scale(by: CGFloat(density)); scale.concat()
            // A 22x18 pt canvas lets the LAN occupy 14.5 pt vertically,
            // with a 1.5 pt stroke, matching standard menu-bar symbol weight.
            NSColor.black.setStroke(); NSColor.black.setFill()
            NSGraphicsContext.saveGraphicsState()
            let mark = NSAffineTransform(); mark.translateX(by: 1.2, yBy: -3)
            mark.scale(by: 0.24); mark.concat()
            connectionMark(.black, drawEndpoint: false, strokeWidth: 6.25)
            NSGraphicsContext.restoreGraphicsState()
            // Replace the external endpoint with the state, leaving the LAN untouched.
            // The route starts at x=4.8, leaving the state symbol clear.
            let center = NSPoint(x: 2, y: 5.4)
            switch state {
            case "connected":
                NSBezierPath(ovalIn: NSRect(x: center.x-1.3, y: center.y-1.3, width: 2.6, height: 2.6)).fill()
            case "offline":
                let cross = NSBezierPath()
                cross.move(to: NSPoint(x: center.x-1.3, y: center.y-1.3))
                cross.line(to: NSPoint(x: center.x+1.3, y: center.y+1.3))
                cross.move(to: NSPoint(x: center.x-1.3, y: center.y+1.3))
                cross.line(to: NSPoint(x: center.x+1.3, y: center.y-1.3))
                cross.lineWidth = 1.2; cross.lineCapStyle = .round; cross.stroke()
            case "working":
                for offset in [CGFloat(-1.5), CGFloat(0), CGFloat(1.5)] {
                    NSBezierPath(ovalIn: NSRect(x: center.x+0.2+offset-0.65, y: center.y-0.65, width: 1.3, height: 1.3)).fill()
                }
            case "attention":
                NSBezierPath(roundedRect: NSRect(x: center.x-0.6, y: center.y-0.1, width: 1.2, height: 2.7), xRadius: 0.6, yRadius: 0.6).fill()
                NSBezierPath(ovalIn: NSRect(x: center.x-0.6, y: center.y-2.1, width: 1.2, height: 1.2)).fill()
            default: break
            }
        }
        let suffix = density == 2 ? "@2x" : ""
        try data.write(to: output.appendingPathComponent("Status-\(state)\(suffix).png"))
    }
}
// Package icons use 512 px: verified to fix app-center blur in 0.1.52.
// Desktop entry resources retain their actual named dimensions.
for (name, size) in [("ICON.PNG", 512), ("ICON_256.PNG", 512), ("DesktopIcon64.png", 64), ("DesktopIcon256.png", 256), ("AppIcon.png", 1024)] {
    try png(size, draw: appIcon).write(to: output.appendingPathComponent(name))
}
let iconset = output.appendingPathComponent("AppIcon.iconset")
try FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
for size in [16, 32, 128, 256, 512] {
    for density in [1, 2] {
        let suffix = density == 2 ? "@2x" : ""
        try png(size*density, draw: appIcon).write(to: iconset.appendingPathComponent("icon_\(size)x\(size)\(suffix).png"))
    }
}

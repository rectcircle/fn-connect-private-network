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
let iconSize: CGFloat = 1024
let markOrigin: CGFloat = 88.8
let markScale: CGFloat = 8.464
func iconBackground() -> NSBezierPath {
    NSBezierPath(roundedRect: NSRect(x: 52, y: 52, width: 920, height: 920), xRadius: 208, yRadius: 208)
}
let blue = NSColor(srgbRed: 0, green: 0.4, blue: 1, alpha: 1)
let pale = NSColor(srgbRed: 224.0/255, green: 237.0/255, blue: 1, alpha: 1)
// Capture the same filled path outlines for Android instead of redrawing the logo.
var captureMark = false
var markOutlines: [CGPath] = []
var storageOutlines: [CGPath] = []
func outline(_ path: NSBezierPath) -> CGPath {
    path.cgPath.copy(strokingWithWidth: path.lineWidth, lineCap: .round, lineJoin: .miter, miterLimit: 10)
}
func stroke(_ path: NSBezierPath) {
    path.stroke()
    if captureMark { markOutlines.append(outline(path)) }
}
func fill(_ path: NSBezierPath) {
    path.fill()
    if captureMark { markOutlines.append(path.cgPath) }
}
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
    stroke(body)
    let top = NSBezierPath(ovalIn: NSRect(x: 42, y: 158, width: 172, height: 64))
    top.lineWidth = 18
    stroke(top)
    let middle = NSBezierPath()
    middle.move(to: NSPoint(x: 42, y: 126))
    middle.curve(to: NSPoint(x: 214, y: 126), controlPoint1: NSPoint(x: 42, y: 80), controlPoint2: NSPoint(x: 214, y: 80))
    middle.lineWidth = 14
    stroke(middle)
    storageOutlines = [body, top, middle].map(outline)
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
    boundary.lineWidth = strokeWidth; boundary.lineCapStyle = .round; stroke(boundary)
    // Short orthogonal route enters the middle of the left-side opening.
    let route = NSBezierPath()
    route.move(to: NSPoint(x: drawEndpoint ? 10 : 15, y: 35))
    route.line(to: NSPoint(x: drawEndpoint ? 13 : 15, y: 35))
    route.curve(to: NSPoint(x: 18, y: 40), controlPoint1: NSPoint(x: 16, y: 35), controlPoint2: NSPoint(x: 18, y: 37))
    route.line(to: NSPoint(x: 18, y: 45))
    route.curve(to: NSPoint(x: 23, y: 50), controlPoint1: NSPoint(x: 18, y: 48), controlPoint2: NSPoint(x: 20, y: 50))
    route.line(to: NSPoint(x: 37, y: 50))
    route.lineWidth = strokeWidth; route.lineCapStyle = .round; stroke(route)
    if drawEndpoint {
        fill(NSBezierPath(roundedRect: NSRect(x: 5, y: 30, width: 10, height: 10), xRadius: 3, yRadius: 3))
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
        stroke(cylinder)
        let top = NSBezierPath(ovalIn: NSRect(x: 43, y: 57, width: 28, height: 8))
        top.lineWidth = strokeWidth
        stroke(top)
        let middle = NSBezierPath()
        middle.move(to: NSPoint(x: 43, y: 49))
        middle.curve(to: NSPoint(x: 71, y: 49), controlPoint1: NSPoint(x: 43, y: 43), controlPoint2: NSPoint(x: 71, y: 43))
        middle.lineWidth = strokeWidth
        stroke(middle)
    } else {
        let logo = color == blue ? blueLogo : blackLogo
        logo.draw(in: NSRect(x: 39, y: 33, width: 34, height: 34),
            from: NSRect(x: 24, y: 20, width: 208, height: 220), operation: .sourceOver, fraction: 1)
        if captureMark {
            var transform = CGAffineTransform(a: 34/208, b: 0, c: 0, d: 34/220, tx: 39-24*34/208, ty: 33-20*34/220)
            markOutlines += storageOutlines.compactMap { $0.copy(using: &transform) }
        }
    }
}
func appIcon(_ s: CGFloat) {
    let transform = NSAffineTransform()
    transform.scale(by: s / iconSize); transform.concat()
    // fnOS default icon palette: pale blue tile and vivid blue foreground.
    NSColor(srgbRed: 224.0/255, green: 237.0/255, blue: 1, alpha: 1).setFill()
    iconBackground().fill()
    let mark = NSAffineTransform()
    mark.translateX(by: markOrigin, yBy: markOrigin); mark.scale(by: markScale); mark.concat()
    blue.setStroke(); blue.setFill()
    connectionMark(blue)
}
func vectorData(_ path: CGPath) -> String {
    var commands: [String] = []
    func point(_ p: CGPoint) -> String { String(format: "%.5f,%.5f", locale: Locale(identifier: "en_US_POSIX"), Double(p.x), Double(p.y)) }
    path.applyWithBlock { element in
        let e = element.pointee
        switch e.type {
        case .moveToPoint: commands.append("M" + point(e.points[0]))
        case .addLineToPoint: commands.append("L" + point(e.points[0]))
        case .addQuadCurveToPoint: commands.append("Q" + point(e.points[0]) + " " + point(e.points[1]))
        case .addCurveToPoint: commands.append("C" + point(e.points[0]) + " " + point(e.points[1]) + " " + point(e.points[2]))
        case .closeSubpath: commands.append("Z")
        @unknown default: break
        }
    }
    return commands.joined(separator: " ")
}
// Export every platform from the same fixed 1024-unit composition as appIcon.
// Do not normalize by path bounds: that changes the logo's size and placement.
func brandPaths() -> [CGPath] {
    markOutlines = []
    _ = png(100) { _ in captureMark = true; connectionMark(blue); captureMark = false }
    var transform = CGAffineTransform(a: markScale, b: 0, c: 0, d: -markScale, tx: markOrigin, ty: iconSize-markOrigin)
    return markOutlines.map { $0.copy(using: &transform)! }
}
func svgIcon(_ paths: [CGPath]) -> String {
    let data = paths.map { "<path fill=\"#0066FF\" d=\"\(vectorData($0))\"/>" }.joined(separator: "\n")
    return """
    <svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
      <path fill="#E0EDFF" d="\(vectorData(iconBackground().cgPath))"/>
      \(data)
    </svg>
    """ + "\n"
}
func androidVector(_ paths: [CGPath], viewport: CGFloat, scale: CGFloat, offset: CGFloat, color: String) -> String {
    var transform = CGAffineTransform(a: scale, b: 0, c: 0, d: scale, tx: offset, ty: offset)
    let data = paths.map { "<path android:fillColor=\"\(color)\" android:pathData=\"\(vectorData($0.copy(using: &transform)!))\"/>" }.joined()
    return "<vector xmlns:android=\"http://schemas.android.com/apk/res/android\" android:width=\"\(Int(viewport))dp\" android:height=\"\(Int(viewport))dp\" android:viewportWidth=\"\(Int(viewport))\" android:viewportHeight=\"\(Int(viewport))\">\(data)</vector>\n"
}
let paths = brandPaths()
try svgIcon(paths).write(to: output.appendingPathComponent("AppIcon.svg"), atomically: true, encoding: .utf8)
// HarmonyOS launcher masks the full-bleed background itself. Keep UI artwork separate.
if let flag = CommandLine.arguments.firstIndex(of: "--harmonyos-root") {
    let root = URL(fileURLWithPath: CommandLine.arguments[flag+1])
    let background = png(1024) { size in
        pale.setFill()
        NSBezierPath(rect: NSRect(x: 0, y: 0, width: size, height: size)).fill()
    }
    let foreground = png(1024) { size in
        let context = NSGraphicsContext.current!.cgContext
        context.translateBy(x: 0, y: size)
        context.scaleBy(x: 1, y: -1)
        context.setFillColor(blue.cgColor)
        for path in paths { context.addPath(path); context.fillPath() }
    }
    for relative in ["AppScope/resources/base/media", "entry/src/main/resources/base/media"] {
        let media = root.appendingPathComponent(relative)
        try FileManager.default.createDirectory(at: media, withIntermediateDirectories: true)
        try background.write(to: media.appendingPathComponent("icon_background.png"))
        try foreground.write(to: media.appendingPathComponent("icon_foreground.png"))
        try """
        {"layered-image":{"background":"$media:icon_background","foreground":"$media:icon_foreground"}}
        """.appending("\n").write(to: media.appendingPathComponent("layered_image.json"), atomically: true, encoding: .utf8)
    }
}
if let flag = CommandLine.arguments.firstIndex(of: "--mobile-root") {
    let root = URL(fileURLWithPath: CommandLine.arguments[flag+1])
    let res = root.appendingPathComponent("android/app/src/main/res")
    let assets = root.appendingPathComponent("assets")
    try FileManager.default.createDirectory(at: assets, withIntermediateDirectories: true)
    try svgIcon(paths).write(to: assets.appendingPathComponent("appicon.svg"), atomically: true, encoding: .utf8)
    // Android's visible nominal 72-unit area corresponds to the whole canonical
    // canvas. The surrounding 18 units on each side are only for system masks.
    try androidVector(paths, viewport: 108, scale: 72/1024, offset: 18, color: "#0066FF")
        .write(to: res.appendingPathComponent("drawable/icon_foreground.xml"), atomically: true, encoding: .utf8)
    // Compact monochrome canvas; fixed composition, without launcher padding.
    try androidVector(paths, viewport: 24, scale: 28/1024, offset: -2, color: "#FFFFFFFF")
        .write(to: res.appendingPathComponent("drawable/tile_icon.xml"), atomically: true, encoding: .utf8)
    let nativeIcon = "<vector xmlns:android=\"http://schemas.android.com/apk/res/android\" android:width=\"32dp\" android:height=\"32dp\" android:viewportWidth=\"1024\" android:viewportHeight=\"1024\"><path android:fillColor=\"#E0EDFF\" android:pathData=\"\(vectorData(iconBackground().cgPath))\"/>\(paths.map { "<path android:fillColor=\"#0066FF\" android:pathData=\"\(vectorData($0))\"/>" }.joined())</vector>\n"
    try nativeIcon.write(to: res.appendingPathComponent("drawable/app_icon.xml"), atomically: true, encoding: .utf8)
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

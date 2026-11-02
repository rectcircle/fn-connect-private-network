#!/usr/bin/env python3
"""Render actual AppKit settings with fixtures; never contacts the installed daemon."""
import argparse
import pathlib
import plistlib
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--output', type=pathlib.Path, default=ROOT / 'dist/settings-preview')
args = parser.parse_args()
args.output.mkdir(parents=True, exist_ok=True)
source = (ROOT / 'apps/macos/FnCPNApp.swift').read_text(encoding='utf-8')
source = source.replace('@main\nprivate final class AppDelegate', 'private final class AppDelegate')
start = source.index('private final class LinkSettingsController')
insert = source.index('    required init?(coder:', start)
fixture = r'''
    func renderPreview(_ name: String, directory: String, width: CGFloat, dark: Bool = false) {
        NSApp.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window!.appearance = NSApp.appearance
        window!.setContentSize(NSSize(width: width, height: 720))
        fnID = "Home NAS"; currentPath = "fn-connect"
        links = [["link":"ipv6","enabled":true],["link":"fn-connect","enabled":true]]
        nas.stringValue = "NAS · Home NAS"; current.stringValue = "FN Connect"
        table.reloadData(); updateSelector()
        let isGeneral = name == "general"
        sidebar.selectRowIndexes(IndexSet(integer: isGeneral ? 0 : 1), byExtendingSelection: false)
        displayPage(isGeneral ? 0 : 1)
        if name.contains("upload") || name.contains("latency") {
            render(["active":true,"stage":name.contains("latency") ? "latency" : name.contains("confirming") ? "upload-confirming" : name.contains("waiting") ? "upload-waiting" : "upload","link":"fn-connect","mbps":86.4,"rttMs":34.2,"sequence":1,"echoSent":1,"echoReceived":1,"progress":0.45,"results":[["link":"fn-connect","latencyMs":34.2,"downloadMbps":123.2]]])
        } else if name.contains("complete") {
            render(["active":false,"stage":"completed","results":[["link":"ipv6","latencyMs":18.1,"downloadMbps":235.1,"uploadMbps":86.4],["link":"fn-connect","latencyMs":34.2,"downloadMbps":123.2,"uploadMbps":86.4]]])
        } else if name.contains("error") {
            render(["active":false,"stage":"completed","results":[["link":"ipv6","error":String(repeating:"IPv6 connection unavailable. ",count:12)],["link":"fn-connect","latencyMs":34.2,"downloadMbps":123.2,"error":"Upload interrupted. Retry to start a fresh HTTP request."]]])
        }
        if name.contains("expanded") { toggleErrorDetails() }
        window!.orderFront(nil)
        RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        if name == "links-idle" {
            func helpButtons(_ view: NSView) -> [HoverHelpButton] {
                (view as? HoverHelpButton).map { [$0] } ?? view.subviews.flatMap { helpButtons($0) }
            }
            let hints = helpButtons(page)
            precondition(hints.count == 1, "network test retained help icon")
            let event = NSEvent.otherEvent(with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil, subtype: 0, data1: 0, data2: 0)!
            hints[0].mouseEntered(with: event)
            precondition(hints[0].previewHelpShown, "hover help did not open")
            hints[0].mouseExited(with: event)
            precondition(!hints[0].previewHelpShown, "hover help did not close")
            selected = []; showLinkSelector()
            let choices = (selectorPopover!.contentViewController!.view.subviews.first as! NSStackView).arrangedSubviews.compactMap { $0 as? NSButton }
            let selectorFrame = selector.frame
            let startFrame = startButton.frame
            choices[0].performClick(nil)
            window!.contentView!.layoutSubtreeIfNeeded()
            precondition(selector.frame == selectorFrame && startButton.frame == startFrame, "selection moved controls")
            choices[1].performClick(nil)
            precondition(selected.count == 2 && selectorPopover!.isShown, "multi-select dismissed")
            choices[2].performClick(nil)
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
            precondition(!selectorPopover!.isShown, "Done failed")
            startTest()
            precondition(window!.attachedSheet != nil && cancellation == nil, "test started without confirmation")
            window!.endSheet(window!.attachedSheet!, returnCode: NSApplication.ModalResponse.alertSecondButtonReturn)
            RunLoop.current.run(until: Date().addingTimeInterval(0.1))
            precondition(cancellation == nil, "canceled confirmation started test")
        }
        let root = window!.contentView!
        root.layoutSubtreeIfNeeded()
        precondition(sidebar.convert(sidebar.bounds,to:root).width >= 110, "sidebar compressed")
        precondition(abs(pageDocument.frame.width - scroll.contentView.bounds.width) < 1, "document overflow")
        let content = isGeneral ? general : page
        precondition(content.frame.maxX <= pageDocument.frame.width - 20, "page overflow")
        if !resultArea.isHidden { precondition(results.frame.width <= resultArea.bounds.width + 1, "result table overflow") }
        let bitmap = root.bitmapImageRepForCachingDisplay(in:root.bounds)!
        root.cacheDisplay(in:root.bounds,to:bitmap)
        // AppKit's layer snapshots can contain transparency: compose onto the window background.
        let composed = NSBitmapImageRep(bitmapDataPlanes:nil,pixelsWide:bitmap.pixelsWide,pixelsHigh:bitmap.pixelsHigh,bitsPerSample:8,samplesPerPixel:4,hasAlpha:true,isPlanar:false,colorSpaceName:.deviceRGB,bytesPerRow:0,bitsPerPixel:0)!
        composed.size = bitmap.size
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep:composed)
        (dark ? NSColor(calibratedWhite:0.12,alpha:1) : NSColor.windowBackgroundColor).setFill()
        NSRect(origin:.zero,size:bitmap.size).fill()
        let image = NSImage(size:bitmap.size); image.addRepresentation(bitmap)
        image.draw(in:NSRect(origin:.zero,size:bitmap.size))
        NSGraphicsContext.restoreGraphicsState()
        try! composed.representation(using:.png,properties:[:])!.write(to:URL(fileURLWithPath:directory).appendingPathComponent(name+".png"))
        canvas.stopAnimation(); window!.orderOut(nil)
    }
'''
source = source[:insert] + fixture + source[insert:]
source = source.replace('private final class HoverHelpButton: SettingsButton {', 'private final class HoverHelpButton: SettingsButton {\n    var previewHelpShown: Bool { popover?.isShown == true }')
source += r'''
@main private enum SettingsPreview {
 static func main() {
  _ = NSApplication.shared
  let directory = CommandLine.arguments[1]
  let windows = (0..<3).map { _ in NSWindow(contentRect:NSRect(x:0,y:0,width:300,height:200),styleMask:[.titled,.closable,.miniaturizable],backing:.buffered,defer:false) }
  for window in windows { window.isReleasedWhenClosed = false; WindowPresence.shared.show(window) }
  precondition(NSApp.activationPolicy() == .regular)
  windows[0].close(); precondition(NSApp.activationPolicy() == .regular)
  windows[1].miniaturize(nil); windows[2].close(); WindowPresence.shared.update()
  precondition(NSApp.activationPolicy() == .regular, "minimized window lost Dock")
  windows[1].close(); precondition(NSApp.activationPolicy() == .accessory, "Dock remained after last close")
  for (name,width,dark) in [("general",840.0,false),("links-idle",840.0,false),("links-upload",780.0,false),("links-latency",840.0,false),("links-complete",840.0,false),("links-error",780.0,false),("links-error-expanded",780.0,false),("links-upload-dark",840.0,true),("links-upload-confirming",780.0,false),("links-upload-waiting",780.0,false)] {
   let settings = LinkSettingsController()
   settings.renderPreview(name,directory:directory,width:width,dark:dark)
  }
 }
}
'''
with tempfile.TemporaryDirectory(prefix='fncpn-settings-preview-') as temporary:
    path = pathlib.Path(temporary)
    (path / 'Preview.swift').write_text(source, encoding='utf-8')
    bundle = path / 'SettingsPreview.app/Contents'
    (bundle / 'MacOS').mkdir(parents=True)
    metadata = plistlib.loads((ROOT / 'apps/macos/packaging/Info.plist').read_bytes())
    metadata['CFBundleIdentifier'] = 'cn.rectcircle.fncpn.settings-preview'
    metadata['CFBundleExecutable'] = 'SettingsPreview'
    metadata['FnCPNRevision'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    metadata['CFBundleVersion'] = subprocess.check_output(['git', 'rev-list', '--count', 'HEAD'], cwd=ROOT, text=True).strip()
    (bundle / 'Info.plist').write_bytes(plistlib.dumps(metadata))
    binary = bundle / 'MacOS/SettingsPreview'
    subprocess.run(['swiftc', '-parse-as-library', str(path / 'Preview.swift'), '-o', str(binary),
                    '-framework', 'AppKit', '-framework', 'ServiceManagement', '-framework', 'UserNotifications',
                    '-framework', 'WebKit'], check=True)
    subprocess.run([str(binary), str(args.output.resolve())], check=True)
print(args.output.resolve())

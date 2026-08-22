// 生成 macOS 应用图标:白色方形底 + 居中 logo(macOS 会自动套用圆角遮罩)。
// 用法: icon-gen <logo.png> <输出iconset目录>

import AppKit

guard CommandLine.arguments.count >= 3 else {
    print("usage: icon-gen <logo.png> <output-iconset-dir>")
    exit(1)
}
let logoPath = CommandLine.arguments[1]
let outDir = CommandLine.arguments[2]
guard let logo = NSImage(contentsOfFile: logoPath), logo.size.width > 0 else {
    print("cannot read logo: \(logoPath)")
    exit(1)
}

let fm = FileManager.default
try? fm.createDirectory(atPath: outDir, withIntermediateDirectories: true)

func renderPNG(pointSize: CGFloat) -> Data {
    let pixels = Int(pointSize)
    guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels,
                                     bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                     colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else {
        exit(1)
    }
    rep.size = NSSize(width: pointSize, height: pointSize)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let bounds = NSRect(x: 0, y: 0, width: pointSize, height: pointSize)
    NSColor.white.setFill()
    bounds.fill()
    let target = pointSize * 0.72
    let scale = min(target / logo.size.width, target / logo.size.height)
    let drawWidth = logo.size.width * scale, drawHeight = logo.size.height * scale
    logo.draw(in: NSRect(x: (pointSize - drawWidth) / 2, y: (pointSize - drawHeight) / 2,
                         width: drawWidth, height: drawHeight))
    NSGraphicsContext.restoreGraphicsState()
    guard let data = rep.representation(using: .png, properties: [:]) else { exit(1) }
    return data
}

for size in [16, 32, 128, 256, 512] {
    for (label, points) in [("", Double(size)), ("@2x", Double(size) * 2)] {
        let path = "\(outDir)/icon_\(size)x\(size)\(label).png"
        try! renderPNG(pointSize: CGFloat(points)).write(to: URL(fileURLWithPath: path))
    }
}
print("iconset written to \(outDir)")

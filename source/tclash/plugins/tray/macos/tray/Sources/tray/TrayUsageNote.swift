import AppKit
import Darwin

final class TrayUsageNote {
    private let label = NSTextField(wrappingLabelWithString: "")
    private var pinned = false
    private var anchor: NSRect?
    private var hasUsage = false
    private var downloaded: [String: UInt64] = [:]
    private var measuredAt = ProcessInfo.processInfo.systemUptime
    private(set) var downloadLine = "↓ —"

    private lazy var panel: NSPanel = {
        let size = NSRect(x: 0, y: 0, width: 364, height: 166)
        let panel = NSPanel(contentRect: size, styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: true)
        panel.level = .floating
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.ignoresMouseEvents = true
        panel.hidesOnDeactivate = false
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        let view = NSView(frame: size)
        view.wantsLayer = true
        view.layer?.backgroundColor = NSColor.black.withAlphaComponent(0.4).cgColor
        view.layer?.cornerRadius = 14
        view.layer?.cornerCurve = .continuous
        label.textColor = .white
        label.font = .systemFont(ofSize: 13, weight: .medium)
        label.maximumNumberOfLines = 7
        label.lineBreakMode = .byTruncatingTail
        label.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 16),
            label.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -16),
            label.topAnchor.constraint(equalTo: view.topAnchor, constant: 13),
            label.bottomAnchor.constraint(lessThanOrEqualTo: view.bottomAnchor, constant: -13),
        ])
        panel.contentView = view
        return panel
    }()

    func update(_ lines: [String]) {
        hasUsage = !lines.isEmpty
        sampleDownload()
        label.stringValue = (lines + [downloadLine]).joined(separator: "\n")
    }

    func hover(_ entered: Bool, anchor: NSRect) {
        if entered {
            self.anchor = anchor
            show()
        } else if !pinned && hasUsage {
            panel.orderOut(nil)
        }
    }

    func pin(_ value: Bool, anchor: NSRect?) {
        pinned = value
        self.anchor = anchor ?? self.anchor
        if value { show() } else if hasUsage { panel.orderOut(nil) }
    }

    private func show() {
        guard hasUsage, let anchor,
              let screen = NSScreen.screens.first(where: { $0.frame.intersects(anchor) }) ?? NSScreen.main
        else { return }
        let available = screen.visibleFrame
        let x = min(max(available.minX, anchor.midX - panel.frame.width / 2), available.maxX - panel.frame.width)
        panel.setFrameTopLeftPoint(NSPoint(x: x, y: min(anchor.minY, available.maxY) - 6))
        panel.orderFrontRegardless()
    }

    private func sampleDownload() {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let head else { return }
        defer { freeifaddrs(head) }
        var counters: [String: UInt64] = [:]
        var item: UnsafeMutablePointer<ifaddrs>? = head
        while let current = item {
            let link = current.pointee
            item = link.ifa_next
            let name = String(cString: link.ifa_name)
            guard name.hasPrefix("en"), Int(name.dropFirst(2)) != nil,
                  link.ifa_addr?.pointee.sa_family == UInt8(AF_LINK),
                  let data = link.ifa_data?.assumingMemoryBound(to: if_data.self)
            else { continue }
            counters[name] = UInt64(data.pointee.ifi_ibytes)
        }
        let now = ProcessInfo.processInfo.systemUptime
        let elapsed = now - measuredAt
        let deltas = counters.compactMap { name, value in
            downloaded[name].flatMap { $0 <= value ? value - $0 : nil }
        }
        if !deltas.isEmpty && elapsed >= 0.25 {
            let rate = Int64(Double(deltas.reduce(0, +)) / elapsed)
            let bytes = ByteCountFormatter.string(fromByteCount: rate, countStyle: .binary)
            downloadLine = "↓ \(bytes)/s"
        }
        downloaded = counters
        measuredAt = now
    }
}

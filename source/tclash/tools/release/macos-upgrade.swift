import AppKit

let oldID = "local.tclash.preview"
let newID = "local.tclash.desktop"

func close(_ bundleID: String) -> Bool {
    let current = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID)
    current.forEach { _ = $0.terminate() }
    let deadline = Date().addingTimeInterval(16)
    while current.contains(where: { !$0.isTerminated }) && Date() < deadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
    }
    return current.allSatisfy(\.isTerminated)
}

switch CommandLine.arguments.dropFirst().first {
case "close":
    if !close(oldID) || !close(newID) { exit(2) }
case "reopen", "reopen-previous":
    let replacement = CommandLine.arguments[1] == "reopen"
    let target = URL(fileURLWithPath: replacement ? "/Applications/TClash.app" : "/Applications/TClash Preview.app")
    let open = NSWorkspace.OpenConfiguration()
    let deadline = Date().addingTimeInterval(15)
    var code: Int32?
    NSWorkspace.shared.openApplication(at: target, configuration: open) { app, error in
        code = error == nil && app != nil ? 0 : 3
    }
    while code == nil && Date() < deadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
    }
    exit(code ?? 3)
default:
    exit(1)
}

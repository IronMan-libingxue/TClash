import Foundation
import Darwin

struct Pair: Codable {
    let repo: String?
    let recipient: String?
}

struct Deployment: Codable {
    let name: String
    let token: String
    let pair: Pair
    let secret: Data
    let startFresh: Bool
}

struct Tracker: Codable {
    let role: String?
    let name: String?
    let repo: String?
    let token: String?
    let pair: Pair?
}

struct InstallError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

func fail(_ message: String) -> InstallError { InstallError(message: message) }

func readFile(_ path: URL, limit: Int) throws -> Data? {
    guard FileManager.default.fileExists(atPath: path.path) else { return nil }
    let attributes = try FileManager.default.attributesOfItem(atPath: path.path)
    guard let size = attributes[.size] as? NSNumber, size.intValue <= limit else {
        throw fail("现有设备信息无法核对，安装已停止。")
    }
    return try Data(contentsOf: path)
}

func tracker(_ path: URL) throws -> Tracker? {
    guard let data = try readFile(path, limit: 16 * 1024 * 1024) else { return nil }
    do { return try JSONDecoder().decode(Tracker.self, from: data) }
    catch { throw fail("现有设备信息无法核对，安装已停止。") }
}

func sameDevice(_ state: Tracker?, _ deployment: Deployment) throws -> Bool {
    guard let state = state else { return false }
    if state.role == "b" && (state.repo ?? "").isEmpty && (state.pair?.recipient ?? "").isEmpty {
        return false
    }
    if state.role == "b" && state.name == deployment.name && state.repo == deployment.pair.repo &&
        state.pair?.recipient == deployment.pair.recipient && state.token == deployment.token {
        return true
    }
    throw fail("此设备已有其他连接，为保护现有数据，安装已停止。")
}

struct Context {
    let deployment: Deployment
    let raw: Data
    let collector: URL
    let hasLegacyB: Bool
    let alreadyPaired: Bool
}

func inspect(enrollment: URL, home: URL) throws -> Context {
    guard let raw = try readFile(enrollment, limit: 16 * 1024),
          let deployment = try? JSONDecoder().decode(Deployment.self, from: raw),
          !deployment.name.isEmpty, !deployment.token.isEmpty,
          !(deployment.pair.repo ?? "").isEmpty, !(deployment.pair.recipient ?? "").isEmpty,
          deployment.secret.count == 32, deployment.startFresh else {
        throw fail("设备授权缺失或无效。")
    }
    let support = home.appendingPathComponent("Library/Application Support", isDirectory: true)
    let legacy = try tracker(support.appendingPathComponent("Codex Usage Tracker/tracker.json"))
    guard legacy?.role != "a" else { throw fail("本机已有 A 端，已停止安装以保护本机数据。") }
    let collector = support.appendingPathComponent("TClash Preview/collector", isDirectory: true)
    guard !FileManager.default.fileExists(atPath: collector.appendingPathComponent("use-legacy-tracker").path) else {
        throw fail("此设备正在使用旧连接，安装已停止以保护现有数据。")
    }
    let current = try tracker(collector.appendingPathComponent("tracker.json"))
    let paired = try sameDevice(current, deployment)
    if let pending = try readFile(collector.appendingPathComponent("install-enrollment.json"), limit: 16 * 1024) {
        let strip = CharacterSet.whitespacesAndNewlines
        guard let a = String(data: pending, encoding: .utf8), let b = String(data: raw, encoding: .utf8),
              a.trimmingCharacters(in: strip) == b.trimmingCharacters(in: strip) else {
            throw fail("已有待处理的设备授权，未覆盖原文件。")
        }
    }
    return Context(deployment: deployment, raw: raw, collector: collector,
                   hasLegacyB: legacy?.role == "b", alreadyPaired: paired)
}

@discardableResult
func command(_ path: String, _ args: [String], capture: Bool = false) throws -> (Int32, Data) {
    let p = Process()
    p.executableURL = URL(fileURLWithPath: path)
    p.arguments = args
    let out = Pipe()
    p.standardOutput = capture ? out : FileHandle.nullDevice
    p.standardError = FileHandle.nullDevice
    try p.run()
    let data = capture ? out.fileHandleForReading.readDataToEndOfFile() : Data()
    p.waitUntilExit()
    return (p.terminationStatus, data)
}

func dialog(_ message: String, retry: Bool) -> Bool {
    let script = """
    on run argv
      if item 2 of argv is "yes" then
        display dialog (item 1 of argv) with title "TClash 设备安装" buttons {"取消", "重试"} default button "重试" with icon caution
      else
        display dialog (item 1 of argv) with title "TClash 设备安装" buttons {"好"} default button "好" with icon caution
      end if
    end run
    """
    return (try? command("/usr/bin/osascript", ["-e", script, "--", message, retry ? "yes" : "no"]).0) == 0
}

func ensureStopped(_ hasLegacyB: Bool) throws {
    var checks = [("TClash Preview", ["-f", "/TClash Preview.app/Contents/MacOS/clash-verge"]),
                  ("tclash-collector", ["-x", "tclash-collector"])]
    if hasLegacyB { checks.append(("Codex Usage Tracker", ["-f", "/Codex 用量.app/Contents/MacOS/codex-usage"])) }
    for (name, args) in checks {
        while true {
            let result = try command("/usr/bin/pgrep", args).0
            if result == 1 { break }
            guard result == 0 else { throw fail("无法检查正在运行的程序，安装已停止。") }
            guard dialog("请先从菜单栏安全退出 \(name)，再点击重试。", retry: true) else {
                throw fail("安装已取消。")
            }
            Thread.sleep(forTimeInterval: 1)
        }
    }
}

func backupExistingApp(home: URL, target: URL) throws {
    let userApp = home.appendingPathComponent("Applications/TClash Preview.app")
    if FileManager.default.fileExists(atPath: userApp.path) {
        throw fail("此 Mac 的用户应用程序目录已有 TClash。为避免出现两个版本，请先联系协助处理已有程序。")
    }
    guard FileManager.default.fileExists(atPath: target.path) else { return }
    let attrs = try FileManager.default.attributesOfItem(atPath: target.path)
    guard attrs[.type] as? FileAttributeType == .typeDirectory,
          let bundle = Bundle(url: target), bundle.bundleIdentifier == "local.tclash.preview" else {
        throw fail("应用程序目录已有同名文件，安装已停止以保护现有文件。")
    }
    let backups = home.appendingPathComponent("Library/Application Support/TClash Preview/installer-backups", isDirectory: true)
    try FileManager.default.createDirectory(at: backups, withIntermediateDirectories: true,
                                            attributes: [.posixPermissions: 0o700])
    let backup = backups.appendingPathComponent("TClash Preview-\(UUID().uuidString).app")
    guard try command("/usr/bin/ditto", [target.path, backup.path]).0 == 0 else {
        throw fail("无法备份现有 TClash，安装已停止。")
    }
}

func stageEnrollment(_ context: Context) throws {
    if context.alreadyPaired { return }
    try FileManager.default.createDirectory(at: context.collector, withIntermediateDirectories: true,
                                            attributes: [.posixPermissions: 0o700])
    let pending = context.collector.appendingPathComponent("install-enrollment.json")
    let fd = open(pending.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, mode_t(0o600))
    if fd == -1 {
        if errno == EEXIST {
            let strip = CharacterSet.whitespacesAndNewlines
            if let current = try readFile(pending, limit: 16 * 1024),
               let a = String(data: current, encoding: .utf8), let b = String(data: context.raw, encoding: .utf8),
               a.trimmingCharacters(in: strip) == b.trimmingCharacters(in: strip) { return }
            throw fail("已有待处理的设备授权，未覆盖原文件。")
        }
        throw fail("无法保存设备授权。")
    }
    defer { close(fd) }
    do {
        try context.raw.withUnsafeBytes { bytes in
            var written = 0
            while written < bytes.count {
                let result = Darwin.write(fd, bytes.baseAddress!.advanced(by: written), bytes.count - written)
                if result <= 0 { throw fail("无法完整保存设备授权。") }
                written += result
            }
        }
    } catch {
        _ = unlink(pending.path)
        throw error
    }
}

func disableLegacyStartup(home: URL) throws {
    let path = home.appendingPathComponent("Library/LaunchAgents/local.codex-usage-tracker.collector.plist")
    guard let data = try readFile(path, limit: 1024 * 1024) else { return }
    guard let obj = try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
          obj["Label"] as? String == "local.codex-usage-tracker.collector",
          let args = obj["ProgramArguments"] as? [String], args.count >= 3,
          args[0].contains("/Codex 用量.app/Contents/MacOS/codex-usage"), args[1] == "-data-dir",
          args[2] == home.appendingPathComponent("Library/Application Support/Codex Usage Tracker").path else {
        throw fail("TClash 已启动，但旧采集程序的自动启动无法安全确认，请联系协助。")
    }
    let service = "gui/\(getuid())/local.codex-usage-tracker.collector"
    if try command("/bin/launchctl", ["print", service]).0 == 0 {
        guard try command("/bin/launchctl", ["bootout", service]).0 == 0 else {
            throw fail("TClash 已启动，但旧采集程序的自动启动未能安全关闭，请联系协助。")
        }
    }
    try FileManager.default.moveItem(at: path, to: URL(fileURLWithPath: path.path + ".tclash-backup-" + UUID().uuidString))
}

func activate(_ context: Context, home: URL, target: URL, start: (URL) throws -> Void,
              sleep: (TimeInterval) -> Void, disable: (URL) throws -> Void) throws {
    try stageEnrollment(context)
    try start(target)
    for _ in 0..<60 {
        sleep(0.5)
        if let state = try? tracker(context.collector.appendingPathComponent("tracker.json")),
           (try? sameDevice(state, context.deployment)) == true {
            if context.hasLegacyB { try disable(home) }
            return
        }
    }
    throw fail("TClash 已安装，但尚未确认设备授权加载，请检查 TClash 是否打开并联系协助。")
}

func preflight(enrollment: URL, home: URL, target: URL) throws {
    let context = try inspect(enrollment: enrollment, home: home)
    try ensureStopped(context.hasLegacyB)
    try backupExistingApp(home: home, target: target)
}

func postflight(enrollment: URL, home: URL, target: URL) throws {
    let context = try inspect(enrollment: enrollment, home: home)
    guard let bundle = Bundle(url: target), bundle.bundleIdentifier == "local.tclash.preview",
          try command("/usr/bin/codesign", ["--verify", "--strict", "--deep", target.path]).0 == 0 else {
        throw fail("安装后的 TClash 未通过校验，未加载设备授权。")
    }
    try activate(context, home: home, target: target, start: { app in
        guard try command("/usr/bin/open", ["-a", app.path]).0 == 0 else {
            throw fail("TClash 已安装，但无法自动启动。")
        }
    }, sleep: { Thread.sleep(forTimeInterval: $0) }, disable: disableLegacyStartup)
}

func runTests() throws {
    let root = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("tclash-pkg-test-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: root) }
    let home = root.appendingPathComponent("home")
    let enrollment = root.appendingPathComponent("enrollment.json")
    let example = Deployment(name: "Mac 3", token: "fixture-token", pair: Pair(repo: "fixture-repo", recipient: "fixture-recipient"),
                             secret: Data(repeating: 0, count: 32), startFresh: true)
    try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
    try JSONEncoder().encode(example).write(to: enrollment)
    let support = home.appendingPathComponent("Library/Application Support")
    let collector = support.appendingPathComponent("TClash Preview/collector")
    let local = collector.appendingPathComponent("tracker.json")
    let legacy = support.appendingPathComponent("Codex Usage Tracker/tracker.json")
    func check(_ condition: Bool, _ name: String) throws { if !condition { throw fail("测试失败：\(name)") } }
    func expectFailure(_ name: String) throws {
        do { _ = try inspect(enrollment: enrollment, home: home) }
        catch { return }
        throw fail("测试失败：\(name)")
    }
    func write(_ data: Data, _ path: URL) throws {
        try FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true)
        try data.write(to: path)
    }
    let context = try inspect(enrollment: enrollment, home: home)
    try check(!context.alreadyPaired, "fresh device")
    var started = false
    try activate(context, home: home, target: root.appendingPathComponent("fake.app"), start: { _ in
        started = true
        let d = try Data(contentsOf: collector.appendingPathComponent("install-enrollment.json"))
        let actual = try JSONDecoder().decode(Deployment.self, from: d)
        let state = Tracker(role: "b", name: actual.name, repo: actual.pair.repo, token: actual.token, pair: actual.pair)
        try JSONEncoder().encode(state).write(to: local)
        try FileManager.default.removeItem(at: collector.appendingPathComponent("install-enrollment.json"))
    }, sleep: { _ in }, disable: { _ in throw fail("unexpected legacy change") })
    try check(started, "automatic start")
    let before = try Data(contentsOf: local)
    let existing = try inspect(enrollment: enrollment, home: home)
    try check(existing.alreadyPaired, "same identity")
    try stageEnrollment(existing)
    try check(!FileManager.default.fileExists(atPath: collector.appendingPathComponent("install-enrollment.json").path), "no re-enrollment")
    try check(try Data(contentsOf: local) == before, "preserve existing data")
    try write(Data("{\"role\":\"a\"}".utf8), legacy)
    try expectFailure("reject A host")
    try FileManager.default.removeItem(at: legacy)
    try write(Data("{\"role\":\"b\",\"repo\":\"other\"}".utf8), local)
    try expectFailure("reject other identity")
    try write(before, local)
    try write(Data("{\"different\":true}".utf8), collector.appendingPathComponent("install-enrollment.json"))
    try expectFailure("reject other pending invitation")
    try FileManager.default.removeItem(at: collector.appendingPathComponent("install-enrollment.json"))
    try write(Data().advanced(by: 0), collector.appendingPathComponent("use-legacy-tracker"))
    try expectFailure("reject legacy marker")
    print("synthetic fresh activation, same-identity reinstall and conflict protections passed")
}

do {
    let args = CommandLine.arguments
    if args.count == 2 && args[1] == "--self-test" {
        try runTests()
    } else if args.count == 5 && ["--preflight", "--postflight"].contains(args[1]) {
        guard getuid() != 0, let pw = getpwuid(getuid()),
              String(cString: pw.pointee.pw_dir) == args[3] else {
            throw fail("无法确定当前 Mac 用户，安装已停止。")
        }
        let enrollment = URL(fileURLWithPath: args[2])
        let home = URL(fileURLWithPath: args[3], isDirectory: true)
        let target = URL(fileURLWithPath: args[4], isDirectory: true)
        if args[1] == "--preflight" { try preflight(enrollment: enrollment, home: home, target: target) }
        else { try postflight(enrollment: enrollment, home: home, target: target) }
    } else { throw fail("安装程序参数无效。") }
} catch {
    let message = (error as? LocalizedError)?.errorDescription ?? "安装过程中出现问题，未完成设备接入。"
    if CommandLine.arguments.count > 1 && CommandLine.arguments[1] == "--self-test" {
        fputs(message + "\n", stderr)
    } else { _ = dialog(message, retry: false) }
    exit(1)
}

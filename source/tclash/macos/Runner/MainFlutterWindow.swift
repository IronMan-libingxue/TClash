import Cocoa
import FlutterMacOS
import window
import LaunchAtLogin

class MainFlutterWindow: NSWindow, NSDraggingDestination {
    private var profileFiles: FlutterMethodChannel?

    override func awakeFromNib() {
        let flutterViewController = FlutterViewController()
        let windowFrame = self.frame
        self.contentViewController = flutterViewController
        self.setFrame(windowFrame, display: true)
        
        FlutterMethodChannel(
            name: "launch_at_startup", binaryMessenger: flutterViewController.engine.binaryMessenger
        )
        .setMethodCallHandler { (_ call: FlutterMethodCall, result: @escaping FlutterResult) in
            switch call.method {
            case "launchAtStartupIsEnabled":
                result(LaunchAtLogin.isEnabled)
            case "launchAtStartupSetEnabled":
                if let arguments = call.arguments as? [String: Any] {
                    LaunchAtLogin.isEnabled = arguments["setEnabledValue"] as! Bool
                }
                result(nil)
            default:
                result(FlutterMethodNotImplemented)
            }
        }
        
        RegisterGeneratedPlugins(registry: flutterViewController)
        profileFiles = FlutterMethodChannel(
            name: "tclash/profile_files",
            binaryMessenger: flutterViewController.engine.binaryMessenger
        )
        registerForDraggedTypes([.fileURL])
        super.awakeFromNib()
    }

    func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        .copy
    }

    func draggingUpdated(_ sender: NSDraggingInfo) -> NSDragOperation {
        .copy
    }

    func prepareForDragOperation(_ sender: NSDraggingInfo) -> Bool {
        true
    }

    func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard let channel = profileFiles,
              let files = sender.draggingPasteboard.readObjects(
                forClasses: [NSURL.self],
                options: [.urlReadingFileURLsOnly: true]
              ) as? [URL],
              !files.isEmpty else {
            return false
        }
        channel.invokeMethod("profilesDropped", arguments: files.map(\.path))
        return true
    }

    override public func order(_ place: NSWindow.OrderingMode, relativeTo otherWin: Int) {
        super.order(place, relativeTo: otherWin)
        hiddenWindowAtLaunch()
    }
}

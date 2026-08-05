import AppKit

// MARK: - App lifecycle

/// Supplies the Dock menu and routes every quit path through the controller.
///
/// Quitting must not go straight to `exit()`: hoard is waiting on stdout for a
/// `closed` event to know the camera window is gone. `CaptureController.shutdown`
/// emits that first, so closing from the Dock leaves the add session in exactly
/// the state it would be in after pressing esc.
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let controller: CaptureController

    init(controller: CaptureController) {
        self.controller = controller
    }

    /// Right-click (or click-and-hold) on the Dock icon.
    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        let menu = NSMenu()
        // A nil target sends the action up the responder chain to NSApp, so this
        // lands in applicationShouldTerminate below just like ⌘Q does.
        menu.addItem(withTitle: "Quit hoard scan",
                     action: #selector(NSApplication.terminate(_:)),
                     keyEquivalent: "")
        return menu
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        controller.shutdown() // emits "closed", stops the session, exits 0
        return .terminateNow
    }

    /// Closing the capture window should end the helper, not leave a menu-bar
    /// ghost with no way back to the camera.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}

/// Builds the minimal menu bar an activation-policy `.regular` app needs.
///
/// Without a main menu AppKit gives the app no ⌘Q at all, which is how the
/// helper ended up only closable from the terminal.
func installMainMenu() {
    let appMenu = NSMenu()
    appMenu.addItem(withTitle: "Quit hoard scan",
                    action: #selector(NSApplication.terminate(_:)),
                    keyEquivalent: "q")

    let appItem = NSMenuItem()
    appItem.submenu = appMenu

    let main = NSMenu()
    main.addItem(appItem)
    NSApp.mainMenu = main
}

import AppKit

MainActor.assumeIsolated {
    let application = NSApplication.shared
    ApplicationMenu.install()
    let delegate = AppDelegate()
    application.delegate = delegate
    application.setActivationPolicy(.accessory)
    application.run()
}

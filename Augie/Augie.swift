// Copyright 2021 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import AppKit
import SwiftUI
import Warden

struct LogView: View {
	@State var log: String
	var body: some View {
		TextEditor(text: $log).font(.system(size: 14, design: .monospaced))
	}
}

struct ProcMenuView: View {
	@ObservedObject var proc: Proc

	var body: some View {
		VStack(alignment: .leading, spacing: 4.0){
			HStack{
				Text(proc.name).fontWeight(.heavy)
				Spacer()
			}
			HStack{
				Text(proc.state())
				Spacer()
				Button("Log") {
					proc.showLog()
				}
			}
		}
		.padding(.horizontal)
	}
}

class Proc: ObservableObject {
	let name: String

	var logWindow: NSWindow?
	var menuItem: NSMenuItem?

	init(_ name: String) {
		self.name = name

		menuItem = NSMenuItem()
		menuItem?.view = NSHostingView(rootView: ProcMenuView(proc: self))
		menuItem?.view?.frame = NSRect(x: 0, y: 0, width: 200, height: 50)

		logWindow = NSWindow(
			contentRect: NSRect(x: 0, y: 0, width: 480, height: 300),
			styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
			backing: .buffered, defer: false)
		logWindow?.isReleasedWhenClosed = false
		logWindow?.center()
		logWindow?.setFrameAutosaveName("Log: \(name)")
		logWindow?.title = "Log: \(name)"
	}

	/// state returns the current state of this process.
	func state() -> String {
		var cs = name.cString(using: .utf8)!
		return withUnsafeMutablePointer(to: &cs[0]) { p -> String in
			if let cstring = wardenProcState(p) {
				defer {free(cstring)}
				return String(cString: cstring)
			}
			return "<unknown>"
		}
	}

	/// log returns the tail of the logs for this process.
	func log() -> String {
		if var cs = name.cString(using: .utf8) {
			return withUnsafeMutablePointer(to: &cs[0]) { p -> String in
				if let cstring = wardenProcLog(p) {
					defer {free(cstring)}
					return String(cString: cstring)
				}
				return "<unknown>"
			}
		}
		return "<unknown>"
	}

	/// update invalidates any views subscribed to object and forces them to redraw.
	func update() {
		self.objectWillChange.send()
	}

	func showLog() {
		logWindow?.contentView = NSHostingView(rootView: LogView(log: log()))
		logWindow?.makeKeyAndOrderFront(nil)
		NSApp.activate(ignoringOtherApps: true)
	}
}

@main
class AppDelegate: NSObject, NSApplicationDelegate {
	var procs: [Proc] = []
	var statusItem: NSStatusItem?

	func initMenu() -> NSMenu {
		let menu = NSMenu()

		// Add each process status item on the menu.
		for p in procs {
			if let item = p.menuItem {
				menu.addItem(item)
			}
		}

		menu.addItem(NSMenuItem.separator())

		// Quit button.
		let quitButton = NSMenuItem(title: "Quit Augie", action: #selector(NSApplication.shared.terminate), keyEquivalent: "")
		menu.addItem(quitButton)

		return menu
	}

	func applicationDidFinishLaunching(_ aNotification: Notification) {
		// Initialise the Go part and get list of configured commands.
		if let cmds = wardenInit() {
			procs = String(cString: cmds).split(separator: ",").map { Proc(String($0)) }
			free(cmds)
		}

		// Create a statusbar item and attach a menu to it.
		self.statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
		self.statusItem?.menu = initMenu()
		self.statusItem?.button?.title = "Augie"
		let augieImage = NSImage(imageLiteralResourceName: "augie-menubar")
		augieImage.isTemplate = true
		self.statusItem?.button?.image = augieImage

		// Poll for changes in a process status.
		// TODO register a callback in Go instead of polling.
		Timer.scheduledTimer(withTimeInterval: 2.0, repeats: true) { _ in
			for p in self.procs {
				p.update()
			}
		}
	}

	func applicationWillTerminate(_ aNotification: Notification) {
		// TODO signal all child process and wait for a grace period.
	}
}

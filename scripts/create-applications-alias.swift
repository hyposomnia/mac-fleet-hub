import AppKit
import Foundation

// A Finder alias carries its own Applications icon on a read-only disk image.
// Do not set an icon on a symbolic link: that can modify its /Applications target.
guard CommandLine.arguments.count == 2 else {
    fatalError("Usage: swift create-applications-alias.swift /tmp/staging/Applications")
}
let destination = URL(fileURLWithPath: CommandLine.arguments[1])
guard !FileManager.default.fileExists(atPath: destination.path) else {
    fatalError("Applications staging entry already exists")
}
let target = URL(fileURLWithPath: "/Applications", isDirectory: true)
let bookmark = try target.bookmarkData(options: .suitableForBookmarkFile,
                                       includingResourceValuesForKeys: nil, relativeTo: nil)
try URL.writeBookmarkData(bookmark, to: destination)
let icon = NSWorkspace.shared.icon(forFile: target.path)
guard NSWorkspace.shared.setIcon(icon, forFile: destination.path, options: []) else {
    fatalError("Unable to attach Applications icon")
}
let resolved = try URL(resolvingAliasFileAt: destination, options: [.withoutUI, .withoutMounting])
guard resolved.standardizedFileURL == target.standardizedFileURL else {
    fatalError("Applications alias resolves to an unexpected destination")
}
print("Applications alias and icon prepared")

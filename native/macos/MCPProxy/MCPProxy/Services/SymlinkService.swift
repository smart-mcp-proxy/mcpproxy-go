// SymlinkService.swift
// MCPProxy
//
// Manages the /usr/local/bin/mcpproxy symlink so that CLI users can
// invoke `mcpproxy` directly from their terminal without PATH manipulation.
//
// Creating or updating the symlink in /usr/local/bin/ typically requires
// elevated privileges. This service uses NSAppleScript to run a privileged
// ln command via osascript, which triggers a standard macOS auth dialog.

import Foundation

// MARK: - Symlink Errors

enum SymlinkError: Error, LocalizedError {
    case targetDirectoryMissing
    case privilegedOperationFailed(String)
    case binaryNotFound(String)

    var errorDescription: String? {
        switch self {
        case .targetDirectoryMissing:
            return "/usr/local/bin does not exist"
        case .privilegedOperationFailed(let detail):
            return "Privileged symlink operation failed: \(detail)"
        case .binaryNotFound(let path):
            return "Core binary not found at \(path)"
        }
    }
}

// MARK: - Symlink Service

/// Manages the `/usr/local/bin/mcpproxy` symlink pointing to the bundled binary.
struct SymlinkService {

    /// The well-known destination path for the CLI symlink.
    static let targetPath = "/usr/local/bin/mcpproxy"

    /// Check whether the symlink needs to be created or updated.
    ///
    /// Returns `true` if:
    /// - The symlink does not exist at `targetPath`, or
    /// - The symlink exists but points to a different binary than expected.
    static func needsSetup() -> Bool {
        let fm = FileManager.default

        // If the target doesn't exist at all, we need setup
        guard fm.fileExists(atPath: targetPath) else {
            return true
        }

        // If it exists but is not a symlink, don't touch it (user may have placed a real binary)
        guard let attrs = try? fm.attributesOfItem(atPath: targetPath),
              let fileType = attrs[.type] as? FileAttributeType,
              fileType == .typeSymbolicLink else {
            return false
        }

        // It's a symlink; check if it's valid (resolves to an executable)
        guard let destination = try? fm.destinationOfSymbolicLink(atPath: targetPath) else {
            return true // Broken symlink
        }

        return !fm.isExecutableFile(atPath: destination)
    }

    /// Create or update the symlink from `targetPath` to the given bundled binary.
    ///
    /// This operation requires elevated privileges and will present a macOS
    /// authorization dialog to the user.
    ///
    /// - Parameter bundledBinary: Absolute path to the mcpproxy binary inside the .app bundle.
    /// - Throws: `SymlinkError` on failure.
    static func createSymlink(from bundledBinary: String) async throws {
        let fm = FileManager.default

        // Validate the source binary exists
        guard fm.isExecutableFile(atPath: bundledBinary) else {
            throw SymlinkError.binaryNotFound(bundledBinary)
        }

        // Ensure /usr/local/bin exists
        let targetDir = (targetPath as NSString).deletingLastPathComponent
        guard fm.fileExists(atPath: targetDir) else {
            throw SymlinkError.targetDirectoryMissing
        }

        // Build the shell command to create/update the symlink.
        // We use `ln -sf` to atomically replace any existing symlink.
        let shellCommand = "ln -sf '\(bundledBinary)' '\(targetPath)'"

        // Use NSAppleScript to run with administrator privileges.
        // This triggers the standard macOS authorization prompt.
        let script = NSAppleScript(source: """
            do shell script "\(shellCommand)" with administrator privileges
            """)

        var errorDict: NSDictionary?
        let result = script?.executeAndReturnError(&errorDict)

        if result == nil {
            let errorMessage: String
            if let dict = errorDict,
               let message = dict[NSAppleScript.errorMessage] as? String {
                errorMessage = message
            } else {
                errorMessage = "Unknown error"
            }
            throw SymlinkError.privilegedOperationFailed(errorMessage)
        }
    }

    /// Silently attempt to update the symlink if the current one is broken or missing.
    /// Does not throw; errors are logged but not propagated.
    ///
    /// - Parameter bundledBinary: Absolute path to the mcpproxy binary.
    static func updateSymlinkIfNeeded(bundledBinary: String) async {
        guard needsSetup() else { return }

        // Only attempt without elevation if we already have write permission
        let fm = FileManager.default
        let targetDir = (targetPath as NSString).deletingLastPathComponent

        guard fm.isWritableFile(atPath: targetDir) else {
            // Cannot write without elevation; skip the silent update.
            // The user can manually set up the symlink via the CLI.
            return
        }

        // Try to create the symlink without elevation (e.g., if user owns /usr/local/bin)
        do {
            // Remove existing if it's a broken symlink
            if fm.fileExists(atPath: targetPath) {
                try fm.removeItem(atPath: targetPath)
            }
            try fm.createSymbolicLink(atPath: targetPath, withDestinationPath: bundledBinary)
        } catch {
            // Silent failure is acceptable here; the CLI still works via PATH
        }
    }
}

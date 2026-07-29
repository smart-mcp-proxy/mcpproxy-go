// FakeCoreBinary.swift
// MCPProxyTests/Support
//
// A stand-in for the `mcpproxy` binary the tray spawns.
//
// The spawn path — resolve binary, launch, wait for socket, relaunch on failure
// — cannot be tested with an external stub core: `maySpawn: false` never
// reaches it. But letting a test spawn the REAL core would put a second writer
// on the developer's BBolt database, which is the exact accident these tests
// exist to prevent. So the tests point MCPPROXY_CORE_PATH at a shell script
// that records each launch and then behaves as instructed.

import Foundation

/// A script that stands in for the core binary and counts its own launches.
struct FakeCoreBinary {

    /// Path to pass via `MCPPROXY_CORE_PATH`.
    let path: String

    /// File the script appends a line to on every launch.
    let launchLogPath: String

    private let directory: String

    /// - Parameter behaviour: shell run after the launch is recorded. Use
    ///   `exit 1` for a core that dies immediately, `sleep 30` for one that
    ///   stays up.
    init(behaviour: String) throws {
        let dir = "/tmp/mcpproxy-fakecore-\(UUID().uuidString.prefix(8))"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        directory = dir
        path = "\(dir)/mcpproxy"
        launchLogPath = "\(dir)/launches.log"

        let script = """
        #!/bin/sh
        echo launch >> "\(launchLogPath)"
        \(behaviour)
        """
        try script.write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: path)
    }

    /// A core that fails its first `failures` launches and then stays up.
    /// Used to prove a successful relaunch clears the retry ladder.
    static func failingThenHealthy(failures: Int) throws -> FakeCoreBinary {
        let counterName = "attempts"
        return try FakeCoreBinary(behaviour: """
        COUNT_FILE="$(dirname "$0")/\(counterName)"
        n=$(cat "$COUNT_FILE" 2>/dev/null || echo 0)
        n=$((n + 1))
        echo "$n" > "$COUNT_FILE"
        if [ "$n" -le \(failures) ]; then
          exit 1
        fi
        sleep 30
        """)
    }

    /// How many times the script has been launched.
    func launchCount() -> Int {
        guard let contents = try? String(contentsOfFile: launchLogPath, encoding: .utf8) else {
            return 0
        }
        return contents.split(separator: "\n").count
    }

    /// Point the tray's binary resolution at this script for the duration of a
    /// test. Returns a closure that restores the environment.
    func install() -> () -> Void {
        setenv("MCPPROXY_CORE_PATH", path, 1)
        let dir = directory
        return {
            unsetenv("MCPPROXY_CORE_PATH")
            try? FileManager.default.removeItem(atPath: dir)
        }
    }
}

// swift-tools-version: 5.9
import PackageDescription
let package = Package(
    name: "MCPProxyUITest",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "mcpproxy-ui-test", path: "Sources"),
    ]
)

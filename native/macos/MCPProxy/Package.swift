// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "MCPProxy",
    platforms: [.macOS(.v13)],
    dependencies: [
        .package(url: "https://github.com/sparkle-project/Sparkle", from: "2.6.0"),
    ],
    targets: [
        .executableTarget(
            name: "MCPProxy",
            dependencies: [
                .product(name: "Sparkle", package: "Sparkle"),
            ],
            path: "MCPProxy"
        ),
        .testTarget(
            name: "MCPProxyTests",
            dependencies: ["MCPProxy"],
            path: "MCPProxyTests"
        ),
    ]
)

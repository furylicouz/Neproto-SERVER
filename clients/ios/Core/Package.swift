// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "NeProtoCore",
    platforms: [.iOS(.v15), .macOS(.v13)],
    products: [
        .library(name: "NeProtoCore", targets: ["NeProtoCore"]),
    ],
    targets: [
        .target(name: "NeProtoCore"),
        .testTarget(name: "NeProtoCoreTests", dependencies: ["NeProtoCore"]),
    ]
)

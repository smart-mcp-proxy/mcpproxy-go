import XCTest
@testable import MCPProxy

/// The package has no SwiftUI interaction harness. Pin the two state branches
/// that make Added/Open failures actionable: errors outside a dismissed sheet,
/// and the server-authoritative name before the redacted-target fallback.
final class CatalogViewOpenFeedbackTests: XCTestCase {
    func testCatalogViewKeepsNavigationErrorVisibleAfterSheetDismissal() throws {
        let source = try catalogViewSource()
        XCTAssertTrue(source.contains("if let addError, pendingResult == nil"))
        XCTAssertTrue(source.contains("accessibilityIdentifier(\"catalog-add-error\")"))
    }

    func testCatalogViewUsesUniqueServerNameBeforeRedactedTargetFallback() throws {
        let source = try catalogViewSource()
        let authoritative = try XCTUnwrap(source.range(of: "if let name = result.addedServerName"))
        let fallback = try XCTUnwrap(source.range(of: "let target = catalogTarget(result.install)"))
        XCTAssertLessThan(authoritative.lowerBound, fallback.lowerBound)
    }

    private func catalogViewSource() throws -> String {
        let testDirectory = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        let source = testDirectory.deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/CatalogView.swift")
        return try String(contentsOf: source)
    }
}

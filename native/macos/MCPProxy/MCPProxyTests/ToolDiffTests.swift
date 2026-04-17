import XCTest
@testable import MCPProxy

final class ToolDiffTests: XCTestCase {

    private func partsByKind(_ parts: [ToolDiffPart]) -> (same: String, added: String, removed: String) {
        var same = ""
        var added = ""
        var removed = ""
        for p in parts {
            switch p.kind {
            case .same: same += p.text
            case .added: added += p.text
            case .removed: removed += p.text
            }
        }
        return (same, added, removed)
    }

    func testIdenticalStringsProduceOnlySameParts() {
        let text = "Get the list of Alibaba Cloud regions."
        let parts = computeWordDiff(text, text)
        let buckets = partsByKind(parts)
        XCTAssertEqual(buckets.same, text)
        XCTAssertEqual(buckets.added, "")
        XCTAssertEqual(buckets.removed, "")
    }

    func testEmptyBeforeMarksEverythingAdded() {
        let parts = computeWordDiff("", "Hello world")
        XCTAssertEqual(parts.count, 1)
        XCTAssertEqual(parts[0].kind, .added)
        XCTAssertEqual(parts[0].text, "Hello world")
    }

    func testEmptyAfterMarksEverythingRemoved() {
        let parts = computeWordDiff("Hello world", "")
        XCTAssertEqual(parts.count, 1)
        XCTAssertEqual(parts[0].kind, .removed)
        XCTAssertEqual(parts[0].text, "Hello world")
    }

    func testExpansionOfAlibabaRegions() {
        // Real case from gcore-mcp-server: short one-liner expanded to docstring.
        let before = "Get the list of Alibaba Cloud regions."
        let after = "Get the list of Alibaba Cloud regions. Args: limit: Maximum number of items."
        let parts = computeWordDiff(before, after)
        let buckets = partsByKind(parts)

        // The common prefix must survive intact in "same" parts.
        XCTAssertTrue(buckets.same.contains("Get the list of Alibaba Cloud regions."))
        // The trailing docstring must be in the "added" bucket.
        XCTAssertTrue(buckets.added.contains("Args:"))
        XCTAssertTrue(buckets.added.contains("limit:"))
        // Nothing should be removed in this direction.
        XCTAssertEqual(buckets.removed, "")
    }

    func testReconstructionFromAddedAndRemoved() {
        // Reconstructing before = same + removed; after = same + added (in order)
        let before = "alpha beta gamma"
        let after = "alpha delta gamma"
        let parts = computeWordDiff(before, after)

        let reconstructedBefore = parts.compactMap { p -> String? in
            p.kind == .added ? nil : p.text
        }.joined()
        let reconstructedAfter = parts.compactMap { p -> String? in
            p.kind == .removed ? nil : p.text
        }.joined()

        XCTAssertEqual(reconstructedBefore, before)
        XCTAssertEqual(reconstructedAfter, after)
    }

    func testConsecutivePartsOfSameKindAreMerged() {
        let parts = computeWordDiff("one two three", "one two three four five")
        // Trailing addition should be a single merged part, not many tokens.
        let addedParts = parts.filter { $0.kind == .added }
        XCTAssertEqual(addedParts.count, 1, "Consecutive added tokens must merge")
        XCTAssertTrue(addedParts[0].text.contains("four"))
        XCTAssertTrue(addedParts[0].text.contains("five"))
    }
}

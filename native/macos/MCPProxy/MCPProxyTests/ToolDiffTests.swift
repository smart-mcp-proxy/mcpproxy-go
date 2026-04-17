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

    func testCharLevelRefinementOnSingleCharChange() {
        // The real screenshot case: date changed from "1 April" to "8 April".
        // Word-level alone would highlight the "1" and "8" tokens. Char-level
        // refinement produces the same result here (single-char tokens), but
        // it also handles the common "gs_1234" → "gs_1235" style where the
        // differing chars are in the MIDDLE of a word.
        let before = "Knowledge up-to-date as at 1 April 2026."
        let after = "Knowledge up-to-date as at 8 April 2026."
        let parts = computeWordDiff(before, after)

        let addedParts = parts.filter { $0.kind == .added }
        let removedParts = parts.filter { $0.kind == .removed }

        // Exactly one char each on each side — nothing else touched.
        XCTAssertEqual(addedParts.map { $0.text }.joined(), "8")
        XCTAssertEqual(removedParts.map { $0.text }.joined(), "1")

        // Reconstruction still matches.
        let recBefore = parts.compactMap { $0.kind == .added ? nil : $0.text }.joined()
        let recAfter = parts.compactMap { $0.kind == .removed ? nil : $0.text }.joined()
        XCTAssertEqual(recBefore, before)
        XCTAssertEqual(recAfter, after)
    }

    func testCharLevelRefinementInsideWord() {
        // Differing chars inside a single token — word-level alone would
        // highlight the whole token ("version-1.2.3" vs "version-1.2.4"),
        // char-level should narrow to just "3" → "4".
        let before = "server version-1.2.3 release"
        let after = "server version-1.2.4 release"
        let parts = computeWordDiff(before, after)

        let removed = parts.filter { $0.kind == .removed }.map { $0.text }.joined()
        let added = parts.filter { $0.kind == .added }.map { $0.text }.joined()
        XCTAssertEqual(removed, "3")
        XCTAssertEqual(added, "4")

        let recBefore = parts.compactMap { $0.kind == .added ? nil : $0.text }.joined()
        let recAfter = parts.compactMap { $0.kind == .removed ? nil : $0.text }.joined()
        XCTAssertEqual(recBefore, before)
        XCTAssertEqual(recAfter, after)
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

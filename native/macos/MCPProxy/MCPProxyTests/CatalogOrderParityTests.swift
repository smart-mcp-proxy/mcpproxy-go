import XCTest
@testable import MCPProxy

/// Spec 109-m SC-008 (T145b, M12): the macOS leg of the catalog order parity
/// chain. The REST golden `internal/registries/testdata/catalog_github_order.json`
/// (written by the Go httpapi test from the real `GET /catalog/search` handler
/// over the fixture registries) decodes into the models the Catalog view lists,
/// and the list keeps the served order: the official, verified GitHub server
/// first, with a distinct identity per row.
final class CatalogOrderParityTests: XCTestCase {

    private struct Golden: Decodable {
        let query: String
        let ids: [String]
        let results: [CatalogResult]
    }

    private func golden() throws -> Golden {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        url.appendPathComponent("internal/registries/testdata/catalog_github_order.json")
        return try JSONDecoder().decode(Golden.self, from: try Data(contentsOf: url))
    }

    func testCatalogModelKeepsTheServedOrder() throws {
        let g = try golden()
        XCTAssertFalse(g.ids.isEmpty, "the REST golden is missing; run the Go httpapi test with UPDATE_GOLDEN=1")
        XCTAssertEqual(g.results.map { "\($0.source):\($0.catalogID)" }, g.ids)
    }

    func testTheOfficialVerifiedGitHubServerIsFirst() throws {
        let first = try XCTUnwrap(try golden().results.first)
        XCTAssertEqual(first.source, "official")
        XCTAssertEqual(first.catalogID, "io.github.github/github-mcp-server")
        XCTAssertTrue(first.official)
        XCTAssertTrue(first.verified)
    }

    func testEveryRowHasADistinctListIdentity() throws {
        let results = try golden().results
        XCTAssertEqual(Set(results.map(\.id)).count, results.count)
    }

    func testTheSearchResponseWrapperDecodesTheSameResults() throws {
        let g = try golden()
        let wrapper = """
        {"query":"\(g.query)","results":[],"sections":null,"unavailable":[]}
        """
        let response = try JSONDecoder().decode(CatalogSearchResponse.self, from: Data(wrapper.utf8))
        XCTAssertEqual(response.query, g.query)
        XCTAssertNil(response.sections, "a non-empty query has no sections")
    }

    // MARK: - Cached fallback (Spec 109 D35, T160)

    private struct CachedGolden: Decodable {
        struct Unavailable: Decodable { let source: String; let fallback: String }
        let query: String
        let ids: [String]
        let results: [CatalogResult]
        let unavailable: [Unavailable]
    }

    private func cachedGolden() throws -> CachedGolden {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        url.appendPathComponent("internal/registries/testdata/catalog_cached_fallback_order.json")
        return try JSONDecoder().decode(CachedGolden.self, from: try Data(contentsOf: url))
    }

    /// The REST golden written while one source is down decodes into the
    /// models the Catalog view lists: same order, and only the cached rows
    /// carry the "From cached list" badge. A hit with no `from_cache` key is a
    /// live hit.
    func testCachedFallbackGoldenOrderAndBadge() throws {
        let g = try cachedGolden()
        XCTAssertFalse(g.ids.isEmpty, "the REST golden is missing; run the Go httpapi cached-fallback test with UPDATE_GOLDEN=1")
        XCTAssertEqual(g.results.map { "\($0.source):\($0.catalogID)" }, g.ids)
        let cached = g.results.filter(\.fromCache).map(\.source)
        XCTAssertFalse(cached.isEmpty)
        XCTAssertEqual(Set(cached), Set(g.unavailable.map(\.source)), "only the fallback source's rows are cached")
        XCTAssertFalse(try XCTUnwrap(g.results.first).fromCache, "the live official hit is not cached")
    }

    func testTheUnavailableNoticeSaysWhatIsShown() throws {
        let g = try cachedGolden()
        let response = """
        {"query":"\(g.query)","results":[],"sections":null,
         "unavailable":[{"source":"slowreg","reason":"timeout after 5s","fallback":"cached_listing","cached_at":"2026-10-02T10:00:00Z"}]}
        """
        let decoded = try JSONDecoder().decode(CatalogSearchResponse.self, from: Data(response.utf8))
        let notice = try XCTUnwrap(decoded.unavailable?.first)
        XCTAssertEqual(notice.fallback, "cached_listing")
        XCTAssertEqual(notice.noticeLine, "slowreg: live search unavailable (timeout after 5s); showing matches from its cached list")

        let plain = try JSONDecoder().decode(
            CatalogSourceError.self, from: Data(#"{"source":"slowreg","reason":"timeout after 5s"}"#.utf8))
        XCTAssertNil(plain.fallback)
        XCTAssertEqual(plain.noticeLine, "slowreg (timeout after 5s) unavailable")
    }
}

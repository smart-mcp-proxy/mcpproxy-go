import XCTest
import AppKit
@testable import MCPProxy

/// Regression tests for issue #538 — "Editor autocorrect creates broken
/// configs". macOS smart-dash substitution turns "--" into "—" in the tray's
/// Add Server / config text fields. `TextSubstitution.disableAutomatic...`
/// must turn every automatic substitution off so a freshly created NSTextView
/// field editor (the type SwiftUI TextField/TextEditor use on macOS) has dash
/// substitution disabled.
final class TextSubstitutionTests: XCTestCase {

    /// After calling the helper, the substitution defaults are all false.
    @MainActor
    func test_disableAutomaticTextSubstitutions_setsAllDefaultsFalse() {
        let suiteName = "TextSubstitutionTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        defer { defaults.removePersistentDomain(forName: suiteName) }

        // Pre-condition: simulate a machine where the user has smart dashes ON.
        for key in TextSubstitution.substitutionDefaultsKeys {
            defaults.set(true, forKey: key)
        }

        TextSubstitution.disableAutomaticTextSubstitutions(defaults: defaults)

        for key in TextSubstitution.substitutionDefaultsKeys {
            XCTAssertFalse(
                defaults.bool(forKey: key),
                "\(key) must be disabled after disableAutomaticTextSubstitutions() (issue #538)"
            )
        }
    }

    /// The dash-substitution key (the one that produces the em-dash bug) is
    /// among the keys the helper disables.
    func test_substitutionKeys_includeDashSubstitution() {
        XCTAssertTrue(
            TextSubstitution.substitutionDefaultsKeys.contains("NSAutomaticDashSubstitutionEnabled"),
            "Dash substitution is the key that causes '--' → '—' (issue #538)"
        )
    }

    /// End-to-end mechanism check: with the substitution defaults written to
    /// the STANDARD domain (what the app does at launch) BEFORE the field
    /// editor exists, a freshly created NSTextView reports dash substitution
    /// disabled. This is the property AppKit consults before rewriting "--".
    @MainActor
    func test_freshNSTextView_hasDashSubstitutionDisabled_afterHelper() {
        // Capture and restore the real standard-domain values so the test is
        // side-effect free regardless of the host machine's settings.
        let keys = TextSubstitution.substitutionDefaultsKeys
        let saved = keys.map { ($0, UserDefaults.standard.object(forKey: $0)) }
        defer {
            for (key, value) in saved {
                if let value { UserDefaults.standard.set(value, forKey: key) }
                else { UserDefaults.standard.removeObject(forKey: key) }
            }
        }

        // Model the buggy condition, then apply the fix to the standard domain.
        UserDefaults.standard.set(true, forKey: "NSAutomaticDashSubstitutionEnabled")
        TextSubstitution.disableAutomaticTextSubstitutions()

        // A field editor created now must inherit the disabled state.
        let textView = NSTextView(frame: .zero)
        XCTAssertFalse(
            textView.isAutomaticDashSubstitutionEnabled,
            "A fresh NSTextView must have smart-dash substitution OFF so '--' is preserved (issue #538)"
        )
    }
}

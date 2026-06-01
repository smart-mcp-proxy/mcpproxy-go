// TextSubstitution.swift
// MCPProxy
//
// Disables macOS automatic text substitutions (smart dashes, smart quotes,
// text replacement, spelling autocorrection, autocapitalization) for the whole
// app. See issue #538: smart-dash substitution rewrites "--" as an em-dash
// "—", silently corrupting CLI flags typed into server Command/Arguments/Env
// fields (e.g. "--flag" → "—flag"), producing broken configs.
//
// None of this app's text fields hold prose — they hold commands, flags,
// paths, URLs, KEY=VALUE pairs, names, and tokens — so every automatic
// substitution is unwanted everywhere. SwiftUI exposes no modifier for smart
// dashes (`.autocorrectionDisabled()` does not affect dash substitution), so
// the reliable lever is the AppKit defaults the field editor (NSTextView)
// reads at creation time.

import AppKit

enum TextSubstitution {
    /// The automatic-substitution default keys NSTextView consults when a field
    /// editor is created. Writing them to the standard (application) defaults
    /// domain overrides the user's system-wide NSGlobalDomain setting for this
    /// app only.
    static let substitutionDefaultsKeys = [
        "NSAutomaticDashSubstitutionEnabled",
        "NSAutomaticQuoteSubstitutionEnabled",
        "NSAutomaticTextReplacementEnabled",
        "NSAutomaticSpellingCorrectionEnabled",
        "NSAutomaticCapitalizationEnabled",
    ]

    /// Disable all automatic text substitutions app-wide. MUST be called before
    /// any window / text field editor is created (e.g. from
    /// `applicationWillFinishLaunching`) so every NSTextView inherits the
    /// disabled state.
    static func disableAutomaticTextSubstitutions(
        defaults: UserDefaults = .standard
    ) {
        for key in substitutionDefaultsKeys {
            defaults.set(false, forKey: key)
        }
    }
}

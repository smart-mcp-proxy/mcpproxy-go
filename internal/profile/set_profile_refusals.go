package profile

// The MCP set_profile refusal texts a Spec 108 client credential receives
// (contracts/refusals.md; golden testdata/contract/set_profile_refusals.json).
//
// They depend only on the caller's own credential (its kind and profile mode),
// never on whether the requested slug exists or is in the binding's
// switchable_to, so for one caller every refused slug returns byte-identical
// text with the slug substituted (the uniform-refusal property). Neither names
// the bound profile. Every other caller (agent tokens, anonymous, confined
// anonymous, URL-scoped) keeps the Spec 105 "unknown profile '<slug>'" text.
const (
	// SetProfileLockedRefusalFormat is the refusal for a locked client credential.
	SetProfileLockedRefusalFormat = "cannot switch to profile '%s': this client's profile is locked"
	// SetProfileNotSwitchableRefusalFormat is the refusal for a switchable
	// client credential asking for a profile it may not switch to.
	SetProfileNotSwitchableRefusalFormat = "cannot switch to profile '%s': it is not a profile this client may switch to"
)

package oauth

import (
	"encoding/json"
	"reflect"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1148, round 9. The WHOLE-CONFIG door and its write twin.
//
// `GET /api/v1/config` serves the entire config.Config: every server's env,
// headers, oauth.client_secret and url credentials, the global
// docker_isolation.extra_args, and the api_key. It is the widest read door on
// the tree and it had no rule at all — the round-8 inventory could not see it,
// because the leak's shape is "a whole config handed to a publish sink", not
// "a leaf copied into a map".
//
// Masking it is only half an answer. The raw-JSON editor and the onboarding
// wizard both GET this document, change one field, and POST the whole thing
// back to `/api/v1/config/apply`; the macOS tray and the Settings page PATCH
// it. Masking the read without a matching revert on the write would persist
// the MASK over the credential — the #1142 corruption this branch has spent
// four rounds preventing. So the two functions below are written as one pair
// and neither is useful alone.

// RedactedConfig returns a masked deep copy of a whole config, using the same
// generic LIVE walk every other door masks a nested server block with.
//
// Walking rather than enumerating is the durable half, for exactly the reason
// round 7 gave: a field added to config.Config later is masked because the walk
// reaches it, instead of being published in the clear until somebody remembers.
//
// Returns nil when the walk cannot round-trip. A caller must treat that as a
// failure and serve an error: publishing the unmasked config instead is the
// fail-open shape this whole issue is made of.
func RedactedConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	masked := *cfg
	if !LiveRedaction.RedactNested("", &masked) {
		return nil
	}
	return &masked
}

// UnmaskLiveConfigTree is the write twin of RedactedConfig: it reverts every
// mask a live read door rendered that can be BOUND TO A KEY, and refuses
// whatever is left.
//
// `incoming` is what the client sent — a whole config document on
// `/config/apply`, a partial one on `PATCH /config` — and `stored` is what the
// proxy holds. Both are walked as generic JSON, so a PARTIAL tree works: the
// bindings are per key at each level, not positional.
//
// The bindings are exactly the ones ServerFieldMaskDecisions already records,
// applied one level up:
//
//   - a MAP key (`env`, `headers`, `oauth.extra_params`) and a named struct
//     field (`url`, `command`, `api_key`, `oauth.client_secret`) bind, because
//     the caller cannot restate the key without also restating what the secret
//     is for.
//   - the `mcpServers` array binds BY NAME, never by index: a caller who
//     renames a server has not told the proxy which stored secret they meant.
//   - an argv / scopes / extra_args slot binds to NOTHING — an index is not a
//     binding — so its mask survives the walk and CheckServerWriteMasks refuses
//     the write, which is the `refuse` decision the table has recorded since
//     round 6.
//   - and one binding wider than all of them: a SUBTREE echoed back byte for
//     byte as the read door rendered it is proof the caller changed nothing in
//     it, so the stored subtree is restored wholesale. That is the same
//     binding UnmaskLiveURL has used since round 4 for a whole URL, one level
//     up — and it is what keeps the raw-JSON editor and the onboarding wizard
//     working: they GET the document, change one top-level field and POST it
//     back, so every server block is byte-identical and none of them has to
//     bind leaf by leaf. Change anything inside a block and the equality
//     fails, the walk narrows to per-key bindings, and an argv mask under an
//     edited server is refused exactly as it should be.
//
// Anything the walk did not revert and still carries a mask this proxy rendered
// fails the write CLOSED, including a field added to config.Config later.
func UnmaskLiveConfigTree(incoming, stored interface{}) (interface{}, error) {
	reverted := unmaskConfigNode("", NormalizeForRedaction(incoming), NormalizeForRedaction(stored))
	if err := CheckServerWriteMasks("config", reverted); err != nil {
		return nil, err
	}
	return reverted, nil
}

// unmaskConfigNode mirrors Redaction.Value one node at a time: whatever the
// read walk rendered for a leaf under this key is what the write walk compares
// against. The two must stay in step, which is why both are keyed the same way
// and neither carries its own field list.
func unmaskConfigNode(key string, incoming, stored interface{}) interface{} {
	// The widest binding first: a subtree the caller echoed back exactly as the
	// read door rendered it is unchanged, so the stored one goes back verbatim.
	// Equality with mask(stored) is proof — the same proof UnmaskLiveURL takes
	// for a whole URL, one level up.
	//
	// It is NOT offered to an argv vector on its own. `key` there is `args` /
	// `extra_args`, and a vector standing alone under an edited block is
	// exactly what CheckArgvMaskEcho refuses at the server write doors: the
	// caller supplies the vector and `command` together, so an echoed token
	// proves nothing about the slot it came from. Reached as part of a whole
	// BLOCK echo it is restored, because there the caller restated the entire
	// server — name, command, every argument — byte for byte, which is a
	// strictly stronger binding than the one that check rejects. The two doors
	// need that difference: `PATCH /api/v1/servers/{id}` lets a caller OMIT
	// `args` to leave it alone, and a whole-document `POST /config/apply` has
	// no such escape — omitting the field there deletes it.
	if stored != nil && incoming != nil && !IsArgvFieldName(key) {
		if reflect.DeepEqual(incoming, LiveRedaction.Value(key, stored)) {
			return stored
		}
	}
	switch in := incoming.(type) {
	case map[string]interface{}:
		storedMap, _ := stored.(map[string]interface{})
		out := make(map[string]interface{}, len(in))
		isHeaders := IsHeadersFieldName(key)
		isEnv := IsEnvFieldName(key)
		for k, v := range in {
			var sv interface{}
			if storedMap != nil {
				sv = storedMap[k]
			}
			if s, ok := v.(string); ok {
				out[k] = revertLeaf(k, s, sv, isHeaders, isEnv)
				continue
			}
			out[k] = unmaskConfigNode(k, v, sv)
		}
		return out
	case []interface{}:
		// An argv vector offers only an INDEX to bind to, which is no binding:
		// the caller supplies the whole vector AND `command` in one request, so
		// a restored token could be moved to a slot it was never read from.
		// Leave it; the residual net refuses it.
		if IsArgvFieldName(key) {
			return in
		}
		byName := map[string]interface{}{}
		if storedSlice, ok := stored.([]interface{}); ok {
			for _, el := range storedSlice {
				m, ok := el.(map[string]interface{})
				if !ok {
					continue
				}
				if name, ok := m["name"].(string); ok && name != "" {
					byName[name] = m
				}
			}
		}
		out := make([]interface{}, len(in))
		for i, el := range in {
			m, ok := el.(map[string]interface{})
			if !ok {
				// A plain string element (oauth.scopes, docker_isolation.
				// extra_args): nothing but its index identifies it. Refuse.
				out[i] = el
				continue
			}
			name, _ := m["name"].(string)
			out[i] = unmaskConfigNode(key, m, byName[name])
		}
		return out
	default:
		return incoming
	}
}

// revertLeaf restores one string leaf echoed back exactly as the live read walk
// rendered it, bound to its own key.
func revertLeaf(key, incoming string, stored interface{}, isHeaders, isEnv bool) string {
	storedStr, ok := stored.(string)
	if !ok || storedStr == "" || incoming == "" {
		return incoming
	}
	var rendered string
	switch {
	case isHeaders:
		rendered = LiveRedaction.HeaderValue(key, storedStr)
	case isEnv:
		rendered = LiveRedaction.EnvValue(key, storedStr)
	default:
		rendered = LiveRedaction.Leaf(key, storedStr)
	}
	if incoming == rendered {
		return storedStr
	}
	return incoming
}

// UnmaskLiveConfigDocument is UnmaskLiveConfigTree for a whole config document
// a client PUT/POSTed: it resolves the masks against `stored` and decodes the
// result into a fresh config.Config.
//
// Decoding into a FRESH value rather than over an existing one is the same rule
// RedactNested follows: encoding/json reuses an existing slice's backing array,
// so decoding over a value that still aliases the stored config would overwrite
// live credentials with their own masks.
func UnmaskLiveConfigDocument(incoming map[string]interface{}, stored *config.Config) (*config.Config, error) {
	resolved, err := UnmaskLiveConfigTree(incoming, stored)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	fresh := &config.Config{}
	if err := json.Unmarshal(encoded, fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

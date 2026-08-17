// Package preflight is the shared eligibility evaluator behind the
// required-tools preflight (Spec 098): given a caller-supplied tool ID and a
// caller context, it answers `ready` or exactly one failure reason from a
// closed 15-code enum, by a fixed precedence chain.
//
// Two invariants define this package and must survive every future change:
//
//  1. ZERO UPSTREAM I/O, ZERO RUNTIME MUTATION (FR-006). The evaluator reads
//     only the narrow interfaces in evaluator.go — an index reader, an approval
//     reader, a connection-state reader and a config-policy reader. It cannot
//     reach a transport, an upstream client, a reconnect path or an index
//     writer, because none of those types are reachable from here: the package
//     imports no transport, no runtime and no upstream package. In particular a
//     preflight must NEVER call index ForProfile (it lazily creates and caches
//     per-profile indexes, i.e. mutation) — profile semantics are "shared index
//     existence + profile scope filter", nothing more.
//
//  2. AN INFRASTRUCTURE ERROR IS AN ERROR, NEVER A REASON CODE. When the index,
//     the approval store or the config-policy read fails, Evaluate returns an
//     error and the caller answers 503. A reason code is a statement about the
//     proxy's state; fabricating one from a failed read would make the whole
//     taxonomy untrustworthy — an operator would chase a `not_found` that was
//     really a BBolt hiccup. The only "absence" that legitimately becomes a
//     reason is an absence the reader reported successfully.
//
// The reason enum, its classes, retryability, default actions, the precedence
// chain and the set-verdict/exit-code mapping all live in reasons.go as the
// single source of truth; internal/contracts mirrors them for the wire and a
// drift test keeps the two identical.
package preflight

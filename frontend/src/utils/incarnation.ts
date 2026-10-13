/**
 * incarnationArgs is the trailing `expectedIncarnation` argument of an approval
 * call (Spec 116 FR-017), or nothing when the incarnation is unknown, so a call
 * site never passes an explicit `undefined`.
 */
export function incarnationArgs(expectedIncarnation?: number): [] | [number] {
  return expectedIncarnation && expectedIncarnation > 0 ? [expectedIncarnation] : []
}

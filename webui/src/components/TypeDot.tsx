// TypeDot is the memory kind mark: the kind (experience, fact, rule,
// methodology) — or the doc.* name (decision, design, review) — picks
// the colour through its .t-<kind> hook, and this element renders that
// colour itself.
export function typeClass(typeName: string): string {
  let kind = typeName.split('.')[0]
  if (kind === 'doc' && typeName.includes('.')) kind = typeName.split('.')[1]
  return 't-' + kind
}

export default function TypeDot({ type: memType }: { type: string }) {
  return <span className={`kind-mark ${typeClass(memType)}`} aria-hidden="true" />
}

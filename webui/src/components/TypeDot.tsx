// TypeDot is the #103 type-colour token: the memory kind (experience,
// fact, rule, methodology) — or the doc.* name (decision, design,
// review) — picks the dot colour from the CSS token classes.
export function typeClass(typeName: string): string {
  let kind = typeName.split('.')[0]
  if (kind === 'doc' && typeName.includes('.')) kind = typeName.split('.')[1]
  return 't-' + kind
}

export default function TypeDot({ type: memType }: { type: string }) {
  return <span className={`type-dot ${typeClass(memType)}`} aria-hidden="true" />
}

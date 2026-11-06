// A stand-in for React's automatic JSX runtime, so a component test can read
// the rendered element tree without a renderer or a DOM: jiti's `jsx` option
// (runtime "automatic") emits jsx/jsxs calls that land here, and the test
// walks the plain objects. Props keep their JSX names (`className`,
// `aria-busy`), never React's DOM aliases.
export function jsx(type, props) {
  return { type, props: props ?? {} };
}

export const jsxs = jsx;
export const jsxDEV = jsx;
export const Fragment = Symbol.for("react.fragment");
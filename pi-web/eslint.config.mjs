import coreWebVitals from "eslint-config-next/core-web-vitals";
import typescript from "eslint-config-next/typescript";

const eslintConfig = [
  // demo/ is the static GitHub Pages export, and its `npm run build` writes
  // generated bundles to demo/.next/ and demo/out/ (git-ignored by
  // demo/.gitignore, but eslint does not read .gitignore): never lint build
  // output.
  { ignores: ["demo/.next/**", "demo/out/**"] },
  // demo/ (the static GitHub Pages demo) was removed in the upstream merge; nothing here references it anymore.
  ...coreWebVitals,
  ...typescript,
  {
    rules: {
      "react-hooks/immutability": "off",
      "react-hooks/preserve-manual-memoization": "off",
      "react-hooks/refs": "off",
      "react-hooks/set-state-in-effect": "off",
      "react-hooks/preserve-manual-memoization": "off",
    },
  },
];

export default eslintConfig;

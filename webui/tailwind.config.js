/** @type {import('tailwindcss').Config} */
export default {
  // The dark theme follows the SYSTEM preference: every colour token is
  // a CSS variable that styles.css re-maps under prefers-color-scheme,
  // so no class toggle and no inline style ever exist. This file only
  // exposes those variables (and the type scale) to utilities.
  darkMode: 'media',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg: 'var(--bg)',
        surface: 'var(--surface)',
        tint: 'var(--tint)',
        line: 'var(--line)',
        fg: 'var(--fg)',
        muted: 'var(--muted)',
        accent: 'var(--accent)',
        'accent-soft': 'var(--accent-soft)',
        'accent-fg': 'var(--accent-fg)',
        ok: 'var(--ok)',
        warn: 'var(--warn)',
        bad: 'var(--bad)',
      },
      fontFamily: {
        sans: 'var(--font-sans)',
        mono: 'var(--font-mono)',
      },
      fontSize: {
        title: ['var(--fs-title)', { lineHeight: '1.3' }],
        section: ['var(--fs-section)', { lineHeight: '1.4' }],
        body: ['var(--fs-body)', { lineHeight: '1.5' }],
        caption: ['var(--fs-caption)', { lineHeight: '1.4' }],
      },
      maxWidth: {
        prose: '72ch',
      },
    },
  },
  plugins: [],
}

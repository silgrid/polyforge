/** @type {import('tailwindcss').Config} */
export default {
  // The dark theme follows the SYSTEM preference (the owner-confirmed
  // visual language): colour tokens are CSS variables that
  // styles.css re-maps under prefers-color-scheme, so no class toggle
  // and no inline style ever exist.
  darkMode: 'media',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg: 'var(--bg)',
        fg: 'var(--fg)',
        muted: 'var(--muted)',
        line: 'var(--line)',
        tint: 'var(--tint)',
        accent: 'var(--blue)',
        ok: 'var(--green)',
        warn: 'var(--amber)',
        bad: 'var(--red)',
      },
      maxWidth: {
        prose: '72ch',
      },
    },
  },
  plugins: [],
}

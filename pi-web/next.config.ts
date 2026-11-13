import type { NextConfig } from "next";
import { readFileSync } from "fs";
import { dirname, join } from "path";
import { fileURLToPath } from "url";

const configDir = dirname(fileURLToPath(import.meta.url));
const { version } = JSON.parse(readFileSync(join(configDir, "package.json"), "utf8")) as { version: string };
// A release never bumps main's package.json (the release is tag-only), so the
// value above is a development one on a tagged tree. The repository's release
// entry (.polyforge/release.sh at the repository root) hands the tag's version
// to the build as PI_WEB_RELEASE_VERSION; that wins, and only a local/dev build
// without it falls back to package.json. The UI renders `v<value>` itself
// (ChatWindow, SessionSidebar), so the tag's leading `v` is stripped here —
// the release hands us the tag as is, e.g. `v0.2.0`.
const appVersion = (process.env.PI_WEB_RELEASE_VERSION || version).replace(/^v/, "");
let piVersion = "unknown";
try {
  const piPkgPath = join(configDir, "node_modules/@earendil-works/pi-coding-agent/package.json");
  piVersion = (JSON.parse(readFileSync(piPkgPath, "utf8")) as { version: string }).version;
} catch { /* package not found, use default */ }

// mdast-util-gfm-autolink-literal (remark-gfm) ships a RegExp lookbehind that
// Safari parses only from 16.4, which blanked `/` on iOS 16.2 (#753). The loader
// swaps it for an equivalent built at runtime; both bundlers must run it.
const gfmAutolinkEmailLoader = join(configDir, "lib/gfm-autolink-email-loader.cjs");

const nextConfig: NextConfig = {
  // e2e only: e2e/all.mjs gives each browser suite its own dist dir so several
  // `next dev` can run in one checkout (each has its own dev/lock and cache).
  distDir: process.env.PI_WEB_DIST_DIR || ".next",
  // e2e only: use tsconfig.e2e.json (it extends the repo one) so Next never
  // rewrites the checkout's tsconfig.json for a per-suite distDir.
  ...(process.env.PI_WEB_DIST_DIR
    ? { typescript: { tsconfigPath: "tsconfig.e2e.json" } }
    : {}),
  outputFileTracingRoot: configDir,
  // Compress HTML and static responses with Next's built-in gzip. Direct
  // clients that bypass the edge reverse proxy — e.g. a Chrome app-mode
  // window (`piweb` alias) pointed at the raw IP:port — would otherwise
  // pull several MB of uncompressed JS chunks on a slow link (measured
  // 2026-09-27: a 1.1 MB static chunk serves as ~380 KB gzipped, -67%).
  // Clients reaching the server through Caddy are unaffected: Caddy passes
  // through responses that already carry content-encoding, so its own
  // `encode zstd gzip` layer keeps serving domain clients and nothing is
  // double-compressed. (Compress was disabled in pi#40 to work around a
  // since-removed client's HTML proxy and kept off as an unneeded behavior
  // change; the direct-IP gap was measured on 2026-09-27 and it is
  // re-enabled here.)
  compress: true,
  experimental: {
    // proxy.ts matches /api/:path*, and Next buffers the request body whenever
    // a proxy is present, capped at 10 MB by default. The upload route accepts
    // up to 100 MB per request, so raise the buffer above that or large uploads
    // are truncated and fail with "Failed to parse body as FormData."
    proxyClientMaxBodySize: "128mb",
  },
  // next/image is only used for the static logo, so the /_next/image optimizer
  // (and its sharp/libheif attack surface, see GHSA-2xp9-vwfh-vxw4) is not needed.
  images: { unoptimized: true },
  // `next dev` runs Turbopack and `npm run build` runs webpack.
  turbopack: {
    rules: {
      "**/mdast-util-gfm-autolink-literal/lib/index.js": { loaders: [gfmAutolinkEmailLoader] },
    },
  },
  webpack(config) {
    config.module.rules.push({
      test: /[\\/]mdast-util-gfm-autolink-literal[\\/]lib[\\/]index\.js$/,
      loader: gfmAutolinkEmailLoader,
    });
    return config;
  },
  // Node modules keep the syntax they ship unless listed here, and mermaid's
  // lazy diagram chunks are full of class `static {}` blocks (#753).
  transpilePackages: ["mermaid", "@mermaid-js/parser"],
  serverExternalPackages: [
    "node-pty",
    "undici",
    "web-push",
    "@earendil-works/pi-coding-agent",
    "@earendil-works/pi-agent-core",
    "@earendil-works/pi-ai",
    "@earendil-works/pi-tui",
  ],
  // Next 16 blocks cross-origin access to dev resources by default. Allow the
  // loopback and the RFC1918 LAN ranges so the dev server stays reachable
  // from other machines on the same LAN.
  allowedDevOrigins: [
    "127.0.0.1",
    "10.*.*.*",
    // 172.16.0.0/12
    "172.16.*.*",
    "172.17.*.*",
    "172.18.*.*",
    "172.19.*.*",
    "172.20.*.*",
    "172.21.*.*",
    "172.22.*.*",
    "172.23.*.*",
    "172.24.*.*",
    "172.25.*.*",
    "172.26.*.*",
    "172.27.*.*",
    "172.28.*.*",
    "172.29.*.*",
    "172.30.*.*",
    "172.31.*.*",
    "192.168.*.*",
  ],
  async headers() {
    return [
      {
        source: "/",
        headers: [
          { key: "Cache-Control", value: "private, no-cache, max-age=0, must-revalidate" },
        ],
      },
      {
        source: "/sw.js",
        headers: [
          { key: "Cache-Control", value: "public, max-age=0, must-revalidate" },
          { key: "Service-Worker-Allowed", value: "/" },
        ],
      },
      {
        source: "/manifest.webmanifest",
        headers: [
          { key: "Cache-Control", value: "public, max-age=0, must-revalidate" },
        ],
      },
    ];
  },
  env: {
    NEXT_PUBLIC_APP_VERSION: appVersion,
    NEXT_PUBLIC_PI_VERSION: piVersion,
  },
};

export default nextConfig;

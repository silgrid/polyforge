# Clients: Chrome app mode and PWAs (pi#115)

Pi Web is a self-hosted web app, so the client story is deliberately thin:
**no bundled server, no custom native UI, no proxying, no script injection**.
Every client opens the same deployed origin you already run — the browser's
own renderer does all the work, exactly as it does in a desktop tab. (The
former native clients — an Android hybrid app, a fullscreen Android
launcher and a desktop wrapper — were removed in pi#40 and pi#115; nothing
of them survives in this repo.)

| Platform | Standalone form | Install path |
|---|---|---|
| macOS | **Chrome app mode** — a standalone window via the `piweb` alias | alias in the shell rc file (below) |
| Windows | **Chrome app mode** — a standalone window via the `piweb` function | function in the PowerShell profile (below) |
| iPhone | **PWA** — Safari Add-to-Home-Screen is the native standalone form | Safari → Share → Add to Home Screen |
| Android | **PWA** (Chrome → Install app) — for now | Chrome → ⋮ → Install app |

## macOS: Chrome app-mode alias

Chrome's `--app` flag opens an origin as a standalone window: no address
bar, no tabs, a window of its own. Put the alias in `~/.bashrc` (or
`~/.zshrc`):

```bash
alias piweb='open -na "Google Chrome" --args --app=http://<ip:port>'
```

`<ip:port>` is your deployment's host and port — the LAN `http://` address
works as-is (app mode has no HTTPS requirement), and so does any HTTPS
origin you expose (`https://piweb.example.com`, for example).

## Windows: Chrome app-mode function

The same idea as a PowerShell function in your `$PROFILE`:

```powershell
function piweb { Start-Process chrome '--app=http://<ip:port>' }
```

## iPhone: Add to Home Screen (the PWA is the native form)

Safari's Add-to-Home-Screen *is* the standalone native form on iOS. The
required metadata is in place and verified in the app code:

- `app/manifest.ts`: `display: "standalone"`, 192/512 PNG icons
- `app/layout.tsx`: `apple-touch-icon` and `appleWebApp { capable: true, title,
  statusBarStyle: "black-translucent" }`

Install: open the site (e.g. `https://piweb.example.com`) in **Safari**
(not Chrome — only Safari can install), tap **Share → Add to Home Screen**.
The icon launches fullscreen, standalone, with safe-area-correct layout.
Web Push works on iOS 16.4+ once you grant the notification permission
(see below).

## Android: Chrome PWA (for now)

Open the origin in Chrome and tap **⋮ → Install app** (or the install
prompt): Chrome installs the PWA — fullscreen, no address bar, its own
home-screen icon. "For now": Chrome mints the installed app through
Google Play Services, and the Google-free fullscreen client this repo used
to ship was removed in pi#115; if Android needs a Google-free client
again, that is a separate work item.

## China reachability — honest notes

- **iOS Web Push needs no VPN.** Push rides Apple's APNs, which is reachable
  from mainland China.
- **Android PWA install and closed-app Web Push need Google.** The installed
  app is minted through Play Services, and closed-app Web Push is delivered
  only through Google's FCM endpoints; in mainland China both need a VPN.
  There is no China-reachable push relay and no Google-free install path in
  this repo; that would be a separate work item.

## Notification capability matrix

| Capability | Android (browser/PWA) | iOS PWA (16.4+) |
|---|---|---|
| In-page notifications (page focused) | ✅ | ✅ |
| Service-worker notifications while the app is alive in the background | ✅ | ✅ |
| Closed-app **Web Push** (agent finishes while no tab/window is open) | ✅ via FCM — **VPN required in mainland China** | ✅ via APNs — works in China, no VPN |
| Background Android push **without** VPN | ❌ not promised | n/a |

What this means practically: keep the Pi Web tab (or an installed PWA
window) alive and notifications arrive everywhere, no VPN anywhere. If you
close the app entirely, iOS still delivers through APNs, but Android needs
FCM reachability, i.e. a VPN in mainland China.
// Browser-error collector with host network-change tolerance for the e2e
// suites (polyforge#156). run.mjs judged every console error and pageerror
// red, so a sibling container starting or stopping on the shared host adds
// or removes a veth/bridge interface and Chromium fails an in-flight request
// (even to 127.0.0.1) with net::ERR_NETWORK_CHANGED; the page's own console
// line about it then red the suite (#128: three false reds, green on a
// targeted re-run).
//
// trackBrowserErrors wires the page's error events and returns a collector:
// settle() pairs, at the flow's end (so event order cannot matter — on the
// real browser the requestfailed event arrives first), each console error
// whose text carries the requestfailed errorText token
// ("Failed to load resource: net::ERR_NETWORK_CHANGED"; Chromium embeds the
// error code verbatim, so the token matches whatever prefix the host
// produces) with one unconsumed requestfailed(errorText=net::ERR_NETWORK_CHANGED)
// event, tolerates at most 3 per flow and logs each one
//   INFO tolerated network-change: <url>
// Everything else — an unpaired console error with the same text, an
// application catch text ("Failed to fetch"), ERR_CONNECTION_REFUSED,
// pageerror, a 5xx from the test server, the tolerated pair past the cap —
// stays red. A helper module imported by e2e/run.mjs, not a suite: it
// declares no `// carries:` header. The rules are pinned by
// lib/e2e-console-errors.test.mjs; e2e/README.md "Network-change tolerance"
// documents them.
const NETWORK_CHANGE_ERROR_TEXT = "net::ERR_NETWORK_CHANGED";

export function trackBrowserErrors(page, { base, log = console.log, maxToleratedNetworkChanges = 3 } = {}) {
  const errors = [];
  const networkChangeUrls = [];
  page.on("pageerror", (error) => { errors.push({ text: error.message, tolerable: false }); });
  page.on("console", (event) => {
    if (event.type() === "error") errors.push({ text: event.text(), tolerable: true });
  });
  page.on("requestfailed", (request) => {
    if (request.failure()?.errorText === NETWORK_CHANGE_ERROR_TEXT) networkChangeUrls.push(request.url());
  });
  page.on("response", (response) => {
    if (response.url().startsWith(base) && response.status() >= 500) {
      errors.push({ text: `${response.status()} ${response.url()}`, tolerable: false });
    }
  });
  return {
    // Called once, at the flow's end: pairs the tolerable console errors
    // with the network-change requestfailures, logs each tolerated one and
    // returns the errors that stay red.
    settle() {
      const remaining = [];
      let paired = 0;
      let tolerated = 0;
      for (const error of errors) {
        if (error.tolerable && tolerated < maxToleratedNetworkChanges && paired < networkChangeUrls.length
          && error.text.includes(NETWORK_CHANGE_ERROR_TEXT)) {
          log(`INFO tolerated network-change: ${networkChangeUrls[paired]}`);
          paired += 1;
          tolerated += 1;
        } else {
          remaining.push(error.text);
        }
      }
      return remaining;
    },
  };
}

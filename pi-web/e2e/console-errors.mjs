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
// and "Resource-load error attribution" document them.
//
// Resource-load attribution (polyforge#180): Chromium's resource-load text
// ("Failed to load resource: …") carries the status for HTTP failures but
// never the resource's address — #179's full-8 red was a bare 403 nobody
// could attribute. Each resource-load console error appends the console
// event's location().url in brackets after the raw text
// ("… status of 403 (Forbidden) [http://…]"); a console event without a
// location URL keeps its raw text — nothing is guessed, and network
// failures keep their net:: error code. The red paths are unchanged: 4xx
// responses alone collect nothing, and the network-change pairing above
// reads only the raw console text, so a token that appears merely in the
// appended URL never pairs.
const NETWORK_CHANGE_ERROR_TEXT = "net::ERR_NETWORK_CHANGED";
const RESOURCE_LOAD_PREFIX = "Failed to load resource:";

// The red text for one console error: the raw text, plus the resource URL
// (the console event's location) appended for resource-load errors — the
// only kind whose subject the text itself never names.
function reportText(event) {
  const text = event.text();
  if (!text.startsWith(RESOURCE_LOAD_PREFIX)) return text;
  const url = event.location()?.url;
  return url ? `${text} [${url}]` : text;
}

export function trackBrowserErrors(page, { base, log = console.log, maxToleratedNetworkChanges = 3 } = {}) {
  const errors = [];
  const networkChangeUrls = [];
  page.on("pageerror", (error) => { errors.push({ text: error.message, tolerable: false }); });
  page.on("console", (event) => {
    if (event.type() === "error") errors.push({ text: event.text(), tolerable: true, report: reportText(event) });
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
          remaining.push(error.report ?? error.text);
        }
      }
      return remaining;
    },
  };
}

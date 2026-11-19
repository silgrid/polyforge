// Browser-error collector for the e2e suites (polyforge#156). Stub commit:
// the signature the tolerance unit test needs, with run.mjs's current
// no-tolerance semantics — every console error, pageerror and in-base 5xx
// is collected red and settle() returns them all. The implementation commit
// adds the requestfailed(net::ERR_NETWORK_CHANGED) pairing, the cap and the
// INFO lines; see lib/e2e-console-errors.test.mjs for the rules.
export function trackBrowserErrors(page, { base } = {}) {
  const errors = [];
  page.on("pageerror", (error) => { errors.push(error.message); });
  page.on("console", (event) => { if (event.type() === "error") errors.push(event.text()); });
  page.on("response", (response) => {
    if (response.url().startsWith(base) && response.status() >= 500) errors.push(`${response.status()} ${response.url()}`);
  });
  return { settle: () => errors };
}

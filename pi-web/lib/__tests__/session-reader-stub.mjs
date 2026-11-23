// The allowed-roots cache's scan, made controllable from lib/file-access.test.mjs
// (polyforge#252): the test decides when listAllSessions() resolves, so it can
// invalidate the cache while a scan is in flight and prove the pre-delete scan
// is never written back. Loaded through jiti's `./session-reader` alias in
// place of the real reader.
export const state = {
  sessions: [],
  hold: null,
  calls: 0,
};

export function setSessions(sessions) {
  state.sessions = sessions;
}

/**
 * Holds the next listAllSessions() at its first await until the returned
 * release function is called. A scan that starts while no hold is set is not
 * held.
 */
export function holdNextScan() {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  state.hold = promise;
  return () => {
    state.hold = null;
    release();
  };
}

export async function listAllSessions() {
  state.calls += 1;
  const sessions = state.sessions.slice();
  const hold = state.hold;
  if (hold) await hold;
  return sessions;
}

// Stub signature (polyforge#140 u1): the bounded worker pool that runs the
// session list's transcript parsing off the main thread. The implementation
// lands with the unit's implementation commit.
export interface SessionScanTaskResult {
  info: null;
  resume?: undefined;
  readStarts: [];
}

export async function runSessionScanTask(): Promise<undefined> {
  return undefined;
}

export function disposeSessionScanWorkerPoolForTests(): void {}

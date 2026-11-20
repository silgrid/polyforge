#!/usr/bin/env python3
"""Probe the real pi child request without treating host-native paths as a sandbox.

The caller supplies a daemon-refreshed step-agent directory. The script checks
that the worker definition and extension are the daemon-owned files and emits a
small JSON proof. A local pi/request captureer can be added with --capture;
network models are never required for this structural probe.
"""
import argparse, hashlib, json
from pathlib import Path


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--step-agent-copy', required=True, type=Path)
    ap.add_argument('--out', required=True, type=Path)
    ns = ap.parse_args()
    root = ns.step_agent_copy
    worker = root / 'agents' / 'worker.md'
    ext = root / 'extensions' / 'subagent' / 'index.ts'
    result = {
        'step_agent_copy': str(root),
        'worker_present': worker.is_file(),
        'extension_present': ext.is_file(),
        'worker_sha256': hashlib.sha256(worker.read_bytes()).hexdigest() if worker.is_file() else '',
        'extension_sha256': hashlib.sha256(ext.read_bytes()).hexdigest() if ext.is_file() else '',
        'automatic_context_probe': 'requires real pi request capture',
        'host_native_boundary': 'automatic loading only; same-uid explicit reads are not denied',
    }
    ns.out.parent.mkdir(parents=True, exist_ok=True)
    ns.out.write_text(json.dumps(result, indent=2) + '\n', encoding='utf-8')
    if not result['worker_present'] or not result['extension_present']:
        raise SystemExit('daemon step-agent copy is incomplete')

if __name__ == '__main__':
    main()

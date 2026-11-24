#!/usr/bin/env bash
# fake pf: the step-script tests' landing lock check (polyforge#245). It
# answers held for every repo unless FAKE_PF_REFUSE names one (then it
# refuses with exit 1, as a lost lease or a transport failure does).
if [ "$1" = "step" ] && [ "$2" = "lock-check" ]; then
  repo=""
  shift 2
  while [ $# -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      *) shift ;;
    esac
  done
  if [ -n "${FAKE_PF_REFUSE:-}" ] && [ "$repo" = "$FAKE_PF_REFUSE" ]; then
    echo "pf step lock-check --repo $repo: the landing lock was lost" >&2
    exit 1
  fi
  echo "lock-check $repo: held"
  exit 0
fi
echo "fake pf: unknown command: $*" >&2
exit 1

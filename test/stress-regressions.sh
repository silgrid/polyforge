#!/usr/bin/env bash
# test/stress-regressions.sh (polyforge#272 U3) — placeholder: parses its
# arguments and judges nothing yet.
set -u
while [ $# -gt 0 ]; do
  case "$1" in
    --repo|--phase|--rounds|--jobs|--out|--item|--items-file|--round-timeout) shift 2 ;;
    *) echo "stress-regressions: unknown argument $1" >&2; exit 2 ;;
  esac
done
exit 1

#!/bin/sh
# A fake docker client for the step scripts' tests (pfstep.container's
# DockerCLI, and the Go client the differential tests drive): every
# "container" runs its commands on this host. State under $FAKE_DOCKER_STATE:
#   calls.log      every call, one line ("docker <args>")
#   runs.log       each `docker run`'s arguments, one call per line
#   removes.log    each removed container id
#   unavailable    when present, the engine does not answer
#   expiring       what `docker ps` lists: "<id>\t<label value>" lines
#   images/        the images built (one empty file per tag)
#   tmp/           a container's /tmp/... paths, mapped here
# $FAKE_DOCKER_PATH is a container's PATH (default /usr/bin:/bin).
S=${FAKE_DOCKER_STATE:?FAKE_DOCKER_STATE is unset}
mkdir -p "$S/images" "$S/tmp"
echo "docker $*" >> "$S/calls.log"
tagfile() { echo "$S/images/$(echo "$1" | tr '/:' '__')"; }
case "$1" in
  version)
    [ -e "$S/unavailable" ] && { echo "Cannot connect to the Docker daemon (fake)" >&2; exit 1; }
    echo 29.0 ;;
  image)
    case "$2" in
      ls) for last; do :; done; t=${last#reference=}; [ -e "$(tagfile "$t")" ] && echo sha256:fake ;;
      inspect) printf '["PATH=%s","HOME=/root"]\n' "${FAKE_DOCKER_PATH:-/usr/bin:/bin}" ;;
    esac ;;
  build)
    shift; tag=""
    while [ $# -gt 0 ]; do case "$1" in --tag) tag=$2; shift 2 ;; *) shift ;; esac; done
    first=$(head -n 1)
    echo "Step 1/1 : $first"
    : > "$(tagfile "$tag")" ;;
  run)
    shift; printf '%s\037' "$@" >> "$S/runs.log"; echo >> "$S/runs.log"
    n=$(wc -l < "$S/runs.log" | tr -d ' ')
    echo "cid-$n" ;;
  rm) for last; do :; done; echo "$last" >> "$S/removes.log" ;;
  volume) ;;  # no volumes: `volume ls` lists none, `volume rm` succeeds
  ps)
    [ -e "$S/expiring" ] && cat "$S/expiring"
    exit 0 ;;
  exec)
    shift; wd=""; envf="$S/env.$$"; : > "$envf"
    while [ $# -gt 0 ]; do case "$1" in
      --workdir) wd=$2; shift 2 ;;
      --env) printf '%s\n' "$2" >> "$envf"; shift 2 ;;
      *) break ;;
    esac; done
    shift  # the container id
    if [ -z "$wd" ]; then
      rm -f "$envf"
      # the keep-alive wait and the reap: nothing runs in a fake container;
      # a command probe runs here
      case "$*" in *pf-probe:*) exec "$@" ;; esac
      exit 0
    fi
    while IFS= read -r kv; do
      case "$kv" in TMPDIR=/tmp/*) kv="TMPDIR=$S/tmp/${kv#TMPDIR=/tmp/}" ;; esac
      set -- "$kv" "$@"
    done < "$envf"
    rm -f "$envf"
    cd "$wd" || exit 125
    exec env -i PATH="${FAKE_DOCKER_PATH:-/usr/bin:/bin}" HOME="$S/home" "$@" ;;
  *) echo "fake docker: unsupported: $*" >&2; exit 2 ;;
esac
exit 0

#!/bin/sh
# 两个构建提供真实组件种子；运行中的容器仅启动一次。
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
project="pf-upgrade-smoke-$$"
work=$(mktemp -d)
old=v0.2.4-dev.1
new=v0.2.4-dev.2
export PI_WEB_PASSWORD="${PI_WEB_PASSWORD:-pf-workstation-smoke-test-password}"
compose() { docker compose -p "$project" -f "$script_dir/compose.yaml" -f "$work/override.yaml" --profile aihub "$@"; }
seed_container=""
cleanup() {
 compose down -v >/dev/null 2>&1 || true
 if [ -n "$seed_container" ]; then docker rm -f "$seed_container" >/dev/null 2>&1 || true; fi
 rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM
poll() {
 what=$1; shift
 attempt=0
 until "$@"; do
  attempt=$((attempt+1)); [ "$attempt" -lt 150 ] || { echo "smoke: FAIL: $what" >&2; exit 1; }
  sleep 2
 done
}
docker version >/dev/null
mkdir -p "$work/old" "$work/new" "$work/components"
for spec in "old:$old" "new:$new"; do
 label=${spec%%:*}; ver=${spec#*:}
 docker build -f "$script_dir/Containerfile" --build-arg "SEED_VERSION=$ver" -t "$project-$label" "$root" >/dev/null
 seed_container=$(docker create "$project-$label")
 docker cp "$seed_container:/opt/polyforge/seed/." "$work/$label"
 docker rm "$seed_container" >/dev/null
 seed_container=""
done
cp -a "$work/old/." "$work/components/"
ln -s "releases/$old" "$work/components/current"
cat > "$work/override.yaml" <<YAML
services:
  workstation-aihub:
    image: $project-old
    volumes:
      - $work/components:/components
      - $work/new:/upgrade-smoke-seed:ro
YAML
compose up -d >/dev/null
cid=$(compose ps -q workstation-aihub)
[ -n "$cid" ]
before_id=$(docker inspect -f '{{.Id}}' "$cid")
before_pid=$(docker exec "$cid" sh -c 'awk '\''{print $22}'\'' /proc/1/stat')
login() { [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${WORKSTATION_WEB_PORT:-30141}/login")" = 200 ]; }
poll 'Pi Web 启动' login
# 两份种子共享底座能力，复制到可写目录后将 minBase 设为实际运行底座。
docker exec "$cid" sh -c 'cp -a /upgrade-smoke-seed/releases/v0.2.4-dev.2 /components/smoke-target'
docker exec "$cid" python3 -c 'import json;p="/components/smoke-target/manifest.json";m=json.load(open(p));m["minBase"]="v0.2.4-dev.1";json.dump(m,open(p,"w"))'
docker exec "$cid" pf components upgrade --version "$new" --seed-dir /components/smoke-target
[ "$(docker exec "$cid" readlink /components/current)" = "releases/$new" ]
[ "$(docker exec "$cid" readlink /components/previous)" = "releases/$old" ]
[ "$(docker exec "$cid" pf version)" = "$new" ]
# 分别轮询版本断言，不以可达性替代版本一致（/status 与运行中 aihub 的 --version）。
poll 'daemon/AIHub/Pi Web 版本' docker exec "$cid" python3 -c '
import http.client,json,socket,subprocess,pathlib,os
class UnixHTTP(http.client.HTTPConnection):
 def connect(self):
  self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);self.sock.settimeout(2);self.sock.connect("/root/.local/share/polyforge/daemon.sock")
c=UnixHTTP("daemon");c.request("GET","/status");d=json.loads(c.getresponse().read());assert d["version"]=="v0.2.4-dev.2",d
found=False
for p in pathlib.Path("/proc").iterdir():
 try:
  args=(p/"cmdline").read_bytes().split(b"\0")
  if b"aihub" not in args: continue
  exe=os.readlink(p/"exe")
  if not exe.startswith("/components/releases/"): continue
  assert subprocess.check_output([str(p/"exe"),"aihub","--version"],timeout=2).decode().strip()=="v0.2.4-dev.2"
  found=True
 except (FileNotFoundError,ProcessLookupError,PermissionError): continue
assert found,"AIHub 必须运行组件卷中的新版 pf"
w=json.load(open("/components/current/pi-web/package.json"));assert w["version"]=="0.2.4-dev.2",w
'
poll 'Pi Web 重连' login
[ "$(docker inspect -f '{{.Id}}' "$cid")" = "$before_id" ]
[ "$(docker exec "$cid" sh -c 'awk '\''{print $22}'\'' /proc/1/stat')" = "$before_pid" ]
docker exec "$cid" python3 -c 'import json;s=json.load(open("/components/upgrade-status.json"));assert s["state"]=="done" and s["to"]=="v0.2.4-dev.2",s'
echo 'smoke: upgrade: PASS'

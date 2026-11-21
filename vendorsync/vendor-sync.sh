#!/usr/bin/env bash
set -uo pipefail
APP="${1:?usage: vendor-sync.sh <app-dir> check|update [name]}"
CMD="${2:-check}"
ONLY="${3:-}"
VENDOR="$APP/assets/static/vendor"
MANIFEST="$VENDOR/vendor.json"
[ -f "$MANIFEST" ] || { echo "vendor-sync: no manifest at $MANIFEST" >&2; exit 64; }
exec python3 -I - "$VENDOR" "$MANIFEST" "$CMD" "$ONLY" <<'PY'
import json, os, sys, tempfile, urllib.request, urllib.parse
from concurrent.futures import ThreadPoolExecutor

vendor, manifest, cmd, only = sys.argv[1:5]
UA = {"User-Agent": "vendor-sync/1.0"}

def get(url, limit=16 << 20):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=60) as r:
        data = r.read(limit + 1)
    if len(data) > limit:
        raise RuntimeError("response too large: " + url)
    return data

def latest(pkg):
    data = json.loads(get("https://registry.npmjs.org/" + urllib.parse.quote(pkg, safe="@") + "/latest"))
    return data["version"]

def sane(path, data, old):
    if not data:
        return "empty download"
    head = data[:200].lstrip().lower()
    if head.startswith(b"<!doctype") or head.startswith(b"<html"):
        return "got an HTML page, not an asset"
    if old and len(data) < old * 0.3:
        return "shrank to under 30% of the installed file"
    return ""

with open(manifest, encoding="utf8") as f:
    doc = json.load(f)

rc = 0
changed = False
for item in doc["items"]:
    name = item["name"]
    if only and only != name:
        continue
    cur = item.get("version", "")
    try:
        new = latest(item["npm"])
    except Exception as e:
        print(f"vendor-sync: ERROR {name}: registry lookup failed: {e}", file=sys.stderr)
        rc = 2
        continue
    if new == cur:
        print(f"{name} {cur} up to date")
        continue
    print(f"{name} {cur or '(unknown)'} -> {new}")
    if cmd != "update":
        rc = rc or 1
        continue
    staged = []
    ok = True

    def fetch(fe):
        url = fe["url"].replace("{version}", new)
        dest = os.path.join(vendor, fe["dest"])
        try:
            data = get(url)
        except Exception as e:
            return url, dest, None, f"download failed: {e}"
        old = os.path.getsize(dest) if os.path.exists(dest) else 0
        return url, dest, data, sane(dest, data, old)

    with ThreadPoolExecutor(max_workers=8) as pool:
        for url, dest, data, why in pool.map(fetch, item["files"]):
            if why:
                print(f"vendor-sync: ERROR {name}: {url}: {why}", file=sys.stderr)
                ok = False
            else:
                staged.append((dest, data))
    if not ok:
        rc = 2
        continue
    for dest, data in staged:
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=os.path.dirname(dest))
        with os.fdopen(fd, "wb") as out:
            out.write(data)
        os.chmod(tmp, 0o664)
        os.replace(tmp, dest)
    item["version"] = new
    changed = True
    print(f"UPDATED {name} {cur or '(unknown)'} -> {new} ({len(staged)} files)")

if changed:
    fd, tmp = tempfile.mkstemp(dir=vendor)
    with os.fdopen(fd, "w", encoding="utf8") as out:
        json.dump(doc, out, indent=2)
        out.write("\n")
    os.chmod(tmp, 0o664)
    os.replace(tmp, manifest)
sys.exit(rc)
PY

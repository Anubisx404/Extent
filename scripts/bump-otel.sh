#!/usr/bin/env bash
# bump-otel.sh - compare the OpenTelemetry pins in internal/instrumenter/versions.json
# with the latest upstream releases (npm, PyPI, Go proxy).
#
#   scripts/bump-otel.sh           print pinned vs latest versions (no changes)
#   scripts/bump-otel.sh --write   also update versions.json to the latest versions
#
# Lookups that fail are reported as "unavailable" and left unchanged. Review the
# diff and run the instrumenter tests before committing: the Node SDK packages
# and their instrumentations must stay mutually compatible, which this script
# does not verify.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
versions_file="$repo_root/internal/instrumenter/versions.json"

write=false
case "${1:-}" in
  "") ;;
  --write) write=true ;;
  -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
  *) echo "usage: $0 [--write]" >&2; exit 2 ;;
esac

for tool in python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done

# Emit "ecosystem<TAB>name<TAB>pinned" for every pin in versions.json.
pins="$(python3 -I - "$versions_file" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)
for group in ("dependencies", "database", "esm"):
    for name, version in data["node"].get(group, {}).items():
        print(f"npm\t{name}\t{version}\tnode.{group}")
for name, version in data["python"]["dependencies"].items():
    print(f"pypi\t{name}\t{version}\tpython.dependencies")
for module in data["go"]["modules"]:
    print(f"go\t{module['path']}\t{module['version']}\tgo.modules")
PY
)"

latest_npm() {
  command -v npm >/dev/null 2>&1 || { echo ""; return; }
  npm view "$1" version 2>/dev/null || echo ""
}

latest_pypi() {
  curl -fsS --max-time 20 "https://pypi.org/pypi/$1/json" 2>/dev/null \
    | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["info"]["version"])' 2>/dev/null || echo ""
}

latest_go() {
  command -v go >/dev/null 2>&1 || { echo ""; return; }
  go list -m -json "$1@latest" 2>/dev/null \
    | python3 -I -c 'import json,sys; print(json.load(sys.stdin).get("Version",""))' 2>/dev/null || echo ""
}

# Collect "ecosystem<TAB>name<TAB>pinned<TAB>latest<TAB>group" rows.
rows=""
while IFS=$'\t' read -r ecosystem name pinned group; do
  [ -n "$name" ] || continue
  case "$ecosystem" in
    npm) latest="$(latest_npm "$name")" ;;
    pypi) latest="$(latest_pypi "$name")" ;;
    go) latest="$(latest_go "$name")" ;;
  esac
  rows+="$ecosystem"$'\t'"$name"$'\t'"$pinned"$'\t'"${latest:-}"$'\t'"$group"$'\n'
done <<< "$pins"

printf '%-6s %-44s %-12s %-12s %s\n' "ECO" "PACKAGE" "PINNED" "LATEST" "STATUS"
while IFS=$'\t' read -r ecosystem name pinned latest group; do
  [ -n "$name" ] || continue
  if [ -z "$latest" ]; then
    status="unavailable"
  elif [ "$latest" = "$pinned" ] || [ "v$latest" = "$pinned" ]; then
    status="current"
  else
    status="outdated"
  fi
  printf '%-6s %-44s %-12s %-12s %s\n' "$ecosystem" "$name" "$pinned" "${latest:--}" "$status"
done <<< "$rows"

if [ "$write" = true ]; then
  # Pass rows as a file argument so the Python process reads no stdin from the shell.
  rows_file="$(mktemp)"
  trap 'rm -f "$rows_file"' EXIT
  printf '%s' "$rows" > "$rows_file"
  python3 -I - "$versions_file" "$rows_file" <<'PY'
import json, sys
path, rows_path = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as f:
    data = json.load(f)
updates = 0
with open(rows_path, encoding="utf-8") as f:
    for line in f:
        parts = line.rstrip("\n").split("\t")
        if len(parts) != 5 or not parts[3]:
            continue
        ecosystem, name, pinned, latest, group = parts
        if ecosystem == "npm":
            section = data["node"][group.split(".", 1)[1]]
            if section.get(name) != latest:
                section[name] = latest
                updates += 1
        elif ecosystem == "pypi":
            section = data["python"]["dependencies"]
            if section.get(name) != latest:
                section[name] = latest
                updates += 1
        elif ecosystem == "go":
            want = latest if latest.startswith("v") else "v" + latest
            for module in data["go"]["modules"]:
                if module["path"] == name and module["version"] != want:
                    module["version"] = want
                    updates += 1
with open(path, "w", encoding="utf-8") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
print(f"updated {updates} pin(s) in {path}")
PY
else
  echo
  echo "Run with --write to update versions.json, then run the instrumenter tests."
fi

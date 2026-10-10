#!/usr/bin/env bash
# Moves a Haven relay that Coolify runs from an inline Dockerfile to another
# image, proving on the way that no event is lost. Run it on the server, as root.
#
#   coolify-update.sh --rehearse <image>   checks everything on a copy; production keeps running
#   coolify-update.sh <image>              stops the relay, backs it up, checks, deploys the image
#   coolify-update.sh --rollback <run>     puts back the Dockerfile a run replaced, and deploys it
#
# Before deploying, both the running image and the new one export every
# database (`haven backup`), each from its own copy of the stopped data. The
# exports are deterministic, so the new image is only deployed when they're
# byte for byte the same. Each run keeps, under BACKUP_DIR/<time>:
#   db.tar.gz        the database folder as it was when the relay stopped
#   old.zip new.zip  both exports, as JSONL
#   dockerfile.old   the Dockerfile it replaced, for --rollback
#
# Settings, from the environment:
#   COOLIFY_APP    the application's uuid (required)
#   COOLIFY_TOKEN  an API token with write and deploy rights (required)
#   COOLIFY_API    default http://localhost:8000/api/v1
#   COOLIFY_CONTAINER  Coolify's own container, default coolify
#   BACKUP_DIR     default /root/haven-backups
#   KEEP           runs to keep, default 10
set -euo pipefail

COOLIFY_API="${COOLIFY_API:-http://localhost:8000/api/v1}"
COOLIFY_CONTAINER="${COOLIFY_CONTAINER:-coolify}"
BACKUP_DIR="${BACKUP_DIR:-/root/haven-backups}"
KEEP="${KEEP:-10}"
HELPER_IMAGE="debian:bookworm-slim"

say() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }
fail() { say "FAILED: $*" >&2; exit 1; }

# Set once the relay is stopped, cleared once the new image is deploying: a
# failure in between starts the relay again, unchanged.
STOPPED=""

api() {
  local method=$1 path=$2 body=${3:-}
  local args=(-fsS -X "$method" -H "Authorization: Bearer ${COOLIFY_TOKEN}" -H 'Accept: application/json')
  if [ -n "$body" ]; then
    args+=(-H 'Content-Type: application/json' --data "$body")
  fi
  curl "${args[@]}" "${COOLIFY_API}${path}"
}

json() { python3 -c "import json,sys; print(json.load(sys.stdin)$1)"; }

running_container() {
  docker ps --filter "name=^/?${COOLIFY_APP}" --format '{{.ID}}' | head -1
}

wait_until_stopped() {
  for _ in $(seq 60); do
    [ -z "$(running_container)" ] && return 0
    sleep 2
  done
  fail "the relay didn't stop within 2 minutes"
}

wait_until_healthy() {
  local image=$1
  for _ in $(seq 150); do
    local id
    id=$(docker ps --filter "name=^/?${COOLIFY_APP}" --filter "ancestor=${image}" --format '{{.ID}}' | head -1)
    if [ -n "$id" ]; then
      local health
      health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id")
      if [ "$health" = healthy ] || [ "$health" = none ]; then
        echo "$id"
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

# Coolify's API doesn't change an inline Dockerfile, so it's saved through
# Coolify's own model, as its web interface does.
set_dockerfile() {
  local saved
  # shellcheck disable=SC2016 # PHP, not shell
  saved=$(docker exec -e APP_UUID="$COOLIFY_APP" -e DOCKERFILE="$(printf '%s' "$1" | base64 -w0)" \
    "$COOLIFY_CONTAINER" php artisan tinker --execute '
      $app = App\Models\Application::where("uuid", getenv("APP_UUID"))->firstOrFail();
      $app->dockerfile = base64_decode(getenv("DOCKERFILE"));
      $app->save();
      echo $app->fresh()->dockerfile;')
  [ "$saved" = "$1" ] || fail "Coolify didn't save the Dockerfile"
}

deploy_dockerfile() {
  set_dockerfile "$1"
  api PATCH "/applications/${COOLIFY_APP}" '{"is_consistent_container_name_enabled": true}' >/dev/null
  api POST "/applications/${COOLIFY_APP}/start" >/dev/null
}

# Runs `haven backup` with an image on a copy of the data, writing <name>.zip to the run folder.
export_with() {
  local image=$1 volume=$2 name=$3
  local command=(backup -o "/out/${name}.zip") entrypoint
  # Fetched again right before use: the server's image cleanup may have removed it.
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    docker pull -q "$image" >/dev/null || fail "can't get ${image}"
  fi
  entrypoint=$(docker image inspect -f '{{json .Config.Entrypoint}}' "$image") || fail "can't inspect ${image}"
  # Images whose entrypoint starts the relay take the command's arguments only.
  if ! grep -q 'haven' <<<"$entrypoint"; then
    command=(./haven "${command[@]}")
  fi
  docker run --rm --env-file "$RUN/env" "${BINDS[@]}" \
    -v "${volume}:/app/db" -v "$RUN:/out" "$image" "${command[@]}" >"$RUN/${name}.log" 2>&1 \
    || { tail -20 "$RUN/${name}.log" >&2; fail "exporting with ${image}"; }
}

compare_exports() {
  python3 - "$RUN/old.zip" "$RUN/new.zip" <<'PY'
import collections, hashlib, json, sys, zipfile

def read(path):
    result = {}
    with zipfile.ZipFile(path) as archive:
        for name in archive.namelist():
            data = archive.read(name)
            kinds = collections.Counter(json.loads(line)["kind"] for line in data.splitlines() if line.strip())
            result[name] = (hashlib.sha256(data).hexdigest(), sum(kinds.values()), kinds)
    return result

old, new = read(sys.argv[1]), read(sys.argv[2])
same = old.keys() == new.keys() and all(old[n][0] == new[n][0] for n in old)
for name in sorted(old.keys() | new.keys()):
    o, n = old.get(name), new.get(name)
    mark = "same" if o and n and o[0] == n[0] else "DIFFERENT"
    print(f"  {name}: {o[1] if o else '-'} events before, {n[1] if n else '-'} after, {mark}")
    if o and o[1]:
        print("    kinds: " + ", ".join(f"{k}: {c}" for k, c in sorted(o[2].items())))
sys.exit(0 if same else 1)
PY
}

cleanup() {
  if [ -n "$STOPPED" ]; then
    say "starting the relay again, as it was"
    api POST "/applications/${COOLIFY_APP}/start" >/dev/null || say "couldn't start it: start it in Coolify"
  fi
  for volume in "${TEMP_VOLUMES[@]:-}"; do
    if [ -n "$volume" ]; then
      docker volume rm -f "$volume" >/dev/null 2>&1 || true
    fi
  done
}

rollback() {
  local run=$1
  [ -f "$run/dockerfile.old" ] || fail "no dockerfile.old in $run"
  say "putting back the Dockerfile from $run"
  deploy_dockerfile "$(cat "$run/dockerfile.old")"
  say "deploy started; follow it in Coolify"
}

main() {
  [ -n "${COOLIFY_APP:-}" ] || fail "set COOLIFY_APP"
  [ -n "${COOLIFY_TOKEN:-}" ] || fail "set COOLIFY_TOKEN"
  local mode=deploy
  case "${1:-}" in
    --rehearse) mode=rehearse; shift ;;
    --rollback) rollback "${2:?usage: --rollback <run folder>}"; return ;;
  esac
  local image=${1:?usage: coolify-update.sh [--rehearse] <image>}

  RUN="${BACKUP_DIR}/$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$RUN"
  chmod 700 "$BACKUP_DIR"
  # The new image writes its export as an unprivileged user.
  chmod 777 "$RUN"
  TEMP_VOLUMES=()
  trap cleanup EXIT

  say "run folder: $RUN"
  local container
  container=$(running_container)
  [ -n "$container" ] || fail "the relay isn't running, so there's nothing to compare against"
  local current_image volume
  current_image=$(docker inspect -f '{{.Config.Image}}' "$container")
  volume=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/db"}}{{.Name}}{{end}}{{end}}' "$container")
  [ -n "$volume" ] || fail "no volume on /app/db: the data isn't on a volume"
  say "running: $current_image, data in volume $volume"

  # The same settings and mounted files, minus anything that reaches the outside.
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$container" \
    | grep -v -E '^(PATH|HOSTNAME|HOME|SOURCE_COMMIT|COOLIFY_[A-Z_]*|S3_[A-Z_]*|BACKUP_PROVIDER)=' \
    | grep -v '^$' >"$RUN/env"
  echo "BACKUP_PROVIDER=none" >>"$RUN/env"
  chmod 600 "$RUN/env"
  BINDS=()
  while read -r source destination; do
    [ -n "$source" ] && BINDS+=(-v "${source}:${destination}:ro")
  done < <(docker inspect -f '{{range .Mounts}}{{if eq .Type "bind"}}{{.Source}} {{.Destination}}{{println}}{{end}}{{end}}' "$container")

  api GET "/applications/${COOLIFY_APP}" | json '["dockerfile"]' >"$RUN/dockerfile.old"

  say "pulling $image"
  docker pull -q "$image" >/dev/null

  if [ "$mode" = deploy ]; then
    say "stopping the relay"
    api POST "/applications/${COOLIFY_APP}/stop" >/dev/null
    STOPPED=yes
    wait_until_stopped
    say "backing up the stopped database"
    docker run --rm -v "${volume}:/data:ro" -v "$RUN:/backup" "$HELPER_IMAGE" \
      tar czf /backup/db.tar.gz -C /data .
  else
    say "rehearsal: copying the live database, which keeps running"
  fi

  # Each image exports from its own copy, so production's files stay as they are.
  # Both copies come from one snapshot, so a live write can't tell them apart.
  local stamp old_copy new_copy
  stamp=$(basename "$RUN")
  old_copy="haven-verify-old-${stamp}"
  new_copy="haven-verify-new-${stamp}"
  TEMP_VOLUMES+=("$old_copy" "$new_copy")
  docker volume create "$old_copy" >/dev/null
  docker volume create "$new_copy" >/dev/null
  docker run --rm -v "${volume}:/from:ro" -v "${old_copy}:/to" "$HELPER_IMAGE" cp -a /from/. /to/
  docker run --rm -v "${old_copy}:/from:ro" -v "${new_copy}:/to" "$HELPER_IMAGE" cp -a /from/. /to/
  say "exporting with the running image"
  export_with "$current_image" "$old_copy" old
  say "exporting with $image"
  export_with "$image" "$new_copy" new

  say "comparing the exports"
  if ! compare_exports; then
    fail "exports differ; nothing was deployed"
  fi

  if [ "$mode" = rehearse ]; then
    say "rehearsal passed: $image reads exactly what the running relay holds"
    return
  fi

  say "deploying $image"
  deploy_dockerfile "FROM ${image}"
  STOPPED=""
  local id
  if ! id=$(wait_until_healthy "$image"); then
    fail "the new relay isn't healthy after 5 minutes. To go back: $0 --rollback $RUN"
  fi
  say "healthy: container $id"

  # Run folders are named by time, so the oldest sort first.
  find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '20*' | sort | head -n "-${KEEP}" | xargs -r rm -rf
  say "done. Backups of this run: $RUN"
}

main "$@"

#!/usr/bin/env bash
set -Eeuo pipefail

DEVBOX_USER="${DEVBOX_USER:-liushiao}"
DEVBOX_HOST="${DEVBOX_HOST:-10.37.6.166}"
DEVBOX="${DEVBOX:-${DEVBOX_USER}@${DEVBOX_HOST}}"

REMOTE_ROOT="${REMOTE_ROOT:-/data00/home/liushiao/cube20-deploy-test}"
REMOTE_TMP="${REMOTE_TMP:-${REMOTE_ROOT}/tmp}"
REMOTE_BIN="${REMOTE_BIN:-${REMOTE_ROOT}/bin/cube}"
REMOTE_NEW="${REMOTE_NEW:-${REMOTE_TMP}/cube-linux-amd64.new}"
REMOTE_ENV="${REMOTE_ENV:-/home/liushiao/.cube20/cube-server.env}"

LOCAL_BIN="${LOCAL_BIN:-bin/cube-linux-amd64}"
PROD_HOST="${PROD_HOST:-0.0.0.0}"
PROD_PORT="${PROD_PORT:-8720}"
CANARY_PORT="${CANARY_PORT:-8721}"
SMOKE_HOST="${SMOKE_HOST:-127.0.0.1}"

DRY_RUN="${DRY_RUN:-0}"
SMOKE_ONLY="${SMOKE_ONLY:-0}"
SSH_BIN="${SSH_BIN:-ssh}"
SCP_BIN="${SCP_BIN:-scp}"

# shellcheck disable=SC2206
SSH_OPTS_ARRAY=(${SSH_OPTS:-})
# shellcheck disable=SC2206
SCP_OPTS_ARRAY=(${SCP_OPTS:-})

usage() {
  cat <<'USAGE'
Usage: deploy/devbox_deploy.sh [--dry-run] [--smoke-only]

Build and deploy cube to the devbox test deployment:
  1. GOOS=linux GOARCH=amd64 go build -o bin/cube-linux-amd64 ./cmd/cube
  2. scp to /data00/home/liushiao/cube20-deploy-test/tmp/cube-linux-amd64.new
  3. canary on 127.0.0.1:8721 with isolated HOME/CODEX_HOME
  4. back up bin/cube, atomically replace it, restart dashboard on 8720
  5. smoke /healthz and /readyz

Modes:
  --dry-run      Print local/remote actions without running them.
  --smoke-only  Only smoke the current 8720 devbox service.

Environment overrides:
  DEVBOX, REMOTE_ROOT, REMOTE_BIN, REMOTE_ENV, LOCAL_BIN
  PROD_HOST, PROD_PORT, CANARY_PORT, SMOKE_HOST
  DRY_RUN=1, SMOKE_ONLY=1, SSH_OPTS, SCP_OPTS

The script never prints the contents of REMOTE_ENV or any token value. The env
file is sourced only inside the remote shell before starting the dashboard.
USAGE
}

log() {
  printf '==> %s\n' "$*"
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

is_dry_run() {
  [[ "${DRY_RUN}" == "1" ]]
}

print_cmd() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
}

run_cmd() {
  if is_dry_run; then
    print_cmd "$@"
    return 0
  fi
  "$@"
}

ssh_cmd() {
  if ((${#SSH_OPTS_ARRAY[@]})); then
    "${SSH_BIN}" "${SSH_OPTS_ARRAY[@]}" "$@"
  else
    "${SSH_BIN}" "$@"
  fi
}

scp_cmd() {
  if is_dry_run; then
    if ((${#SCP_OPTS_ARRAY[@]})); then
      print_cmd "${SCP_BIN}" "${SCP_OPTS_ARRAY[@]}" "$@"
    else
      print_cmd "${SCP_BIN}" "$@"
    fi
    return 0
  fi

  if ((${#SCP_OPTS_ARRAY[@]})); then
    "${SCP_BIN}" "${SCP_OPTS_ARRAY[@]}" "$@"
  else
    "${SCP_BIN}" "$@"
  fi
}

parse_args() {
  while (($#)); do
    case "$1" in
      --dry-run)
        DRY_RUN=1
        ;;
      --smoke-only)
        SMOKE_ONLY=1
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1"
        ;;
    esac
    shift
  done
}

build_binary() {
  log "Building linux/amd64 binary: ${LOCAL_BIN}"
  local commit build_date ldflags
  commit="$(git rev-parse --short HEAD 2>/dev/null || printf 'unknown')"
  build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ldflags="-X main.buildCommit=${commit} -X main.buildDate=${build_date}"
  run_cmd mkdir -p "$(dirname "${LOCAL_BIN}")"
  run_cmd env GOOS=linux GOARCH=amd64 go build -ldflags "${ldflags}" -o "${LOCAL_BIN}" ./cmd/cube
}

upload_binary() {
  log "Uploading ${LOCAL_BIN} to ${DEVBOX}:${REMOTE_NEW}"
  if is_dry_run; then
    log "DRY_RUN: would create ${REMOTE_TMP} and $(dirname "${REMOTE_BIN}") on ${DEVBOX}"
  else
    ssh_cmd "${DEVBOX}" bash -s -- "${REMOTE_TMP}" "$(dirname "${REMOTE_BIN}")" <<'REMOTE'
set -Eeuo pipefail
mkdir -p "$1" "$2"
REMOTE
  fi
  scp_cmd "${LOCAL_BIN}" "${DEVBOX}:${REMOTE_NEW}"
}

remote_canary() {
  log "Running canary on ${DEVBOX}:127.0.0.1:${CANARY_PORT}"
  if is_dry_run; then
    log "DRY_RUN: would start ${REMOTE_NEW} with isolated HOME/CODEX_HOME and smoke /healthz /readyz"
    return 0
  fi

  ssh_cmd "${DEVBOX}" bash -s -- \
    "${REMOTE_ROOT}" "${REMOTE_NEW}" "${REMOTE_ENV}" "${CANARY_PORT}" <<'REMOTE'
set -Eeuo pipefail

remote_root="$1"
new_bin="$2"
env_file="$3"
port="$4"

ts="$(date +%Y%m%d%H%M%S)"
canary_root="${remote_root}/tmp/canary-${ts}-$$"
log_file="${remote_root}/tmp/canary-${ts}.log"

mkdir -p "${canary_root}/home" "${canary_root}/codex" "$(dirname "${log_file}")"
chmod 700 "${canary_root}" "${canary_root}/home" "${canary_root}/codex"

if [[ ! -s "${new_bin}" ]]; then
  echo "canary binary is missing or empty: ${new_bin}" >&2
  exit 1
fi
chmod 0755 "${new_bin}"

if [[ -f "${env_file}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "${env_file}"
  set +a
fi

export HOME="${canary_root}/home"
export CODEX_HOME="${canary_root}/codex"
export CUBE_QUOTA_REFRESH_INTERVAL=0s

"${new_bin}" dashboard --host 127.0.0.1 --port "${port}" >"${log_file}" 2>&1 &
pid="$!"

cleanup() {
  status="$?"
  if kill -0 "${pid}" 2>/dev/null; then
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  fi
  rm -rf "${canary_root}"
  exit "${status}"
}
trap cleanup EXIT

probe() {
  path="$1"
  url="http://127.0.0.1:${port}${path}"
  for _ in $(seq 1 30); do
    if curl -fsS --max-time 2 -o /dev/null "${url}"; then
      return 0
    fi
    if ! kill -0 "${pid}" 2>/dev/null; then
      echo "canary process exited before ${path}; log: ${log_file}" >&2
      return 1
    fi
    sleep 1
  done
  echo "canary probe failed for ${path}; log: ${log_file}" >&2
  return 1
}

probe /healthz
probe /readyz
echo "canary ok on 127.0.0.1:${port}; log: ${log_file}"
REMOTE
}

remote_deploy() {
  log "Replacing ${REMOTE_BIN}, restarting ${PROD_HOST}:${PROD_PORT}, and smoking probes"
  if is_dry_run; then
    log "DRY_RUN: would back up ${REMOTE_BIN}, mv ${REMOTE_NEW} into place, restart dashboard, and smoke /healthz /readyz"
    return 0
  fi

  ssh_cmd "${DEVBOX}" bash -s -- \
    "${REMOTE_ROOT}" "${REMOTE_NEW}" "${REMOTE_BIN}" "${REMOTE_ENV}" \
    "${PROD_HOST}" "${PROD_PORT}" "${SMOKE_HOST}" <<'REMOTE'
set -Eeuo pipefail

remote_root="$1"
new_bin="$2"
remote_bin="$3"
env_file="$4"
host="$5"
port="$6"
smoke_host="$7"

ts="$(date +%Y%m%d%H%M%S)"
tmp_dir="${remote_root}/tmp"
log_dir="${remote_root}/logs"
pid_file="${tmp_dir}/cube-dashboard.pid"
log_file="${log_dir}/cube-dashboard-${ts}.log"
backup=""

rollback_hint() {
  if [[ -n "${backup}" ]]; then
    cat >&2 <<EOF
rollback hint on devbox:
  cp -p '${backup}' '${remote_bin}'
  pkill -f '${remote_bin} dashboard' || true
  set -a; . '${env_file}'; set +a
  nohup '${remote_bin}' dashboard --host '${host}' --port '${port}' > '${log_dir}/cube-dashboard-rollback-${ts}.log' 2>&1 &
EOF
  fi
}
trap rollback_hint ERR

probe() {
  path="$1"
  url="http://${smoke_host}:${port}${path}"
  for _ in $(seq 1 30); do
    if curl -fsS --max-time 2 -o /dev/null "${url}"; then
      return 0
    fi
    if [[ -s "${pid_file}" ]]; then
      pid="$(cat "${pid_file}" 2>/dev/null || true)"
      if [[ "${pid}" =~ ^[0-9]+$ ]] && ! kill -0 "${pid}" 2>/dev/null; then
        echo "dashboard process exited before ${path}; log: ${log_file}" >&2
        rollback_hint
        return 1
      fi
    fi
    sleep 1
  done
  echo "probe failed for ${path}; log: ${log_file}" >&2
  rollback_hint
  return 1
}

stop_listener() {
  local candidates="" trusted_pids="" handled_pids="" pid cmd exe

  if [[ -s "${pid_file}" ]]; then
    trusted_pids+=" $(cat "${pid_file}" 2>/dev/null || true)"
    candidates+="${trusted_pids}"
  fi

  if command -v lsof >/dev/null 2>&1; then
    candidates+=" $(lsof -tiTCP:"${port}" -sTCP:LISTEN 2>/dev/null || true)"
  elif command -v fuser >/dev/null 2>&1; then
    candidates+=" $(fuser "${port}/tcp" 2>/dev/null || true)"
  fi
  if command -v pgrep >/dev/null 2>&1; then
    candidates+=" $(pgrep -f "${remote_bin} dashboard" 2>/dev/null || true)"
  fi

  is_trusted_pid() {
    local candidate="$1" trusted
    for trusted in ${trusted_pids}; do
      [[ "${candidate}" == "${trusted}" ]] && return 0
    done
    return 1
  }

  is_cube_process() {
    local candidate="$1" command="$2" resolved=""
    case "${command}" in
      *"cube dashboard"*|*"bin/cube"*|*"cube-linux-amd64"*)
        return 0
        ;;
    esac
    if [[ -e "/proc/${candidate}/exe" ]]; then
      resolved="$(readlink -f "/proc/${candidate}/exe" 2>/dev/null || true)"
      case "${resolved}" in
        "${remote_bin}"|*"cube-linux-amd64"*|*"bin/cube"*)
          return 0
          ;;
      esac
    fi
    return 1
  }

  for pid in ${candidates}; do
    [[ "${pid}" =~ ^[0-9]+$ ]] || continue
    [[ "${pid}" != "$$" ]] || continue
    case " ${handled_pids} " in
      *" ${pid} "*)
        continue
        ;;
    esac
    handled_pids+=" ${pid}"
    kill -0 "${pid}" 2>/dev/null || continue
    cmd="$(ps -p "${pid}" -o command= 2>/dev/null || true)"
    exe="$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)"
    if is_trusted_pid "${pid}" || is_cube_process "${pid}" "${cmd}"; then
      echo "stopping cube listener on ${port}: pid ${pid} (${cmd:-${exe:-unknown}})"
      kill "${pid}" 2>/dev/null || true
      continue
    fi
    echo "refusing to stop non-cube listener on ${port}: pid ${pid} (${cmd:-${exe:-unknown}})" >&2
    return 1
  done

  for _ in $(seq 1 15); do
    local alive=0
    for pid in ${candidates}; do
      [[ "${pid}" =~ ^[0-9]+$ ]] || continue
      if kill -0 "${pid}" 2>/dev/null; then
        alive=1
      fi
    done
    [[ "${alive}" == "0" ]] && return 0
    sleep 1
  done

  for pid in ${candidates}; do
    [[ "${pid}" =~ ^[0-9]+$ ]] || continue
    kill -0 "${pid}" 2>/dev/null || continue
    kill -KILL "${pid}" 2>/dev/null || true
  done
}

mkdir -p "${tmp_dir}" "${log_dir}" "$(dirname "${remote_bin}")"

if [[ ! -s "${new_bin}" ]]; then
  echo "new binary is missing or empty: ${new_bin}" >&2
  exit 1
fi
if [[ ! -f "${env_file}" ]]; then
  echo "env file is missing: ${env_file}" >&2
  exit 1
fi

chmod 0755 "${new_bin}"
if [[ -e "${remote_bin}" ]]; then
  backup="${remote_bin}.bak-${ts}"
  cp -p "${remote_bin}" "${backup}"
  echo "backup: ${backup}"
fi

mv -f "${new_bin}" "${remote_bin}"
chmod 0755 "${remote_bin}"

stop_listener

set -a
# shellcheck disable=SC1090
. "${env_file}"
set +a

nohup "${remote_bin}" dashboard --host "${host}" --port "${port}" >"${log_file}" 2>&1 &
pid="$!"
echo "${pid}" >"${pid_file}"

probe /healthz
probe /readyz

echo "deploy ok: pid ${pid}; log: ${log_file}"
if [[ -n "${backup}" ]]; then
  echo "rollback source: ${backup}"
fi
REMOTE
}

remote_smoke() {
  log "Smoking current devbox service on ${DEVBOX}:${SMOKE_HOST}:${PROD_PORT}"
  if is_dry_run; then
    log "DRY_RUN: would curl /healthz and /readyz on ${SMOKE_HOST}:${PROD_PORT}"
    return 0
  fi

  ssh_cmd "${DEVBOX}" bash -s -- "${SMOKE_HOST}" "${PROD_PORT}" <<'REMOTE'
set -Eeuo pipefail
host="$1"
port="$2"

probe() {
  path="$1"
  curl -fsS --max-time 5 -o /dev/null "http://${host}:${port}${path}"
  echo "ok ${path}"
}

probe /healthz
probe /readyz
REMOTE
}

main() {
  parse_args "$@"

  if [[ "${SMOKE_ONLY}" == "1" ]]; then
    remote_smoke
    return 0
  fi

  build_binary
  upload_binary
  remote_canary
  remote_deploy
}

main "$@"

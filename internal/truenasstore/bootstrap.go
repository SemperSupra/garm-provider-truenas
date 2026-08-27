package truenasstore

// containerBootstrapScript adapts GARM's JIT metadata contract to the
// container-native runner image. The image already contains the pinned runner
// assets under /runnertmp, so bootstrap only materializes the short-lived JIT
// credentials and starts run.sh in the foreground.
//
// GARM_INSTANCE_TOKEN is deliberately copied into a non-exported shell
// variable and removed from the environment before the runner child starts.
// Auth headers are supplied to curl over stdin rather than in argv. The
// credential files live under RUNNER_HOME and are removed on every normal
// shell exit. The TrueNAS app itself is one-job ephemeral and is retired by the
// provider once it is no longer active.
const containerBootstrapScript = `set -eu
umask 077

: "${GARM_CALLBACK_URL:?GARM_CALLBACK_URL is required}"
: "${GARM_METADATA_URL:?GARM_METADATA_URL is required}"
: "${GARM_INSTANCE_TOKEN:?GARM_INSTANCE_TOKEN is required}"

RUNNER_ASSETS_DIR="${RUNNER_ASSETS_DIR:-/runnertmp}"
RUNNER_HOME="${RUNNER_HOME:-/home/runner/actions-runner}"
MAX_ATTEMPTS="${GARM_BOOTSTRAP_MAX_ATTEMPTS:-5}"
RETRY_DELAY="${GARM_BOOTSTRAP_RETRY_DELAY_SECONDS:-2}"
READY_DELAY="${GARM_BOOTSTRAP_READY_DELAY_SECONDS:-2}"

# Keep the bearer token in this bootstrap shell only. Imported environment
# variables are exported by bash; a fresh assignment is not.
bootstrap_token="$GARM_INSTANCE_TOKEN"
unset GARM_INSTANCE_TOKEN
runner_pid=""

cleanup_credentials() {
  rm -f \
    "$RUNNER_HOME/.runner" \
    "$RUNNER_HOME/.credentials" \
    "$RUNNER_HOME/.credentials_rsaparams" \
    "$RUNNER_HOME/.runner.tmp" \
    "$RUNNER_HOME/.credentials.tmp" \
    "$RUNNER_HOME/.credentials_rsaparams.tmp"
}

terminate_runner() {
  if [ -n "${runner_pid:-}" ]; then
    kill -TERM "$runner_pid" 2>/dev/null || true
    wait "$runner_pid" 2>/dev/null || true
  fi
  exit 143
}

trap cleanup_credentials EXIT
trap terminate_runner INT TERM HUP

status_url="${GARM_CALLBACK_URL%/}"
case "$status_url" in
  */status) ;;
  *) status_url="${status_url}/status" ;;
esac
callback_base="${status_url%/status}"
metadata_base="${GARM_METADATA_URL%/}"

post_json() {
  url="$1"
  payload="$2"
  attempt=1
  while [ "$attempt" -le "$MAX_ATTEMPTS" ]; do
    if printf 'header = "Authorization: Bearer %s"\n' "$bootstrap_token" | \
      curl --config - \
      --fail --silent --show-error \
      --connect-timeout 5 --max-time 30 \
      -X POST \
      -H 'Accept: application/json' \
      -H 'Content-Type: application/json' \
      --data "$payload" \
      "$url" >/dev/null; then
      return 0
    fi
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then
      sleep "$RETRY_DELAY"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

post_status() {
  status="$1"
  message="$2"
  payload=$(printf '{"status":"%s","message":"%s"}' "$status" "$message")
  post_json "$status_url" "$payload"
}

fetch_metadata() {
  path="$1"
  destination="$2"
  tmp="${destination}.tmp"
  rm -f "$tmp"
  attempt=1
  while [ "$attempt" -le "$MAX_ATTEMPTS" ]; do
    if printf 'header = "Authorization: Bearer %s"\n' "$bootstrap_token" | \
      curl --config - \
      --fail --silent --show-error --location \
      --connect-timeout 5 --max-time 30 \
      -H 'Accept: application/json' \
      "${metadata_base}/${path}" \
      -o "$tmp"; then
      if [ -s "$tmp" ]; then
        mv "$tmp" "$destination"
        chmod 600 "$destination"
        return 0
      fi
    fi
    rm -f "$tmp"
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then
      sleep "$RETRY_DELAY"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

post_status installing 'preparing container runner' || exit 20

if [ ! -d "$RUNNER_ASSETS_DIR" ] || [ ! -f "$RUNNER_ASSETS_DIR/run.sh" ]; then
  post_status failed 'pinned runner assets are unavailable' || true
  exit 21
fi

mkdir -p "$RUNNER_HOME"
# Do not use cp -a here. /runnertmp is root-owned in the official image and the
# provider deliberately runs as uid 1001 with no-new-privileges, so preserving
# root ownership would fail. A normal recursive copy creates the ephemeral
# runner tree as the unprivileged runner user while retaining executable bits.
cp -R "$RUNNER_ASSETS_DIR/." "$RUNNER_HOME/"
if [ -d "$RUNNER_HOME/externalstmp" ]; then
  mkdir -p "$RUNNER_HOME/externals"
  cp -R "$RUNNER_HOME/externalstmp/." "$RUNNER_HOME/externals/"
  rm -rf "$RUNNER_HOME/externalstmp"
fi
cd "$RUNNER_HOME"

post_status installing 'fetching JIT runner metadata' || exit 22
fetch_metadata 'credentials/runner' '.runner' || {
  post_status failed 'failed to fetch runner metadata' || true
  exit 23
}
fetch_metadata 'credentials/credentials' '.credentials' || {
  post_status failed 'failed to fetch runner credentials' || true
  exit 24
}
fetch_metadata 'credentials/credentials_rsaparams' '.credentials_rsaparams' || {
  post_status failed 'failed to fetch runner RSA parameters' || true
  exit 25
}

# The runner process must never inherit the GARM bootstrap bearer token.
env -u GARM_INSTANCE_TOKEN ./run.sh &
runner_pid=$!
sleep "$READY_DELAY"
if ! kill -0 "$runner_pid" 2>/dev/null; then
  wait "$runner_pid" 2>/dev/null || true
  post_status failed 'runner exited during bootstrap' || true
  exit 26
fi

os_name=""
os_version=""
if [ -f /etc/os-release ]; then
  os_name=$(sed -n 's/^NAME=//p' /etc/os-release | head -n1 | tr -d '"')
  os_version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | head -n1 | tr -d '"')
fi
agent_id=$(sed -n 's/.*"agentId"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' .runner | head -n1)
if [ -n "$agent_id" ]; then
  system_payload=$(printf '{"os_name":"%s","os_version":"%s","agent_id":%s}' "$os_name" "$os_version" "$agent_id")
  ready_payload=$(printf '{"status":"idle","message":"container runner started","agent_id":%s}' "$agent_id")
else
  system_payload=$(printf '{"os_name":"%s","os_version":"%s","agent_id":null}' "$os_name" "$os_version")
  ready_payload='{"status":"idle","message":"container runner started","agent_id":null}'
fi

post_json "${callback_base}/system-info/" "$system_payload" || {
  kill -TERM "$runner_pid" 2>/dev/null || true
  wait "$runner_pid" 2>/dev/null || true
  exit 27
}
post_json "$status_url" "$ready_payload" || {
  kill -TERM "$runner_pid" 2>/dev/null || true
  wait "$runner_pid" 2>/dev/null || true
  exit 28
}

# Once GARM accepts idle, its instance middleware no longer accepts this token.
# Clear our live copy immediately; only the unavoidable TrueNAS app-config
# residue remains until the ephemeral app is retired.
bootstrap_token=''

set +e
wait "$runner_pid"
runner_rc=$?
set -e
runner_pid=""
exit "$runner_rc"
`

func containerBootstrapCommand() string {
	return containerBootstrapScript
}

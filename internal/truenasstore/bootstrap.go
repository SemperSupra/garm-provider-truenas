package truenasstore

// containerBootstrapScript adapts GARM's JIT metadata contract to a
// container-native runner lifecycle. The pinned actions-runner image provides
// the tested Ubuntu/runtime dependency base, but the runner executable tree is
// installed from the exact, checksummed GitHub runner release selected for this
// MVP. JIT credential bytes live only on a non-executable tmpfs; runner and
// work files use the one-job container writable layer so they remain executable.
const containerBootstrapScript = `set -eu
umask 077

: "${GARM_CALLBACK_URL:?GARM_CALLBACK_URL is required}"
: "${GARM_METADATA_URL:?GARM_METADATA_URL is required}"
: "${GARM_INSTANCE_TOKEN:?GARM_INSTANCE_TOKEN is required}"
: "${GARM_RUNNER_DOWNLOAD_URL:?GARM_RUNNER_DOWNLOAD_URL is required}"
: "${GARM_RUNNER_FILENAME:?GARM_RUNNER_FILENAME is required}"
: "${GARM_RUNNER_SHA256:?GARM_RUNNER_SHA256 is required}"

RUNNER_HOME="${GARM_BOOTSTRAP_RUNNER_HOME:-/home/runner/actions-runner}"
JIT_DIR="${GARM_BOOTSTRAP_JIT_DIR:-/run/garm-jit}"
RUNNER_ARCHIVE="${GARM_BOOTSTRAP_RUNNER_ARCHIVE:-/home/runner/${GARM_RUNNER_FILENAME}}"
MAX_ATTEMPTS="${GARM_BOOTSTRAP_MAX_ATTEMPTS:-5}"
RETRY_DELAY="${GARM_BOOTSTRAP_RETRY_DELAY_SECONDS:-2}"
READY_DELAY="${GARM_BOOTSTRAP_READY_DELAY_SECONDS:-2}"

bootstrap_token="$GARM_INSTANCE_TOKEN"
unset GARM_INSTANCE_TOKEN
runner_pid=""

cleanup_credentials() {
  rm -f \
    "$RUNNER_HOME/.runner" \
    "$RUNNER_HOME/.credentials" \
    "$RUNNER_HOME/.credentials_rsaparams" \
    "$JIT_DIR/.runner" \
    "$JIT_DIR/.credentials" \
    "$JIT_DIR/.credentials_rsaparams" \
    "$JIT_DIR/.runner.tmp" \
    "$JIT_DIR/.credentials.tmp" \
    "$JIT_DIR/.credentials_rsaparams.tmp" \
    "$RUNNER_ARCHIVE" \
    "${RUNNER_ARCHIVE}.tmp"
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
      curl --config - --fail --silent --show-error \
      --connect-timeout 5 --max-time 30 -X POST \
      -H 'Accept: application/json' -H 'Content-Type: application/json' \
      --data "$payload" "$url" >/dev/null; then
      return 0
    fi
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then sleep "$RETRY_DELAY"; fi
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
      curl --config - --fail --silent --show-error --location \
      --connect-timeout 5 --max-time 30 -H 'Accept: application/json' \
      "${metadata_base}/${path}" -o "$tmp"; then
      if [ -s "$tmp" ]; then
        mv "$tmp" "$destination"
        chmod 600 "$destination"
        return 0
      fi
    fi
    rm -f "$tmp"
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then sleep "$RETRY_DELAY"; fi
    attempt=$((attempt + 1))
  done
  return 1
}

download_runner() {
  tmp="${RUNNER_ARCHIVE}.tmp"
  rm -f "$tmp" "$RUNNER_ARCHIVE"
  attempt=1
  while [ "$attempt" -le "$MAX_ATTEMPTS" ]; do
    if curl --fail --silent --show-error --location \
      --connect-timeout 5 --max-time 180 "$GARM_RUNNER_DOWNLOAD_URL" -o "$tmp"; then
      if printf '%s  %s\n' "$GARM_RUNNER_SHA256" "$tmp" | sha256sum -c - >/dev/null 2>&1; then
        mv "$tmp" "$RUNNER_ARCHIVE"
        return 0
      fi
    fi
    rm -f "$tmp"
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then sleep "$RETRY_DELAY"; fi
    attempt=$((attempt + 1))
  done
  return 1
}

post_status installing 'downloading verified runner payload' || exit 20
download_runner || {
  post_status failed 'failed to download or verify runner payload' || true
  exit 21
}

rm -rf "$RUNNER_HOME"
mkdir -p "$RUNNER_HOME" "$JIT_DIR"
tar xzf "$RUNNER_ARCHIVE" -C "$RUNNER_HOME" || {
  post_status failed 'failed to extract runner payload' || true
  exit 22
}
rm -f "$RUNNER_ARCHIVE"

if [ ! -x "$RUNNER_HOME/run.sh" ] || [ ! -x "$RUNNER_HOME/bin/Runner.Listener" ]; then
  post_status failed 'verified runner payload is incomplete' || true
  exit 23
fi
if [ ! -x "$RUNNER_HOME/externals/node24/bin/node" ]; then
  post_status failed 'verified runner payload lacks the required node24 action runtime' || true
  exit 24
fi
cd "$RUNNER_HOME"

post_status installing 'fetching JIT runner metadata' || exit 25
fetch_metadata 'credentials/runner' "$JIT_DIR/.runner" || {
  post_status failed 'failed to fetch runner metadata' || true
  exit 26
}
fetch_metadata 'credentials/credentials' "$JIT_DIR/.credentials" || {
  post_status failed 'failed to fetch runner credentials' || true
  exit 27
}
fetch_metadata 'credentials/credentials_rsaparams' "$JIT_DIR/.credentials_rsaparams" || {
  post_status failed 'failed to fetch runner RSA parameters' || true
  exit 28
}

ln -s "$JIT_DIR/.runner" .runner
ln -s "$JIT_DIR/.credentials" .credentials
ln -s "$JIT_DIR/.credentials_rsaparams" .credentials_rsaparams

# The runner process must never inherit the GARM bootstrap bearer token.
env -u GARM_INSTANCE_TOKEN ./run.sh &
runner_pid=$!
sleep "$READY_DELAY"
if ! kill -0 "$runner_pid" 2>/dev/null; then
  wait "$runner_pid" 2>/dev/null || true
  post_status failed 'runner exited during bootstrap' || true
  exit 29
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
  exit 30
}
post_json "$status_url" "$ready_payload" || {
  kill -TERM "$runner_pid" 2>/dev/null || true
  wait "$runner_pid" 2>/dev/null || true
  exit 31
}

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

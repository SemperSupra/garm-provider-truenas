package truenasstore

import "strings"

// containerBootstrapScript adapts GARM's JIT metadata contract to a
// container-native runner lifecycle. The pinned actions-runner image provides
// the tested Ubuntu/runtime dependency base, but the runner executable tree is
// installed from the exact, checksummed GitHub runner release selected for this
// MVP. JIT credential bytes live only on a non-executable tmpfs; runner and
// work files use the one-job container writable layer so they remain executable.
//
// The digest-pinned runner image already ships the complete runner tree for the
// pinned release under /home/runner. When that image-resident tree is complete
// and its Runner.Listener reports exactly the version encoded in the verified
// GARM_RUNNER_FILENAME, the bootstrap reuses it in place instead of fetching the
// ~215 MB release archive over the WAN on every ephemeral boot. Integrity of the
// reused tree derives from the provider-enforced immutable image digest. Any
// incompleteness or version disagreement falls back to the original checksummed
// download path, which remains the fail-closed authority.
const containerBootstrapScript = `set -eu
umask 077

: "${GARM_CALLBACK_URL:?GARM_CALLBACK_URL is required}"
: "${GARM_METADATA_URL:?GARM_METADATA_URL is required}"
: "${GARM_INSTANCE_TOKEN:?GARM_INSTANCE_TOKEN is required}"
: "${GARM_RUNNER_DOWNLOAD_URL:?GARM_RUNNER_DOWNLOAD_URL is required}"
: "${GARM_RUNNER_FILENAME:?GARM_RUNNER_FILENAME is required}"
: "${GARM_RUNNER_SHA256:?GARM_RUNNER_SHA256 is required}"

RUNNER_HOME="${GARM_BOOTSTRAP_RUNNER_HOME:-/home/runner/actions-runner}"
PRESTAGED_RUNNER_HOME="${GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME-/home/runner}"
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

expected_runner_version=$(printf '%s' "$GARM_RUNNER_FILENAME" | \
  sed -n 's/^actions-runner-linux-x64-\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)\.tar\.gz$/\1/p')

prestaged_runner_matches() {
  home="$1"
  [ -n "$home" ] || return 1
  [ -n "$expected_runner_version" ] || return 1
  [ -x "$home/run.sh" ] || return 1
  [ -x "$home/bin/Runner.Listener" ] || return 1
  [ -x "$home/externals/node24/bin/node" ] || return 1
  [ -w "$home" ] || return 1
  [ ! -e "$home/.runner" ] && [ ! -L "$home/.runner" ] || return 1
  [ ! -e "$home/.credentials" ] && [ ! -L "$home/.credentials" ] || return 1
  [ ! -e "$home/.credentials_rsaparams" ] && [ ! -L "$home/.credentials_rsaparams" ] || return 1
  actual_version=$(env -u GARM_INSTANCE_TOKEN "$home/bin/Runner.Listener" --version 2>/dev/null | \
    tr -d '\r' | sed -n '1p') || return 1
  [ "$actual_version" = "$expected_runner_version" ]
}

if prestaged_runner_matches "$PRESTAGED_RUNNER_HOME"; then
  RUNNER_HOME="$PRESTAGED_RUNNER_HOME"
  post_status installing 'using image-resident runner payload' || exit 20
  mkdir -p "$JIT_DIR"
else
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
fi

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

// containerBootstrapCommand returns the Compose-safe representation of the
// runtime shell program. Compose interpolates dollar-prefixed expressions in
// scalar values before the container is created; doubling each dollar causes
// Compose to pass one literal dollar through so the shell expands variables at
// runtime inside the runner container instead of against the TrueNAS host.
func containerBootstrapCommand() string {
	return strings.ReplaceAll(containerBootstrapScript, "$", "$$")
}

// containerBootstrapRuntimeCommand is the raw shell program used by unit tests
// that execute the bootstrap directly without a Compose interpolation layer.
func containerBootstrapRuntimeCommand() string {
	return containerBootstrapScript
}

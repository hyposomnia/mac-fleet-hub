#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-start}"
FIXTURE_DIR="${2:-}"
IMAGE="headscale/headscale:0.26.1"
NATIVE_BINARY="${HEADSCALE_FIXTURE_BINARY:-}"

case "$ACTION" in
  help|--help|-h)
    cat <<'HELP'
Isolated Headscale 0.26.1 fixture; no system services or production clients.

Docker:
  docker pull headscale/headscale:0.26.1
  bash scripts/headscale-multiuser-fixture.sh start

Native fallback, from the official v0.26.1 source checkout:
  GOPROXY=https://goproxy.cn go build -o /private/tmp/headscale-0.26.1 ./cmd/headscale
  HEADSCALE_FIXTURE_BINARY=/private/tmp/headscale-0.26.1 HEADSCALE_FIXTURE_FOREGROUND=1 bash scripts/headscale-multiuser-fixture.sh start

Start prints the fixture directory, loopback URL, and private API key file path.
It configures policy.mode=database and keeps all state under /private/tmp.
Keep native foreground mode running in its terminal while testing.

Smoke test, from server/enroll, using the printed paths:
  HEADSCALE_UAT_URL=http://127.0.0.1:PORT HEADSCALE_UAT_KEY_FILE=/private/tmp/macfleet-headscale-uat.SUFFIX/api-key go test -race -run TestHeadscaleLocalIntegration -v ./multiuser

Optional live key attribution uses a fresh userspace daemon, private socket,
and temporary state. It never uses the installed client's default socket:
  HEADSCALE_UAT_TAILSCALE=/path/to/tailscale HEADSCALE_UAT_TAILSCALED=/path/to/tailscaled HEADSCALE_UAT_URL=http://127.0.0.1:PORT HEADSCALE_UAT_KEY_FILE=/private/tmp/macfleet-headscale-uat.SUFFIX/api-key go test -race -run TestHeadscaleLiveKeyAttribution -v ./multiuser

Lifecycle:
  bash scripts/headscale-multiuser-fixture.sh status /private/tmp/macfleet-headscale-uat.SUFFIX
  bash scripts/headscale-multiuser-fixture.sh stop /private/tmp/macfleet-headscale-uat.SUFFIX

HTTP schemas are protobuf JSON: IDs are decimal strings; node attribution is
preAuthKey.id, addresses are ipAddresses, and timestamps are RFC3339 or null.
PUT /api/v1/policy accepts {"policy":"<JSON policy text>"}.
NewHeadscale(base, key, gatewayIP, servicePorts ...int) accepts ports 1..65535;
omitting ports retains 7681/8080/7682. Custom ports replace those defaults.
Missing-node expiry returns HTTP 500 in 0.26.1; inventory confirms absence.
No key values are printed. Fixture state remains available after stop.
HELP
    ;;
  start)
    [[ -z "$FIXTURE_DIR" ]] || { echo 'start creates its own /private/tmp directory' >&2; exit 1; }
    FIXTURE_DIR="$(mktemp -d /private/tmp/macfleet-headscale-uat.XXXXXX)"
    chmod 700 "$FIXTURE_DIR"
    mkdir "$FIXTURE_DIR/data"
    cat > "$FIXTURE_DIR/config.yaml" <<'YAML'
server_url: http://127.0.0.1:8080
listen_addr: 0.0.0.0:8080
metrics_listen_addr: 127.0.0.1:9090
grpc_listen_addr: 127.0.0.1:50443
grpc_allow_insecure: false
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: false
  urls: []
  paths:
    - /etc/headscale/derp.yaml
  auto_update_enabled: false
disable_check_updates: true
ephemeral_node_inactivity_timeout: 30m
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
    write_ahead_log: true
policy:
  mode: database
dns:
  magic_dns: false
  base_domain: fixture.invalid
  override_local_dns: false
log:
  level: warn
unix_socket: /var/lib/headscale/headscale.sock
unix_socket_permission: "0600"
YAML
    cat > "$FIXTURE_DIR/derp.yaml" <<'YAML'
regions:
  999:
    regionid: 999
    regioncode: fixture
    regionname: Isolated fixture
    nodes:
      - name: fixture
        regionid: 999
        hostname: fixture.invalid
        derpport: 443
        stunport: -1
YAML
    if [[ -n "$NATIVE_BINARY" ]]; then
      [[ "$NATIVE_BINARY" == /private/tmp/* && -x "$NATIVE_BINARY" ]] || { echo 'native fixture binary must be executable under /private/tmp' >&2; exit 1; }
      PORT="$(python3 -c 'import socket; listener=socket.socket(); listener.bind(("127.0.0.1",0)); print(listener.getsockname()[1]); listener.close()')"
      sed -e "s|/var/lib/headscale|$FIXTURE_DIR/data|g" -e "s|/etc/headscale|$FIXTURE_DIR|g" \
        -e "s|0.0.0.0:8080|127.0.0.1:$PORT|" -e "s|127.0.0.1:8080|127.0.0.1:$PORT|" \
        -e 's|127.0.0.1:9090|127.0.0.1:0|' -e 's|127.0.0.1:50443|127.0.0.1:0|' \
        "$FIXTURE_DIR/config.yaml" > "$FIXTURE_DIR/native-config.yaml"
      mv "$FIXTURE_DIR/native-config.yaml" "$FIXTURE_DIR/config.yaml"
      printf '%s\n' "$NATIVE_BINARY" > "$FIXTURE_DIR/binary"
      nohup "$NATIVE_BINARY" --config "$FIXTURE_DIR/config.yaml" serve > "$FIXTURE_DIR/server.log" 2>&1 &
      printf '%s\n' "$!" > "$FIXTURE_DIR/pid"
    else
      CONTAINER="macfleet-headscale-uat-$(basename "$FIXTURE_DIR" | tr '.' '-')"
      printf '%s\n' "$CONTAINER" > "$FIXTURE_DIR/container"
      docker run -d --name "$CONTAINER" --label macfleet.fixture=headscale-multiuser \
        -p 127.0.0.1::8080 \
        -v "$FIXTURE_DIR:/etc/headscale:ro" -v "$FIXTURE_DIR/data:/var/lib/headscale" \
        "$IMAGE" serve > "$FIXTURE_DIR/container-id"
      PORT="$(docker port "$CONTAINER" 8080/tcp | sed -n 's/^127\.0\.0\.1://p')"
    fi
    [[ "$PORT" =~ ^[0-9]+$ ]]
    URL="http://127.0.0.1:$PORT"
    printf '%s\n' "$URL" > "$FIXTURE_DIR/url"
    READY=0
    for ATTEMPT in $(seq 1 30); do
      if curl -fsS --max-time 2 "$URL/health" >/dev/null 2>&1; then READY=1; break; fi
      sleep 1
    done
    if [[ "$READY" != 1 ]]; then
      if [[ -n "$NATIVE_BINARY" ]]; then
        cat "$FIXTURE_DIR/server.log" >&2
      else
        docker logs "$CONTAINER" >&2
        docker rm -f "$CONTAINER" >/dev/null
      fi
      exit 1
    fi
    umask 077
    if [[ -n "$NATIVE_BINARY" ]]; then
      "$NATIVE_BINARY" --config "$FIXTURE_DIR/config.yaml" apikeys create --expiration 1h > "$FIXTURE_DIR/api-key"
    else
      docker exec "$CONTAINER" headscale apikeys create --expiration 1h > "$FIXTURE_DIR/api-key"
    fi
    printf 'Fixture directory: %s\nHeadscale URL: %s\nAPI key file: %s/api-key\n' "$FIXTURE_DIR" "$URL" "$FIXTURE_DIR"
    if [[ -n "$NATIVE_BINARY" && "${HEADSCALE_FIXTURE_FOREGROUND:-0}" == 1 ]]; then
      wait "$(cat "$FIXTURE_DIR/pid")"
    fi
    ;;
  stop|status)
    [[ "$FIXTURE_DIR" == /private/tmp/macfleet-headscale-uat.* && -d "$FIXTURE_DIR" ]] || { echo 'invalid fixture directory' >&2; exit 1; }
    if [[ -f "$FIXTURE_DIR/pid" ]]; then
      PID="$(cat "$FIXTURE_DIR/pid")"
      NATIVE_BINARY="$(cat "$FIXTURE_DIR/binary")"
      [[ "$PID" =~ ^[0-9]+$ && "$NATIVE_BINARY" == /private/tmp/* ]] || { echo 'invalid fixture process' >&2; exit 1; }
      [[ "$(ps -p "$PID" -o command=)" == "$NATIVE_BINARY --config $FIXTURE_DIR/config.yaml serve" ]] || { echo 'process is not this fixture' >&2; exit 1; }
      if [[ "$ACTION" == stop ]]; then
        kill "$PID"
      else
        curl -fsS --max-time 5 "$(cat "$FIXTURE_DIR/url")/health"
      fi
      exit 0
    fi
    [[ -f "$FIXTURE_DIR/container" ]] || { echo 'missing fixture container' >&2; exit 1; }
    CONTAINER="$(cat "$FIXTURE_DIR/container")"
    [[ "$CONTAINER" == macfleet-headscale-uat-* ]] || { echo 'invalid fixture container' >&2; exit 1; }
    [[ "$(docker inspect --format '{{index .Config.Labels "macfleet.fixture"}}' "$CONTAINER")" == headscale-multiuser ]] || { echo 'container is not this fixture' >&2; exit 1; }
    if [[ "$ACTION" == stop ]]; then
      docker rm -f "$CONTAINER"
    else
      docker inspect --format '{{.State.Status}}' "$CONTAINER"
      curl -fsS --max-time 5 "$(cat "$FIXTURE_DIR/url")/health"
    fi
    ;;
  *) echo 'usage: bash scripts/headscale-multiuser-fixture.sh help | start | status <fixture-dir> | stop <fixture-dir>' >&2; exit 1 ;;
esac

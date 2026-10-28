#!/usr/bin/env bash
# Cross-process destination API check: a real settings-service and a real lsc
# talk over a throwaway redis. Covers slot allocation, UUID lifecycle,
# creation-timestamp preservation, whole-record deletion, shortcut-item
# pruning, boot healing, and TOML persistence across a restart.
#
# Usage: test/e2e-destination.sh          (from the settings-service repo)
# Env:   PORT     redis port          (default 6399)
#        LSC_SRC  lsc repo checkout   (default ../lsc-motion)

set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-6399}"
LSC_SRC="${LSC_SRC:-../lsc-motion}"
ADDR="127.0.0.1:${PORT}"
CLI=(redis-cli -p "$PORT")

WORK="$(mktemp -d)"
SS_PID=""
REDIS_PID=""

cleanup() {
    [ -n "$SS_PID" ] && kill "$SS_PID" 2>/dev/null || true
    [ -n "$REDIS_PID" ] && kill "$REDIS_PID" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
    echo "FAIL: $*" >&2
    if [ -n "$SS_PID" ]; then
        echo "--- settings-service log tail ---" >&2
        tail -20 "$(ls -t "$WORK"/settings-service.*.log 2>/dev/null | head -1)" >&2 2>/dev/null || true
        echo "--- settings.toml ---" >&2
        cat "$WORK/settings.toml" >&2 2>/dev/null || echo "(missing)" >&2
    fi
    exit 1
}

expect_eq() { # actual want label
    [ "$1" = "$2" ] || fail "$3: got '$1', want '$2'"
}

redis_up() { "${CLI[@]}" ping 2>/dev/null | grep -q PONG; }

if redis_up; then
    fail "port $PORT already has a redis; set PORT to a free one"
fi

echo "building settings-service and lsc..."
go build -o "$WORK/settings-service" ./cmd/settings-service
(cd "$LSC_SRC" && go build -o "$WORK/lsc" .)

echo "starting redis on $PORT..."
redis-server --port "$PORT" --dir "$WORK" --dbfilename dump.rdb \
    --save '' --appendonly no >/dev/null 2>&1 &
REDIS_PID=$!
for _ in $(seq 1 50); do
    redis_up && break
    sleep 0.1
done
redis_up || fail "redis did not start"

start_settings() { # label
    REDIS_ADDR="$ADDR" "$WORK/settings-service" \
        --settings-file "$WORK/settings.toml" --schema settings.schema.json \
        >"$WORK/settings-service.$1.log" 2>&1 &
    SS_PID=$!
    for _ in $(seq 1 100); do
        if "${CLI[@]}" HGET settings dashboard.mode 2>/dev/null | grep -q speedometer; then
            return
        fi
        kill -0 "$SS_PID" 2>/dev/null || fail "settings-service died: $(tail -5 "$WORK/settings-service.$1.log")"
        sleep 0.1
    done
    fail "settings-service did not hydrate: $(tail -5 "$WORK/settings-service.$1.log")"
}

hget() { "${CLI[@]}" HGET settings "$1"; }
publish() { "${CLI[@]}" PUBLISH settings "$1" >/dev/null; }

LSC=("$WORK/lsc" --redis-addr "$ADDR")

start_settings boot1
echo "settings-service up"

echo "adding locations through lsc..."
"${LSC[@]}" loc add 52.5 13.4 Home >/dev/null
"${LSC[@]}" loc add 52.6 13.5 Work >/dev/null

expect_eq "$(hget dashboard.saved-locations.0.label)" "Home" "first allocation"
expect_eq "$(hget dashboard.saved-locations.0.latitude)" "52.5000000" "coordinate format"
expect_eq "$(hget dashboard.saved-locations.1.label)" "Work" "second allocation"
UID0="$(hget dashboard.saved-locations.0.uuid)"
UID1="$(hget dashboard.saved-locations.1.uuid)"
echo "$UID0" | grep -Eq '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' \
    || fail "slot 0 uuid '$UID0' is not canonical"
[ -n "$UID1" ] && [ "$UID0" != "$UID1" ] || fail "slots share a uuid"

echo "configuring a shortcut item for slot 0..."
ITEMS_DEST="[\"view\",\"destination:$UID0:home\",\"theme\"]"
"${CLI[@]}" HSET settings dashboard.shortcut-menu.items "$ITEMS_DEST" >/dev/null
publish dashboard.shortcut-menu.items

echo "editing preserves identity..."
CREATED0="$(hget dashboard.saved-locations.0.created-at)"
"${LSC[@]}" loc edit 0 label "Home Sweet Home" >/dev/null
expect_eq "$(hget dashboard.saved-locations.0.label)" "Home Sweet Home" "edited label"
expect_eq "$(hget dashboard.saved-locations.0.uuid)" "$UID0" "uuid after edit"
expect_eq "$(hget dashboard.saved-locations.0.created-at)" "$CREATED0" "created-at after edit"

echo "waiting for TOML persistence..."
for _ in $(seq 1 50); do
    grep -q "Home Sweet Home" "$WORK/settings.toml" 2>/dev/null && break
    sleep 0.1
done
grep -q "Home Sweet Home" "$WORK/settings.toml" || fail "edited record never reached settings.toml"
grep -q "$UID0" "$WORK/settings.toml" || fail "uuid never reached settings.toml"

echo "touch bumps last-used..."
"${CLI[@]}" HSET settings dashboard.saved-locations.1.last-used-at \
    "2020-01-01T00:00:00Z" >/dev/null
"${LSC[@]}" loc touch 1 >/dev/null
TOUCHED="$(hget dashboard.saved-locations.1.last-used-at)"
[ -n "$TOUCHED" ] && [ "$TOUCHED" != "2020-01-01T00:00:00Z" ] \
    || fail "touch left last-used-at at '$TOUCHED'"

echo "delete clears the record and prunes its item..."
"${CLI[@]}" HSET settings dashboard.saved-locations.0.quick-slot "1" >/dev/null
"${CLI[@]}" HSET settings dashboard.saved-locations.0.quick-icon "home" >/dev/null
"${LSC[@]}" loc delete 0 >/dev/null
for field in latitude longitude label uuid created-at last-used-at quick-slot quick-icon; do
    LEFT="$(hget "dashboard.saved-locations.0.$field")"
    [ -z "$LEFT" ] || fail "record field $field survived delete ('$LEFT')"
done
expect_eq "$(hget dashboard.shortcut-menu.items)" "[\"view\",\"theme\"]" "pruned items"
expect_eq "$(hget dashboard.saved-locations.1.label)" "Work" "delete touched the wrong record"

for _ in $(seq 1 50); do
    grep -q "Home Sweet Home" "$WORK/settings.toml" 2>/dev/null || break
    sleep 0.1
done
if grep -q "Home Sweet Home" "$WORK/settings.toml" 2>/dev/null; then
    fail "deleted record still in settings.toml"
fi
grep -q "Work" "$WORK/settings.toml" || fail "surviving record left settings.toml"
grep -q "$UID1" "$WORK/settings.toml" || fail "surviving uuid left settings.toml"

echo "deleting a missing id reports not found..."
MISSING="$("${LSC[@]}" loc delete 9 2>&1 || true)"
echo "$MISSING" | grep -qi "not found" || fail "delete of missing id said: $MISSING"

echo "restarting settings-service over a uuid-less record..."
kill "$SS_PID"
wait "$SS_PID" 2>/dev/null || true
SS_PID=""
cat >> "$WORK/settings.toml" <<'EOF'

[dashboard.saved-locations.5]
latitude = "10.0000000"
longitude = "20.0000000"
label = "HealMe"
EOF
start_settings boot2
HEALED=""
for _ in $(seq 1 50); do
    HEALED="$(hget dashboard.saved-locations.5.uuid)"
    [ -n "$HEALED" ] && break
    sleep 0.1
done
echo "$HEALED" | grep -Eq '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' \
    || fail "boot heal did not assign slot 5 a uuid (got '$HEALED')"
for _ in $(seq 1 50); do
    grep -q "$HEALED" "$WORK/settings.toml" 2>/dev/null && break
    sleep 0.1
done
grep -q "$HEALED" "$WORK/settings.toml" || fail "healed uuid never persisted to settings.toml"
expect_eq "$(hget dashboard.saved-locations.1.label)" "Work" "restart kept other records"

echo "PASS: destination e2e"

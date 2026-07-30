#!/usr/bin/env bash
# OAM instance-snapshot E2E test.
# Runs ON the OAM node against the live gateway — never against mocks.
# Mutates live config (creates/deletes an LB rule); leaves everything clean.
#
# Usage: OAM=http://localhost:8080 ADMIN_USER=admin ADMIN_PASS=... ./snapshot-oam-e2e.sh
set -u

OAM="${OAM:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:?set ADMIN_PASS}"
INSTANCE_ID="${INSTANCE_ID:-1}"
MYSQL="docker exec oam-mysql mysql -uoamuser -p${DB_PASSWORD:?DB_PASSWORD must be set} loxioam -N -s -e"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS: $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL: $1"; }
check() { # check <desc> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected [$2] got [$3])"; fi
}

TMP=$(mktemp -d /tmp/snap-e2e.XXXXXX)
trap 'rm -rf "$TMP"' EXIT

echo "== login"
TOKEN=$(curl -s -X POST "$OAM/oam/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | jq -r .token)
[ -n "$TOKEN" ] && [ "$TOKEN" != "null" ] && ok "admin login" || { bad "admin login"; exit 1; }
AUTH="Authorization: Bearer $TOKEN"

gw() { curl -s -H "$AUTH" "$OAM/oam/loxilbs/$INSTANCE_ID/netlox/v1$1"; }
lb_count() { gw /config/loadbalancer/all | jq '.lbAttr | length'; }

echo "== S0: seed one LB rule so the snapshot round-trip is non-trivial"
curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" -H 'Content-Type: application/json' \
  "$OAM/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer" \
  -d '{"serviceArguments":{"externalIP":"20.20.20.99","port":8899,"protocol":"tcp","sel":0,"mode":0,"BGP":false,"Monitor":false},"endpoints":[{"endpointIP":"31.31.31.99","targetPort":8899,"weight":1}]}' > "$TMP/seed"
check "seed LB created (200)" "200" "$(cat "$TMP/seed")"
check "gateway has 1 LB" "1" "$(lb_count)"

echo "== S1: take -> list -> get -> download -> checksum"
TAKE=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
  "$OAM/oam/instances/$INSTANCE_ID/snapshots" -d '{"name":"e2e-take-1","description":"e2e"}')
SID=$(echo "$TAKE" | jq -r .id)
[ ${#SID} -eq 36 ] && ok "take returns uuid" || bad "take returns uuid ($TAKE)"
check "take: encrypted at rest" "true" "$(echo "$TAKE" | jq -r .encrypted)"
check "take: trigger manual" "manual" "$(echo "$TAKE" | jq -r .trigger_type)"
echo "$TAKE" | jq -e '.checksum | startswith("sha256:")' >/dev/null && ok "take: gateway checksum present" || bad "take: gateway checksum present"
echo "$TAKE" | grep -q snapshot_blob && bad "metadata leaks blob" || ok "metadata has no blob"

LIST=$(curl -s -H "$AUTH" "$OAM/oam/instances/$INSTANCE_ID/snapshots?page=1&limit=10")
echo "$LIST" | jq -e --arg s "$SID" '.data[] | select(.id==$s)' >/dev/null && ok "list contains snapshot" || bad "list contains snapshot"

HDRS=$(curl -s -D - -o "$TMP/snap.json" -H "$AUTH" "$OAM/oam/snapshots/$SID/download")
FILE_SHA="sha256:$(sha256sum "$TMP/snap.json" | cut -d' ' -f1)"
HDR_SHA=$(echo "$HDRS" | grep -i '^x-content-checksum:' | tr -d '\r' | awk '{print $2}')
check "download: sha256(file) == X-Content-Checksum" "$FILE_SHA" "$HDR_SHA"
DOC_SUM=$(jq -r .checksum "$TMP/snap.json")
GW_HDR=$(echo "$HDRS" | grep -i '^x-snapshot-checksum:' | tr -d '\r' | awk '{print $2}')
check "download: X-Snapshot-Checksum == document checksum" "$DOC_SUM" "$GW_HDR"
echo "$HDRS" | grep -qi 'content-disposition: attachment' && ok "download: attachment disposition" || bad "download: attachment disposition"

echo "== S2: off-box upload round-trip"
UP=$(curl -s -X POST -H "$AUTH" -F "file=@$TMP/snap.json" -F "name=e2e-upload-1" \
  "$OAM/oam/instances/$INSTANCE_ID/snapshots/upload")
UPID=$(echo "$UP" | jq -r .id)
[ ${#UPID} -eq 36 ] && ok "upload accepted" || bad "upload accepted ($UP)"
check "upload: same gateway checksum" "$DOC_SUM" "$(echo "$UP" | jq -r .checksum)"
GARBAGE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" -F "file=@/etc/hostname" \
  "$OAM/oam/instances/$INSTANCE_ID/snapshots/upload")
check "upload: non-snapshot file rejected 400" "400" "$GARBAGE"

echo "== S3: restore dry-run then commit (delete LB first, restore brings it back)"
DEL=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AUTH" \
  "$OAM/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer/externalipaddress/20.20.20.99/port/8899/protocol/tcp")
check "LB deleted (200)" "200" "$DEL"
check "gateway has 0 LB" "0" "$(lb_count)"

DRY=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' "$OAM/oam/snapshots/$SID/restore" -d '{}')
check "dry-run: mode" "dry-run" "$(echo "$DRY" | jq -r .mode)"
check "dry-run: gateway 200" "200" "$(echo "$DRY" | jq -r .gateway_status)"
check "dry-run: compatible" "true" "$(echo "$DRY" | jq -r .gateway_response.compatible)"
check "dry-run: did not mutate (0 LB)" "0" "$(lb_count)"
check "dry-run: no pre-restore snapshot" "null" "$(echo "$DRY" | jq -r .pre_restore_snapshot_id)"
check "dry-run: restore_count still 0" "0" "$(curl -s -H "$AUTH" "$OAM/oam/snapshots/$SID" | jq -r .restore_count)"

COMMIT=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' "$OAM/oam/snapshots/$SID/restore" -d '{"mode":"commit"}')
check "commit: gateway 200" "200" "$(echo "$COMMIT" | jq -r .gateway_status)"
check "commit: result ok" "ok" "$(echo "$COMMIT" | jq -r .gateway_response.result)"
PREID=$(echo "$COMMIT" | jq -r .pre_restore_snapshot_id)
[ ${#PREID} -eq 36 ] && ok "commit: pre-restore snapshot taken" || bad "commit: pre-restore snapshot taken ($COMMIT)"
check "commit: LB is back" "1" "$(lb_count)"
check "commit: pre-restore trigger type" "pre_restore" "$(curl -s -H "$AUTH" "$OAM/oam/snapshots/$PREID" | jq -r .trigger_type)"
META=$(curl -s -H "$AUTH" "$OAM/oam/snapshots/$SID")
check "commit: restore_count bumped" "1" "$(echo "$META" | jq -r .restore_count)"
check "commit: last_restore_result recorded" "ok" "$(echo "$META" | jq -r .last_restore_result)"
echo "$META" | jq -e '.last_restore_response | length > 0' >/dev/null && ok "commit: audit response stored" || bad "commit: audit response stored"

echo "== S4: tampered blob rejected before touching the gateway"
$MYSQL "UPDATE instance_snapshots SET snapshot_blob = CONCAT(X'FF', SUBSTRING(snapshot_blob, 2)) WHERE id='$UPID';"
TAMPER_DL=$(curl -s -o "$TMP/tamper" -w '%{http_code}' -H "$AUTH" "$OAM/oam/snapshots/$UPID/download")
check "tampered download rejected 422" "422" "$TAMPER_DL"
TAMPER_RS=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" -H 'Content-Type: application/json' \
  "$OAM/oam/snapshots/$UPID/restore" -d '{"mode":"commit"}')
check "tampered restore rejected 422" "422" "$TAMPER_RS"
check "tampered row flagged checksum_ok=false" "false" "$(curl -s -H "$AUTH" "$OAM/oam/snapshots/$UPID" | jq -r .checksum_ok)"
check "gateway untouched by tampered restore (1 LB)" "1" "$(lb_count)"

echo "== S5: pin protection + patch"
PIN=$(curl -s -X PATCH -H "$AUTH" -H 'Content-Type: application/json' "$OAM/oam/snapshots/$SID" -d '{"pinned":true}')
check "patch: pinned" "true" "$(echo "$PIN" | jq -r .pinned)"
DEL1=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AUTH" "$OAM/oam/snapshots/$SID")
check "pinned delete blocked 409" "409" "$DEL1"
DEL2=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AUTH" "$OAM/oam/snapshots/$SID?force=true")
check "pinned delete with force 200" "200" "$DEL2"

echo "== S6: RBAC — viewer can read, cannot write/download"
VPASS='E2eViewer1!'
curl -s -o /dev/null -X POST -H "$AUTH" -H 'Content-Type: application/json' "$OAM/oam/users" \
  -d "{\"username\":\"e2e_snap_viewer\",\"email\":\"v@e2e.local\",\"password\":\"$VPASS\",\"role\":\"viewer\"}"
VTOKEN=$(curl -s -X POST "$OAM/oam/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"e2e_snap_viewer\",\"password\":\"$VPASS\"}" | jq -r .token)
[ -n "$VTOKEN" ] && [ "$VTOKEN" != "null" ] && ok "viewer login" || bad "viewer login"
VAUTH="Authorization: Bearer $VTOKEN"
check "viewer list 200" "200" "$(curl -s -o /dev/null -w '%{http_code}' -H "$VAUTH" "$OAM/oam/instances/$INSTANCE_ID/snapshots")"
check "viewer take 403" "403" "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$VAUTH" "$OAM/oam/instances/$INSTANCE_ID/snapshots")"
check "viewer download 403" "403" "$(curl -s -o /dev/null -w '%{http_code}' -H "$VAUTH" "$OAM/oam/snapshots/$PREID/download")"
check "viewer restore 403" "403" "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$VAUTH" "$OAM/oam/snapshots/$PREID/restore")"

echo "== S7: gateway-down passthrough (dead instance, no OAM-invented message)"
DEADINST=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' "$OAM/oam/loxilbs" \
  -d '{"name":"e2e-dead","host":"10.0.0.12","port":"19999","protocol":"http","version":"v1","cimage":"x","ctag":"x","description":"e2e dead"}')
DEADID=$($MYSQL "SELECT id FROM loxilb_instances WHERE name='e2e-dead' ORDER BY id DESC LIMIT 1;")
DEADRES=$(curl -s -w '\n%{http_code}' -X POST -H "$AUTH" "$OAM/oam/instances/$DEADID/snapshots")
DEADCODE=$(echo "$DEADRES" | tail -1)
check "dead gateway take -> 502" "502" "$DEADCODE"
echo "$DEADRES" | head -1 | grep -q 'connection refused' && ok "502 carries raw connection error" || bad "502 carries raw connection error ($(echo "$DEADRES" | head -1))"

echo "== S8: schedule PUT/GET + scheduler tick (waits up to 6 min)"
SCHED=$(curl -s -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  "$OAM/oam/instances/$INSTANCE_ID/snapshot-schedule" -d '{"enabled":true,"interval_hours":1,"retain_count":3}')
check "schedule saved: enabled" "true" "$(echo "$SCHED" | jq -r .enabled)"
check "schedule saved: retain 3" "3" "$(echo "$SCHED" | jq -r .retain_count)"
# never ran -> due on next 5-min tick; also pre-seed extra snapshots so
# retention has something to trim (retain=3).
for i in 1 2 3; do
  curl -s -o /dev/null -X POST -H "$AUTH" -H 'Content-Type: application/json' \
    "$OAM/oam/instances/$INSTANCE_ID/snapshots" -d "{\"name\":\"e2e-filler-$i\"}"
done
DEADLINE=$(( $(date +%s) + 360 ))
RAN=""
while [ $(date +%s) -lt $DEADLINE ]; do
  RAN=$(curl -s -H "$AUTH" "$OAM/oam/instances/$INSTANCE_ID/snapshot-schedule" | jq -r .last_run_at)
  [ "$RAN" != "null" ] && break
  sleep 15
done
[ "$RAN" != "null" ] && ok "scheduler fired (last_run_at=$RAN)" || bad "scheduler fired within 6 min"
SCHEDSNAP=$(curl -s -H "$AUTH" "$OAM/oam/instances/$INSTANCE_ID/snapshots?limit=50" | jq '[.data[] | select(.trigger_type=="scheduled")] | length')
[ "$SCHEDSNAP" -ge 1 ] && ok "scheduled snapshot exists" || bad "scheduled snapshot exists"
UNPINNED=$($MYSQL "SELECT COUNT(*) FROM instance_snapshots WHERE instance_id=$INSTANCE_ID AND pinned=FALSE AND trigger_type <> 'pre_upgrade';")
[ "$UNPINNED" -le 3 ] && ok "retention trimmed to retain_count ($UNPINNED)" || bad "retention trimmed to retain_count (have $UNPINNED, want <=3)"

echo "== S9: container-rebuild survival (the legacy killer)"
SURVIVOR=$($MYSQL "SELECT id FROM instance_snapshots WHERE instance_id=$INSTANCE_ID ORDER BY created_at DESC LIMIT 1;")
docker rm -f loxioam >/dev/null
docker run -d --name loxioam --network oam-net -p 8080:8080 \
  -e DB_HOST=oam-mysql -e DB_PORT=3306 -e DB_USER=oamuser -e DB_PASSWORD="${DB_PASSWORD:?DB_PASSWORD must be set}" -e DB_NAME=loxioam \
  -e OAM_JWT_SECRET="${OAM_JWT_SECRET:?set OAM_JWT_SECRET}" \
  -e SNAPSHOT_ENC_KEY="$(cat /root/.snapshot_enc_key)" loxilb-oam:latest >/dev/null
sleep 5
TOKEN=$(curl -s -X POST "$OAM/oam/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | jq -r .token)
AUTH="Authorization: Bearer $TOKEN"
DLCODE=$(curl -s -o "$TMP/survivor.json" -w '%{http_code}' -H "$AUTH" "$OAM/oam/snapshots/$SURVIVOR/download")
check "download works after container recreate" "200" "$DLCODE"
jq -e '.kind=="loxilb-snapshot"' "$TMP/survivor.json" >/dev/null && ok "survivor decrypts to a valid document" || bad "survivor decrypts to a valid document"

echo "== cleanup"
curl -s -o /dev/null -X DELETE -H "$AUTH" "$OAM/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer/externalipaddress/20.20.20.99/port/8899/protocol/tcp"
curl -s -o /dev/null -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  "$OAM/oam/instances/$INSTANCE_ID/snapshot-schedule" -d '{"enabled":false,"interval_hours":1,"retain_count":3}'
$MYSQL "DELETE FROM instance_snapshots; DELETE FROM instance_snapshot_schedules;"
$MYSQL "DELETE FROM loxilb_instances WHERE name IN ('e2e-dead');"
VID=$($MYSQL "SELECT id FROM users WHERE username='e2e_snap_viewer';")
[ -n "$VID" ] && curl -s -o /dev/null -X DELETE -H "$AUTH" "$OAM/oam/users/$VID"
check "gateway clean (0 LB)" "0" "$(lb_count)"
check "snapshot table empty" "0" "$($MYSQL 'SELECT COUNT(*) FROM instance_snapshots;')"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ $FAIL -eq 0 ]

#!/usr/bin/env bash
set -euo pipefail

OCS_CONTAINER=${OCS_CONTAINER:-ocs-master}
WARDEN_URL=${WARDEN_URL:-http://127.0.0.1:4207}
BACKEND=${BACKEND:-PASQAL_LOCAL}
TOTAL_SLOTS=${TOTAL_SLOTS:-10}
EXTERNAL_SLOTS=${EXTERNAL_SLOTS:-6}
JOB_SLOTS=${JOB_SLOTS:-5}

ocs() {
  docker exec "$OCS_CONTAINER" bash -lc \
    "source /opt/ocs/default/common/settings.sh && $*"
}

revoke_external() {
  if [[ -n "${external_session:-}" ]]; then
    docker exec "$OCS_CONTAINER" bash -lc \
      "cred=\$(munge -n | tr -d '\\n'); curl -fsS -X DELETE \
        -H \"X-Munge-Cred: \$cred\" \
        ${WARDEN_URL}/sessions/${external_session}" >/dev/null || true
    external_session=
  fi
}
trap revoke_external EXIT

docker exec "$OCS_CONTAINER" curl -fsS "${WARDEN_URL}/accessible" |
  python3 -c "import json,sys; p=json.load(sys.stdin); assert p['qpu_slots_total']==${TOTAL_SLOTS}"

job_key="ocs-e2e-$(date +%s)-$$"
external_session=$(docker exec "$OCS_CONTAINER" bash -lc \
  "cred=\$(munge -n | tr -d '\\n'); curl -fsS \
    -H \"X-Munge-Cred: \$cred\" \
    -H 'Content-Type: application/json' \
    -d '{\"user_id\":\"0\",\"slurm_job_id\":\"${job_key}\",\"qpu_slots\":${EXTERNAL_SLOTS}}' \
    ${WARDEN_URL}/sessions" |
  python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")

for _ in {1..30}; do
  available=$(ocs "qhost -F qpu_slots" | sed -n 's/.*qpu_slots=\([0-9]*\).*/\1/p' | head -n1)
  [[ "$available" == "$((TOTAL_SLOTS - EXTERNAL_SLOTS))" ]] && break
  sleep 1
done
[[ "${available:-}" == "$((TOTAL_SLOTS - EXTERNAL_SLOTS))" ]]

job_id=$(ocs "qsub -b y -terse -q all.q@ocs-master \
  -l qpu=${BACKEND},qpu_slots=${JOB_SLOTS},qpu_ready=1 \
  -o /tmp/ocs_warden_slots.out -e /tmp/ocs_warden_slots.err /bin/sleep 2")
sleep 1
ocs "qstat -j ${job_id}" >/dev/null

revoke_external
for _ in {1..45}; do
  if ! ocs "qstat -j ${job_id}" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

for _ in {1..30}; do
  if accounting=$(ocs "qacct -j ${job_id}" 2>/dev/null); then
    break
  fi
  sleep 1
done
[[ -n "${accounting:-}" ]]
grep -q '^failed[[:space:]]*0' <<<"$accounting"
grep -q '^exit_status[[:space:]]*0' <<<"$accounting"
grep -q '^qrmi_acquired_count[[:space:]]*1' <<<"$accounting"
grep -q '^qrmi_release_success[[:space:]]*1' <<<"$accounting"

echo "OCS/Warden Load Sensor integration passed for job ${job_id}"

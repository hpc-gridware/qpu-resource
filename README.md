# QPU-resource

Pasqal-OCS integration

Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors.

Go CLI for QRMI setup on Gridware Cluster Scheduler (GCS) and
Open Cluster Scheduler (OCS).

In this workspace, the documented end-to-end path is OCS local validation with
Pasqal Cloud (`EMU_FREE`).

## Who This Is For

- End users:
  - Submit jobs through OCS with `qsub -l qpu=EMU_FREE`.
  - Start at: `docs/quickinstall-testing.md` (sections 2 and 3).
- Admins:
  - Configure Gridware resource state and queue hooks.
  - Start at:
    - `docs/quickinstall-testing.md` (primary admin runbook)
    - `setup-qrmi-support` section below
- Developers:
  - Work on adapter CLI and queue hook code.
  - Start at:
    - `src/cmd/gridware-adapter/main.go`
    - `src/cmd/qrmi-ocs-prolog/main.c` (legacy C hook)
    - `src/cmd/qrmi-ocs-epilog/main.c` (legacy C hook)
    - `src/cmd/qrmi-ocs-prolog-go/main.go` (Go port)
    - `src/cmd/qrmi-ocs-epilog-go/main.go` (Go port)

## Repository Layout

```text
qpu-resource
├── LICENSE
├── Makefile
├── README.md
├── consumable-issue.md
├── demo
│   └── qrmi
│       └── quickinstall.sh
├── docs
│   ├── plans/
│   └── quickinstall-testing.md
├── go.mod
├── go.sum
├── scripts
│   └── Dockerfile.hooks
├── src
│   ├── cmd
│   │   ├── gridware-adapter
│   │   │   └── main.go
│   │   ├── qrmi-ocs-epilog
│   │   │   └── main.c
│   │   ├── qrmi-ocs-epilog-go (Go port of the epilog hook)
│   │   │   └── main.go
│   │   ├── qrmi-ocs-prolog
│   │   │   └── main.c
│   │   └── qrmi-ocs-prolog-go (Go port of the prolog hook)
│   │       └── main.go
│   └── internal
│       ├── qrmi          (cgo wrapper around libqrmi)
│       └── qrmiocs       (scheduler-side plumbing, no cgo)
```

What is where:

- `src/cmd/gridware-adapter/main.go`: adapter CLI (`setup-qrmi-support`, `ensure-resource`, `configure-queue-hooks`).
- `src/cmd/qrmi-ocs-load-sensor/main.go`: optional OCS Load Sensor for dynamic QPU readiness.
- `src/cmd/qrmi-ocs-prolog/main.c`: legacy OCS queue prolog hook (resource/env setup + acquire).
- `src/cmd/qrmi-ocs-epilog/main.c`: legacy OCS queue epilog hook (release + accounting fields).
- `src/cmd/qrmi-ocs-prolog-go/main.go`: Go port of the prolog hook (preferred for new deployments).
- `src/cmd/qrmi-ocs-epilog-go/main.go`: Go port of the epilog hook (preferred for new deployments).
- `src/internal/qrmiocs`: scheduler-side glue (env, paths, metadata TSV, RUST_LOG mapping). No cgo. Fully unit-tested.
- `src/internal/qrmi`: cgo wrapper around `libqrmi.so`. Compiled only with `-tags qrmi`.
- `scripts/Dockerfile.hooks`: multi-stage Docker build that produces the Go hook binaries plus `libqrmi.so`.
- `Makefile`: `make build-go-hooks`, `make build-adapter`, `make test`, `make vet`.
- `docs/quickinstall-testing.md`: admin runbook for quickinstall + validation.
- `demo/qrmi/quickinstall.sh`: runnable smoke commands for quick checks.
- `go.mod` and `go.sum`: Go module and dependency lock state.

## Build

The repository ships a Makefile that wraps the Docker-based build flows.

### Adapter (no cgo)

```bash
make build-adapter
# binary at bin/adapter/adapter

make build-load-sensor
# binary at bin/adapter/qrmi-ocs-load-sensor
```

### Go OCS hooks (cgo against `libqrmi.so`)

The Go ports use cgo to link against `libqrmi.so`. The Docker build in
`scripts/Dockerfile.hooks` clones QRMI from upstream, builds the shared
library, then cgo-builds the Go binaries against it. Output is written
under `bin/go-hooks/`:

```bash
make build-go-hooks                    # builds against QRMI v0.13.3 by default
make build-go-hooks QRMI_REF=main      # builds against QRMI main
```

Outputs in `bin/go-hooks/`:

- `qrmi-ocs-prolog`
- `qrmi-ocs-epilog`
- `libqrmi.so`

The hook binaries embed `rpath=$ORIGIN`, so as long as `libqrmi.so` sits
beside them on the execution host they will resolve the library without
any `LD_LIBRARY_PATH` mangling.

### Legacy C hooks

The legacy C prolog/epilog under `src/cmd/qrmi-ocs-prolog/` and
`src/cmd/qrmi-ocs-epilog/` are kept until the Go ports are validated in
production. Build instructions for those are unchanged; see the
`Build queue hooks` section below.

### Tests

```bash
make test    # runs Go unit tests; no QRMI required (uses stub build)
make vet
```

Two of the existing C-harness tests in `src/cmd/gridware-adapter`
(`TestPrologApplyBackendEnvUsesConfiguredValue`,
`TestEpilogStrictMetadataBehavior`) require a sibling `qrmi/` checkout
at `${GOPATH}/src/github.com/hpc-gridware/qrmi` with the QRMI headers.
Without it those two tests fail with `required path missing "qrmi"`;
that is unrelated to the Go ports and matches the pre-existing
behavior of the C hook tests.

## End-User Quick Start (OCS)

Admin prerequisite: run `setup-qrmi-support` once on the target OCS queue.

Submit a quick smoke job:

```bash
qsub -b y -terse -l qpu=EMU_FREE /bin/echo OCS_QRMI_OK
```

For a real Pasqal Cloud Pulser task, follow
`docs/quickinstall-testing.md` section `3`.

## Pasqal Cloud Access (`EMU_FREE`)

Pasqal documents free emulator access in the Explorer Offer, including `EMU_FREE`:
- https://docs.pasqal.com/cloud/set-up/

Start from the portal:
- https://portal.pasqal.cloud

Then get your project ID from the same "Join Pasqal Cloud" guide ("Find your project ID"),
and configure credentials on submit/exec hosts:

```bash
mkdir -p ~/.pasqal
cat > ~/.pasqal/config <<'EOF'
username=<your_email>
password=<your_password_or_token_flow>
project_id=<your_project_id>
auth_endpoint=https://authenticate.pasqal.cloud/oauth/token
EOF
chmod 600 ~/.pasqal/config
```

## Admin Guide

Admin setup and verification details are in `docs/quickinstall-testing.md`.
The key operational model is:

- Scheduler resource: `qpu` as `STRING` with relop `==`
- Consumable policy: `qpu` is configured as `NO` (backend selector only)
- Host assignment: one backend name per host (for example `qpu=EMU_FREE`)
- Optional capacity resource: `qpu_slots` as `INT`, `<=`, `Consumable=JOB`
- Optional readiness resource: `qpu_ready` as `INT`, `<=`, `consumable=NO`
- Job request without Load Sensor: `-l qpu=<backend>` (for example `-l qpu=EMU_FREE`)
- Job request with Load Sensor: `-l qpu=<backend>,qpu_slots=1,qpu_ready=1`

### Admin Quick Checklist

1. Build and copy adapter binary to scheduler master (`/tmp/adapter` in quickinstall flow).
2. Build and copy queue hooks + `libqrmi.so` to scheduler master.
3. Run `setup-qrmi-support` to apply resource, host mapping, queue hooks, and reporting params in one command.
4. Verify `qsub -l qpu=<backend>` succeeds.

## Adapter Commands (Admin/Advanced Users)

### `setup-qrmi-support` (Default)

Apply all required OCS-side QRMI setup in one command:

- ensure `qpu` complex entry (`STRING`, `==`, `requestable=YES`, `consumable=NO`)
- set host `complex_values` to one backend name per host
- set queue `prolog` and `epilog`
- ensure global `reporting_params` contains `usage_patterns=qrmi:qrmi_*`

```bash
./adapter setup-qrmi-support \
  --hosts ocs-master,ocs-worker1,ocs-worker2 \
  --host-value EMU_FREE \
  --queue all.q \
  --prolog /shared/gridware-adapter/bin/qrmi-ocs-prolog \
  --epilog /shared/gridware-adapter/bin/qrmi-ocs-epilog
```

Optional Load Sensor setup keeps capacity and readiness separate:

```bash
./adapter setup-qrmi-support \
  --hosts ocs-master,ocs-worker1,ocs-worker2 \
  --host-value PASQAL_LOCAL \
  --queue all.q \
  --prolog /shared/gridware-adapter/bin/qrmi-ocs-prolog \
  --epilog /shared/gridware-adapter/bin/qrmi-ocs-epilog \
  --enable-qpu-slots \
  --qpu-slots-capacity 0 \
  --enable-load-sensor \
  --load-sensor-host ocs-master \
  --load-sensor-path /shared/gridware-adapter/bin/qrmi-ocs-load-sensor
```

Use `--qpu-slots-capacity 0` for Warden-owned Pasqal Local slots. Use a
positive value only when OCS should own static host-local slot capacity.
For one statically configured backend shared by several hosts, add
`--qpu-slots-scope global`.

OCS stores `load_sensor` as an executable path. Put the configuration at
`/etc/qrmi-ocs-load-sensor.yaml`, or set `QRMI_OCS_LOAD_SENSOR_CONFIG` when
running the sensor manually. Load Sensor configuration:

```yaml
load_sensor:
  enabled: true
  scope: global
  resource_name: qpu_ready
  slots_resource_name: qpu_slots
  provider: warden
  timeout_seconds: 3

warden:
  base_url: http://127.0.0.1:8006
  endpoint: /accessible
  tls_verify: true

static:
  ready: true
  state_file: ""
```

The `static` provider either returns `static.ready` or reads `0`/`1` from
`static.state_file`. It is a dummy readiness signal for paper/demo use and does
not represent Pasqal Cloud queue availability. The `warden` provider polls
`GET /accessible`, can report `qpu_slots_available` when Warden is configured
with `qpu_slots_total`, and fails closed on timeouts, HTTP errors, or malformed
responses.

The Load Sensor is an early scheduler filter only. The OCS prolog still calls
QRMI `IsAccessible` and `Acquire`, so a job can still be rejected at dispatch
time if another scheduler or user consumed the external QPU after OCS scheduled
the job.

For the Pasqal Local setup, including Warden, MUNGE, QRMI config, and the
readiness Load Sensor, see `load_sensor.md`.

### `ensure-resource` (Advanced/Manual)

Ensure the scheduler complex entry exists and set host `complex_values`.

Use `STRING` with one backend name per host:

```bash
./adapter ensure-resource \
  --qconf qconf \
  --hosts ocs-master,ocs-worker1,ocs-worker2 \
  --host-value EMU_FREE
```

If hosts target different backends, run per host (or host group):

```bash
./adapter ensure-resource \
  --hosts ocs-worker1 \
  --host-value EMU_FREE

./adapter ensure-resource \
  --hosts ocs-worker2 \
  --host-value EMU_FREE
```

### `configure-queue-hooks` (Advanced/Manual)

Configure `prolog`, `epilog`, and `reporting_params`.

```bash
./adapter configure-queue-hooks \
  --queue all.q \
  --prolog /shared/gridware-adapter/bin/qrmi-ocs-prolog \
  --epilog /shared/gridware-adapter/bin/qrmi-ocs-epilog
```

### Build queue hooks

Compile against the QRMI C API (`qrmi/qrmi.h`) and shared library:

```bash
mkdir -p /shared/gridware-adapter/bin

gcc -Wall -Wextra -O2 \
  -I/shared/qrmi \
  -L/shared/qrmi/libqrmi-0.12.0 \
  -Wl,-rpath,'$ORIGIN' \
  -o /shared/gridware-adapter/bin/qrmi-ocs-prolog \
  /shared/gridware-adapter/src/cmd/qrmi-ocs-prolog/main.c \
  -lqrmi

gcc -Wall -Wextra -O2 \
  -I/shared/qrmi \
  -L/shared/qrmi/libqrmi-0.12.0 \
  -Wl,-rpath,'$ORIGIN' \
  -o /shared/gridware-adapter/bin/qrmi-ocs-epilog \
  /shared/gridware-adapter/src/cmd/qrmi-ocs-epilog/main.c \
  -lqrmi

cp /shared/qrmi/libqrmi-0.12.0/libqrmi.so /shared/gridware-adapter/bin/
```

Hook behavior:

- Prolog reads granted scheduler resource, resolves one backend name, acquires QRMI token, and writes runtime variables into the job environment.
- Prolog requires `SGE_HGR_<resource>` or `SGE_SGR_<resource>` to be available in the prolog environment.
- Epilog reads acquisition metadata and releases tokens.
- Epilog expects exactly one metadata record; multiple records are treated as an error.
- Prolog publishes runtime `qrmi_*` values in the job environment.
- Epilog appends numeric `qrmi_*` values into `${SGE_JOB_SPOOL_DIR}/usage` so they are captured by `usage_patterns=qrmi:qrmi_*` and appear in `qacct`/accounting JSON.
- Typical runtime `qrmi_*` variables from prolog:
  - `qrmi_resources`: backend/resource names acquired by prolog.
  - `qrmi_resource_types`: QRMI resource types for acquired backends.
  - `qrmi_prolog_status`: prolog outcome (`success` or `error`).
- Typical runtime error variable from prolog:
  - `QRMI_PLUGIN_ERROR`: prolog error text when a failure occurs.
- Typical accounting `qrmi_*` fields in `qacct` / accounting JSON:
  - `qrmi_acquired_count`: number of acquired resources (published from epilog usage data).
  - `qrmi_release_total`: number of non-empty metadata records seen before release handling (`0` or `1` in strict single-record mode).
  - `qrmi_release_success`: number of successful releases in epilog.
  - `qrmi_release_failed`: number of failed releases in epilog.
  - `qrmi_release_elapsed_seconds`: epilog release-loop elapsed time.
  - `qrmi_epilog_status_code`: epilog outcome as numeric code (`1` success, `0` error).
- If `RUST_LOG` is unset, prolog derives it from `QRMI_OCS_LOG_LEVEL` (or `SGE_DEBUG_LEVEL`) using:
  - `2 -> error`, `3 -> info`, `4 -> debug`, `>=5 -> trace`

### Behavior vs SPANK Plugin

This Gridware/OCS adapter mirrors core SPANK behavior in these areas:

- Loads backend settings from `qrmi_config.json` and exports backend-prefixed `QRMI_*` variables.
- Sets `RUST_LOG` from scheduler debug level mapping when `RUST_LOG` is not already set.
- Exports `QRMI_JOB_QPU_RESOURCES` and `QRMI_JOB_QPU_TYPES` for runtime compatibility.
- Acquires in prolog and releases in epilog.

Intentional differences from Slurm SPANK:

- Single backend per job in this adapter model (`-l qpu=<backend>`).
- No comma-separated multi-backend request syntax.

## Developer Notes

- Keep queue hooks aligned with the single-backend scheduler model (`-l qpu=<backend>`).
- Use `make build-adapter` and `make build-go-hooks` for the standard build flows; see `Build` above.
- Compile-check legacy C hook sources:

```bash
gcc -Wall -Wextra -fsyntax-only -I./qrmi src/cmd/qrmi-ocs-prolog/main.c
gcc -Wall -Wextra -fsyntax-only -I./qrmi src/cmd/qrmi-ocs-epilog/main.c
```

- Run Go unit tests for the new hook plumbing without QRMI:

```bash
make test
```

- Working on the cgo wrapper itself (`src/internal/qrmi`)? Build with the
  `qrmi` tag and the QRMI artifacts on hand:

```bash
CGO_ENABLED=1 \
  CGO_CFLAGS="-I/path/to/qrmi" \
  CGO_LDFLAGS="-L/path/to/qrmi -lqrmi -Wl,-rpath,\$ORIGIN" \
  go build -tags qrmi ./src/cmd/qrmi-ocs-prolog-go
```

## Demo and Docs

- Runnable quickinstall demo commands: `demo/qrmi/quickinstall.sh`
- Full runbook: `docs/quickinstall-testing.md`

## Additional Notes

- QRMI runtime config is expected at `/etc/slurm/qrmi_config.json` on submit and execution hosts.
- OCS quickinstall containers should provide both `python3` and `python` commands.

## License

Apache License 2.0. See `LICENSE`.

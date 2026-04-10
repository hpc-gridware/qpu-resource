# QPU-resource
Pasqal-OCS integration

Copyright 2026 Pasqal, Gridware and its contributors.

Go CLI for QRMI setup on HPC-Gridware ClusterScheduler.
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
    - `src/cmd/qrmi-ocs-prolog/main.c`
    - `src/cmd/qrmi-ocs-epilog/main.c`

## Repository Layout

```text
gridware-adapter
├── LICENSE
├── README.md
├── adapter
├── consumable-issue.md
├── demo
│   └── qrmi
│       └── quickinstall.sh
├── docs
│   └── quickinstall-testing.md
├── go.mod
├── go.sum
├── src
│   └── cmd
│       ├── gridware-adapter
│       │   └── main.go
│       ├── qrmi-ocs-epilog
│       │   └── main.c
│       ├── qrmi-ocs-prolog
│           └── main.c
```

What is where:
- `src/cmd/gridware-adapter/main.go`: adapter CLI (`setup-qrmi-support`, `ensure-resource`, `configure-queue-hooks`).
- `src/cmd/qrmi-ocs-prolog/main.c`: OCS queue prolog hook (resource/env setup + acquire).
- `src/cmd/qrmi-ocs-epilog/main.c`: OCS queue epilog hook (release + accounting fields).
- `docs/quickinstall-testing.md`: admin runbook for quickinstall + validation.
- `demo/qrmi/quickinstall.sh`: runnable smoke commands for quick checks.
- `adapter`: locally built binary output from `go build`.
- `go.mod` and `go.sum`: Go module and dependency lock state.

## Build

Host Go in this workspace is older than required by `go-clusterscheduler`, so build with Docker:

```bash
docker run --rm --user $(id -u):$(id -g) \
  -e GOCACHE=/tmp/go-build \
  -e GOPATH=/tmp/go \
  -v "$PWD":/work \
  -w /work/gridware-adapter \
  golang:1.24 /bin/sh -lc \
  'export PATH=/usr/local/go/bin:$PATH && go mod tidy && go build -buildvcs=false -o /work/gridware-adapter/adapter ./src/cmd/gridware-adapter'
```

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
- Job request: `-l qpu=<backend>` (for example `-l qpu=EMU_FREE`)

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
- Exports `SLURM_JOB_QPU_RESOURCES` and `SLURM_JOB_QPU_TYPES` for runtime compatibility.
- Acquires in prolog and releases in epilog.

Intentional differences from Slurm SPANK:
- Single backend per job in this adapter model (`-l qpu=<backend>`).
- No comma-separated multi-backend request syntax.

## Developer Notes

- Keep queue hooks aligned with the single-backend scheduler model (`-l qpu=<backend>`).
- Build adapter binary:

```bash
docker run --rm --user $(id -u):$(id -g) \
  -e GOCACHE=/tmp/go-build \
  -e GOPATH=/tmp/go \
  -v "$PWD":/work \
  -w /work/gridware-adapter \
  golang:1.24 /bin/sh -lc \
  'export PATH=/usr/local/go/bin:$PATH && go build -buildvcs=false -o /work/gridware-adapter/adapter ./src/cmd/gridware-adapter'
```

- Compile-check hook sources:

```bash
gcc -Wall -Wextra -fsyntax-only -I./qrmi gridware-adapter/src/cmd/qrmi-ocs-prolog/main.c
gcc -Wall -Wextra -fsyntax-only -I./qrmi gridware-adapter/src/cmd/qrmi-ocs-epilog/main.c
```

## Demo and Docs

- Runnable quickinstall demo commands: `demo/qrmi/quickinstall.sh`
- Full runbook: `docs/quickinstall-testing.md`

## Additional Notes

- QRMI runtime config is expected at `/etc/slurm/qrmi_config.json` on submit and execution hosts.
- OCS quickinstall containers should provide both `python3` and `python` commands.

## License

Apache License 2.0. See `LICENSE`.

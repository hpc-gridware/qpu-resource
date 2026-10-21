# QRMI Adapter for Grid Engine

**Schedule vendor-agnostic quantum resources through familiar `qsub` jobs.**

[![arXiv](https://img.shields.io/badge/arXiv-2607.19591-b31b1b.svg)](https://arxiv.org/abs/2607.19591)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[Paper](https://arxiv.org/abs/2607.19591) ·
[Admin runbook](docs/quickinstall-testing.md) ·
[Load Sensor guide](load_sensor.md) ·
[QRMI](https://github.com/qiskit-community/qrmi)

This repository connects the
[Quantum Resource Management Interface (QRMI)](https://github.com/qiskit-community/qrmi)
to the Grid Engine family of schedulers, including
[Open Cluster Scheduler (OCS)](https://github.com/hpc-gridware/clusterscheduler)
and Gridware Cluster Scheduler (GCS). QPUs and emulators become schedulable
resources alongside CPUs and GPUs, while QRMI keeps jobs independent of
provider-specific APIs.

This is the completed Grid Engine integration examined in the 2026 paper
[*Examining QRMI as a Unified Interface for Quantum-HPC Integration*](https://arxiv.org/abs/2607.19591).
It covers the scheduler, lifecycle, configuration, dynamic state, and
accounting behavior described there.

## Implementation Status

**The Grid Engine/OCS scope claimed in the paper is implemented in this
repository.**

| Paper capability | Implementation |
| --- | --- |
| Scheduler-native quantum resource selection and constraints | `qpu`, `qpu_slots`, and `qpu_ready` complexes configured by `gridware-adapter` |
| Dynamic readiness and capacity | OCS Load Sensor with static and Pasqal Warden providers |
| Lifecycle management | QRMI acquisition in the queue prolog and release in the epilog |
| Provider-independent configuration | Logical resource lookup through `qrmi_config.json` |
| Runtime state and accounting | Per-job spool metadata, environment variables, and `qrmi_*` fields exposed through `qacct` |

The Pasqal Cloud `EMU_FREE` path and Pasqal Local readiness work with released
QRMI versions. Pasqal Local slot capacity depends on two pending upstream
changes:

- [QRMI #164: Add QPU slots to Pasqal local QRMI](https://github.com/qiskit-community/qrmi/pull/164)
- [Warden #73: Add Warden-side features for QPU slots](https://github.com/pasqal-io/warden/pull/73)

Until those changes are merged and released, use their matching
`aw/qpu-slots` branches to validate slot capacity.

## Architecture

![QRMI integration with Open Cluster Scheduler](docs/figs/qrmi-ocs-architecture.png)

*The implemented OCS architecture. A job is admitted through scheduler-native
complexes, acquires its quantum resource in the queue prolog, and releases it
in the epilog. The Load Sensor feeds current readiness and capacity into
scheduling decisions when dynamic state is enabled.*

The integration follows three main ideas from the paper:

- **Scheduler-native requests:** users select a quantum resource with ordinary
  Grid Engine syntax instead of a provider-specific submission tool.
- **Thin lifecycle hooks:** the prolog acquires through QRMI and the epilog
  releases the resource and records usage.
- **Provider separation:** scheduler configuration names logical resources;
  `qrmi_config.json` owns provider resource types and runtime settings.

### Resource model

| Complex | Type and relation | Consumable | Purpose |
| --- | --- | --- | --- |
| `qpu` | `STRING ==` | No | Select one logical QRMI resource |
| `qpu_slots` | `INT <=` | Per job | Request capacity from that resource |
| `qpu_ready` | `INT <=` | No | Require dynamic availability reported by a Load Sensor |

`qpu` is deliberately a selector, not a consumable. Grid Engine requires
consumables to be numeric and use `<=`, so capacity belongs in a separate
complex such as `qpu_slots`. See [the resource-model note](consumable-issue.md)
for the scheduler constraints behind this design.

Submit against a configured resource:

```bash
# Selection only
qsub -l qpu=EMU_FREE job.sh

# Selection plus dynamic readiness and capacity
qsub -l qpu=PASQAL_FRESNEL,qpu_ready=1,qpu_slots=2 job.sh
```

The second request is a conjunction: OCS dispatches it only when the host
advertises `PASQAL_FRESNEL`, the backend is ready, and at least two QPU slots
are available. Resource names are administrator-defined entries in
`qrmi_config.json`; use the name deployed at your site.

### Job lifecycle

1. The user submits a normal `qsub` request for `qpu` and optional constraints.
2. OCS evaluates the request against host complexes and Load Sensor values.
3. The queue prolog resolves the granted name, checks accessibility, acquires
   the QRMI resource, and publishes runtime metadata to the job environment.
4. The user job talks to the selected backend through QRMI.
5. The queue epilog releases the resource and writes `qrmi_*` usage fields for
   `qacct` and accounting integrations.

## Start Here

| Audience | First step |
| --- | --- |
| Users | Submit the [smoke job](#end-user-quick-start-ocs), then follow the [Pasqal Cloud test](docs/quickinstall-testing.md#3-pasqal-cloud-pulser-test-on-ocs) |
| Administrators | Follow the [quickinstall runbook](docs/quickinstall-testing.md) or use [`setup-qrmi-support`](#setup-qrmi-support-default) |
| Developers | Build and test below, then start with the adapter and Go hook commands under `src/cmd/` |

## Repository Map

| Path | Role |
| --- | --- |
| `src/cmd/gridware-adapter` | Configures complexes, host mappings, queue hooks, Load Sensors, and reporting |
| `src/cmd/qrmi-ocs-load-sensor` | Publishes dynamic QPU readiness and capacity |
| `src/cmd/qrmi-ocs-prolog-go` / `qrmi-ocs-epilog-go` | Preferred Go lifecycle hooks |
| `src/cmd/qrmi-ocs-launch` | Starts the hooks as the hook user with a clean environment |
| `src/cmd/qrmi-ocs-prolog` / `qrmi-ocs-epilog` | Legacy C lifecycle hooks |
| `src/internal/availability` | Static and Pasqal Warden availability providers |
| `src/internal/qrmi` | cgo wrapper around `libqrmi.so` |
| `src/internal/qrmiocs` | Scheduler environment, spool, metadata, and logging support |
| `docs/quickinstall-testing.md` | OCS installation and Pasqal Cloud validation runbook |
| `docs/figs` | Documentation figures, including the OCS architecture above |
| `load_sensor.md` | Pasqal Local, Warden, and Load Sensor operations |
| `demo/qrmi` | Runnable smoke and Load Sensor checks |

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
make build-go-hooks                    # builds against QRMI 0.26.3 by default
make build-go-hooks QRMI_REF=main      # builds against QRMI main
make build-go-hooks QRMI_FEATURES=munge # links libmunge at build time
```

Pasqal Local authenticates to Warden with MUNGE, so the hosts need
`libmunge.so.2`: QRMI 0.26.1 and later load it at run time, and
`QRMI_FEATURES=munge` links it at build time instead. QPU slot counts need the
`aw/qpu-slots` QRMI branch until PR #164 is released.

Outputs in `bin/go-hooks/`:

- `qrmi-ocs-prolog`
- `qrmi-ocs-epilog`
- `qrmi-ocs-launch` (static, no cgo)
- `libqrmi.so`

The hook binaries embed `rpath=$ORIGIN`, so as long as `libqrmi.so` sits
beside them on the execution host they will resolve the library without
any `LD_LIBRARY_PATH` mangling.

### Legacy C hooks

The Go prolog and epilog are the current implementations. The legacy C hooks
under `src/cmd/qrmi-ocs-prolog/` and `src/cmd/qrmi-ocs-epilog/` remain for
compatibility and have the same external contract. See
[Build queue hooks](#build-queue-hooks) for their build instructions.

### Tests

```bash
make test    # runs Go tests and the legacy C harnesses
make vet
```

Two C-harness tests in `src/cmd/gridware-adapter`
(`TestPrologApplyBackendEnvUsesConfiguredValue`,
`TestEpilogStrictMetadataBehavior`) compile the C hooks against `qrmi.h`.
Point `QRMI_INCLUDE_DIR` at a QRMI checkout, or check QRMI out next to this
repository (`../qrmi`). Without either, they fail with `required path missing`.

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

The adapter installs the QRMI hooks as `<hook-user>@<path> … $job_owner`
procedures. OCS runs them as that account and passes the job owner's name.
`--hook-user` defaults to the cluster's `admin_user` from
`$SGE_ROOT/$SGE_CELL/common/bootstrap`, or `root` when it is `none`. The hooks
need that account for two reasons:

- They write the QRMI variables and the acquisition metadata into the job's
  spool directory (`$SGE_JOB_SPOOL_DIR/environment`), which OCS owns as its
  admin account. This applies to every QRMI resource type.
- For Pasqal Local, the prolog opens a Warden session on behalf of the job
  owner's UID and the epilog revokes it. Warden allows only administrators to
  do that, so add the hook account's UID to Warden's `admin_users` (`0` for root).

Because the hooks run privileged with the job's environment, they trust only
the scheduler:

- The queue command starts them through `qrmi-ocs-launch`
  (`<hook-user>@<dir>/qrmi-ocs-launch --sge-root=… --sge-cell=… [--spool-root=…]…
  [--qmaster-port=…] [--execd-port=…] <hook> … $job_owner`; `--launcher` sets
  another path). `setup-qrmi-support` writes the launcher options from its own
  `SGE_ROOT`, `SGE_CELL` and ports, and one `--spool-root` for the global
  `execd_spool_dir` and for each host-local override (`qconf -sconf <host>`).
  Run it again after setting a host-local `execd_spool_dir`. The
  launcher is statically linked, so no dynamic loader reads the job's
  environment. It refuses to run, failing the job, when `LD_*` or
  `GLIBC_TUNABLES` is set. Otherwise it changes to `/` and starts the hook with:
  `JOB_ID`, `SGE_TASK_ID` and `SGE_JOB_SPOOL_DIR` from OCS, validated (the
  spool directory must be the job's own `…/active_jobs/<job>.<task>` below one
  of the spool roots, without symbolic links); `SGE_ROOT`, `SGE_CELL` and the ports from
  its options; `TZ` if it is a plain time zone name; a fixed `PATH`, the hook
  user's `HOME` and the `C` locale. Everything else is dropped: `QRMI_*` and
  `<BACKEND>_QRMI_*` settings, proxy and TLS trust variables, `RUST_LOG` (the
  hooks set it from `--log-level`), shell functions and the job owner's `HOME`.
  The hooks repeat the loader check and the reset. Backend settings come only
  from `qrmi_config.json` and the hook arguments. The hooks need no Pasqal Cloud
  credentials; the job reads the job owner's `~/.pasqal/config`.
- The QRMI config path, the absolute `qstat` path (with `qconf` next to it), the
  resource names and the log level are part of the administrator-owned queue
  command.
- The prolog reads the job's hard request from `qstat -j` (`hard_resource_list`,
  or `master_hard_resource_list` for `-scope master`). Soft requests are ignored,
  and the backend must match the host's `complex_values`.

The launcher, hook binaries, `qstat`/`qconf` and their parent directories must
be writable only by administrators.

Exit codes follow OCS's prolog semantics: `0` when the job is set up or
requests no QPU, `100` (job error) when the request cannot be served, for
example a backend missing from `qrmi_config.json`, `99` (reschedule) when the
backend is temporarily unavailable, and `1` (queue error) only for host or
configuration problems. A failed release in the epilog is logged and recorded
in accounting (`qrmi_release_failed`), but the epilog still exits `0`. OCS
deletes the job's spool directory after the job, so the epilog moves the
metadata file, which holds the acquisition token, to
`<execd spool>/<host>/qrmi_failed_releases/<job>.<task>.tsv` (directory mode
`0700`, owned by the hook user) for the administrator to release by hand.

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
`static.state_file`. It provides deterministic state for scheduler validation
without a live provider and does not represent Pasqal Cloud queue availability.
The `warden` provider polls `GET /accessible` for readiness and, when the
response contains it, `qpu_slots_available`. It fails closed on timeouts, HTTP
errors, or malformed responses.

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
  -L/shared/qrmi/libqrmi-0.26.3 \
  -Wl,-rpath,'$ORIGIN' \
  -o /shared/gridware-adapter/bin/qrmi-ocs-prolog \
  /shared/gridware-adapter/src/cmd/qrmi-ocs-prolog/main.c \
  -lqrmi

gcc -Wall -Wextra -O2 \
  -I/shared/qrmi \
  -L/shared/qrmi/libqrmi-0.26.3 \
  -Wl,-rpath,'$ORIGIN' \
  -o /shared/gridware-adapter/bin/qrmi-ocs-epilog \
  /shared/gridware-adapter/src/cmd/qrmi-ocs-epilog/main.c \
  -lqrmi

cp /shared/qrmi/libqrmi-0.26.3/libqrmi.so /shared/gridware-adapter/bin/
```

Hook behavior:

- Prolog reads granted scheduler resource, resolves one backend name, acquires QRMI token, and writes runtime variables into the job environment.
- Prolog reads the dispatched job's resource list from the administrator-configured `qstat -j` command.
- Acquisition metadata is stored only in the scheduler-owned job spool.
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
- `RUST_LOG` comes from `--hook-log-level` (names, or SGE debug levels mapped as
  `2 -> error`, `3 -> info`, `4 -> debug`, `>=5 -> trace`), not from the job environment.

### Behavior vs SPANK Plugin

This Gridware/OCS adapter mirrors core SPANK behavior in these areas:

- Loads backend settings from `qrmi_config.json` and exports backend-prefixed `QRMI_*` variables.
- Sets `RUST_LOG` from the administrator's `--hook-log-level`.
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

- Run the Go hook tests without QRMI:

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

- [Runnable quickinstall demo commands](demo/qrmi/quickinstall.sh)
- [OCS and Pasqal Cloud admin runbook](docs/quickinstall-testing.md)
- [Load Sensor and Pasqal Local guide](load_sensor.md)
- [Grid Engine integration paper](https://arxiv.org/abs/2607.19591)

## Additional Notes

- QRMI runtime config is expected at `/etc/qrmi/qrmi_config.json` on submit and execution hosts.
- OCS quickinstall containers should provide both `python3` and `python` commands.

## License

[Apache License 2.0](LICENSE).

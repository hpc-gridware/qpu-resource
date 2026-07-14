# OCS QPU Load Sensor

This adapter provides an optional Open Cluster Scheduler Load Sensor for QPU
readiness. It is a scheduler-side admission filter only. QRMI acquisition and
release remain in the queue prolog and epilog.

## Resource Model

Use three separate complexes:

```text
qpu        STRING ==  requestable  non-consumable  backend selector
qpu_slots  INT    <=  requestable  consumable      requested QPU slot count
qpu_ready  INT    <=  requestable  non-consumable  dynamic readiness
```

`qpu_ready` is intentionally `INT <=`, not `BOOL ==`. Live OCS validation
showed that numeric load values work reliably with a global configured maximum:

```text
global complex_values: qpu_ready=1
Load Sensor values:    global:qpu_ready:0 or global:qpu_ready:1
```

Typical job request:

```sh
qsub -l qpu=PASQAL_LOCAL,qpu_slots=1,qpu_ready=1 job.sh
```

Do not map provider readiness onto `qpu_slots`. `qpu_ready` is binary
readiness. `qpu_slots` is the requested share of QPU capacity. For static OCS
setups, OCS can own this as a normal consumable. For Pasqal Local, Warden can
own the global slot count and the Load Sensor can report current available
slots to OCS.

## Executable

The Load Sensor binary is:

```text
qrmi-ocs-load-sensor
```

Default config path:

```text
/etc/qrmi-ocs-load-sensor.yaml
```

Override with:

```text
QRMI_OCS_LOAD_SENSOR_CONFIG=/path/to/config.yaml
qrmi-ocs-load-sensor --config /path/to/config.yaml
```

OCS host configuration should contain only the executable path. Put options in
the config file, not in the `load_sensor` path.

## Protocol

The executable implements the OCS Load Sensor stdin/stdout protocol:

```text
begin
global:qpu_ready:1
global:qpu_slots:5
end
```

It exits on `quit`. Standard output is reserved for protocol frames only.
Diagnostics go to standard error.

Provider failures, HTTP errors, malformed responses, and timeouts fail closed:

```text
global:qpu_ready:0
global:qpu_slots:0
```

Startup and configuration errors also keep the protocol loop alive and report
`qpu_ready=0`. This avoids leaving the last successful readiness value active
while OCS waits for a failed Load Sensor process to be marked stale.

## Providers

`static` is for demos and paper support only:

```yaml
load_sensor:
  enabled: true
  scope: global
  resource_name: qpu_ready
  provider: static
  timeout_seconds: 3

static:
  ready: true
  state_file: ""
```

If `state_file` is set, the provider reads `1` as ready and `0` as unavailable.
Any read error or malformed value is unavailable.

`warden` polls Pasqal Warden:

```yaml
load_sensor:
  enabled: true
  scope: global
  resource_name: qpu_ready
  slots_resource_name: qpu_slots
  provider: warden
  timeout_seconds: 3

warden:
  base_url: http://127.0.0.1:4207
  endpoint: /accessible
  tls_verify: true
```

`GET /accessible` maps `{"is_accessible": true}` to ready. `false` maps to
unavailable. If Warden is configured with `qpu.qpu_slots_total`, the same
response includes `qpu_slots_total`, `qpu_slots_used`, and
`qpu_slots_available`; the Load Sensor reports `qpu_slots_available` as the
dynamic `qpu_slots` value. Missing slot data fails closed to `0` only when
`slots_resource_name` is set.

Warden remains scheduler-neutral; it does not configure OCS, run `qconf`, or
start this Load Sensor.

## Adapter Setup

Load Sensor support is disabled by default. Enable it explicitly:

```sh
adapter setup-qrmi-support \
  --hosts ocs-master \
  --host-value PASQAL_LOCAL \
  --enable-qpu-slots \
  --qpu-slots-capacity 0 \
  --enable-load-sensor \
  --load-sensor-host ocs-master \
  --load-sensor-path /shared/gridware-adapter/bin/adapter/qrmi-ocs-load-sensor \
  --queue all.q \
  --prolog /tmp/qrmi-hooks/qrmi-ocs-prolog \
  --epilog /tmp/qrmi-hooks/qrmi-ocs-epilog
```

Use `--qpu-slots-capacity 0` when Warden owns Pasqal Local slots. That creates
the `qpu_slots` complex and clears host-side `qpu_slots` values so the Load
Sensor is the dynamic source. Use a positive capacity only for static
OCS-owned slot accounting.

For static capacity, `--qpu-slots-scope host` is the default and applies the
capacity to each listed execution host. Use `--qpu-slots-scope global` when
the listed hosts share one backend:

```sh
--enable-qpu-slots --qpu-slots-capacity 10 --qpu-slots-scope global
```

Configure one designated execution host to report the global backend readiness
and slots. Do not run multiple global reporters for the same backend unless
there is a separate ownership design.

One global `qpu_ready` and `qpu_slots` pair represents one shared backend.
For several global backends, configure distinct names such as
`pasqal_local_ready` and `pasqal_local_slots` through
`--qpu-ready-name`, `--qpu-slots-name`, `resource_name`, and
`slots_resource_name`.

## Pasqal Local Setup

This is the local setup needed in addition to the existing OCS and QRMI/Pasqal
Cloud setup.

### Build Artifacts

Build and install these on the OCS master/execution host:

```text
adapter
qrmi-ocs-load-sensor
qrmi-ocs-prolog
qrmi-ocs-epilog
libqrmi.so
```

For Pasqal Local, `libqrmi.so` must be built with QRMI's `munge` feature. The
Go prolog/epilog must link against that same library and `libmunge`.

Keep `libqrmi.so` beside the Go hook binaries. The hooks are built with
`rpath=$ORIGIN`, so they resolve the colocated library without
`LD_LIBRARY_PATH`.

### MUNGE Boundary

Pasqal Local session creation uses MUNGE. The OCS prolog creates the credential
and Warden decodes it. Both processes must therefore be in the same MUNGE trust
domain.

Valid local layouts:

```text
OCS execd/prolog + munged + Warden on the same host
OCS execd/prolog + Warden on different hosts with shared MUNGE trust
```

For containers, `localhost` means the container's network namespace. Use
`http://127.0.0.1:<port>` only when Warden runs in the same container or host
namespace as the OCS execution daemon and prolog. If Warden runs outside the OCS
container, use a routable address and explicitly share the MUNGE trust setup.

### Warden

Run Warden normally. It needs no OCS configuration. The required external
contract is:

```text
GET /accessible
POST /sessions
DELETE /sessions/{id}
```

The Load Sensor only calls `GET /accessible`. The prolog and epilog use QRMI,
which calls Warden session endpoints with MUNGE credentials.

Example same-host endpoint:

```text
http://127.0.0.1:4207
```

Verify from the OCS execution host:

```sh
curl http://127.0.0.1:4207/accessible
```

Expected response:

```json
{"is_accessible":true,"message":"Warden ok.","qpu_slots_total":10,"qpu_slots_used":0,"qpu_slots_available":10}
```

### QRMI Config

Install the QRMI config where the Go hooks read it by default:

```text
/etc/qrmi/qrmi_config.json
```

Example:

```json
{
  "resources": [
    {
      "name": "PASQAL_LOCAL",
      "type": "pasqal-local",
      "environment": {
        "QRMI_URL": "http://127.0.0.1:4207"
      }
    }
  ]
}
```

Use the Warden URL reachable from the OCS execution host. Override the config
path with `QRMI_OCS_CONFIG_PATH` only if the site already uses a different
location.

### Load Sensor Config

Install:

```text
/etc/qrmi-ocs-load-sensor.yaml
```

Example:

```yaml
load_sensor:
  enabled: true
  scope: global
  resource_name: qpu_ready
  slots_resource_name: qpu_slots
  provider: warden
  timeout_seconds: 3

warden:
  base_url: http://127.0.0.1:4207
  endpoint: /accessible
  tls_verify: true
```

Use the same Warden base URL as QRMI unless there is a deliberate split between
readiness and session endpoints. Leave `slots_resource_name` unset for the
older static OCS capacity model. Set it to `qpu_slots` for Pasqal Local when
Warden is configured with `qpu_slots_total`.

### OCS Configuration

Run the adapter once:

```sh
adapter setup-qrmi-support \
  --hosts ocs-master \
  --host-value PASQAL_LOCAL \
  --enable-qpu-slots \
  --qpu-slots-capacity 0 \
  --enable-load-sensor \
  --load-sensor-host ocs-master \
  --load-sensor-path /shared/gridware-adapter/bin/adapter/qrmi-ocs-load-sensor \
  --queue all.q \
  --prolog /tmp/qrmi-hooks/qrmi-ocs-prolog \
  --epilog /tmp/qrmi-hooks/qrmi-ocs-epilog
```

Expected scheduler state:

```text
qpu        qpu        STRING == YES NO  NONE 1000
qpu_slots  qpu_slots  INT    <= YES JOB 0    0
qpu_ready  qpu_ready  INT    <= YES NO  0    0

global:     complex_values qpu_ready=1
exec host:  complex_values qpu=PASQAL_LOCAL
host conf:  load_sensor /path/to/qrmi-ocs-load-sensor
queue:      prolog /path/to/qrmi-ocs-prolog
queue:      epilog /path/to/qrmi-ocs-epilog
```

### Smoke Test

Check readiness:

```sh
qhost -F qpu_ready
```

Submit:

```sh
qsub -b y -terse \
  -q all.q@ocs-master \
  -l qpu=PASQAL_LOCAL,qpu_slots=1,qpu_ready=1 \
  -o /tmp/pasqal_local.out \
  -e /tmp/pasqal_local.err \
  /bin/sleep 5
```

With Warden `qpu_slots_total: 10`, a running 5-slot job should make
`qhost -F qpu_slots` report 5 available slots. A second 6-slot job should stay
pending until Warden releases the first session.
After completion, accounting should show:

```text
failed                       0
exit_status                  0
qrmi_acquired_count          1
qrmi_release_success         1
qrmi_release_failed          0
```

Warden should have one session for the OCS job, with `revoked_at` set after the
epilog runs.

The repeatable container check is:

```sh
./demo/qrmi/quickinstall-load-sensor-e2e.sh
```

## Prolog/Epilog Boundary

The Load Sensor is only an early scheduler filter. The OCS prolog still calls
QRMI `IsAccessible` and `Acquire`; the epilog still calls `Release`.

For Pasqal Local, QRMI and Warden use MUNGE-authenticated session operations.
The prolog and Warden must share a MUNGE trust domain. In containers, either
co-locate Warden with the OCS execution host or explicitly share the MUNGE
key/socket trust setup. HTTP reachability alone is sufficient for
`/accessible`, but not for `Acquire` and `Release`.

## Limitations

`qpu_ready` is binary readiness, not cross-scheduler capacity accounting. It
does not represent Pasqal Cloud queue availability.

When Warden owns `qpu_slots_total`, it enforces active Warden sessions only.
That gives Pasqal Local a single global admission point for QRMI clients that
create Warden sessions, including Slurm/SPANK and OCS prolog paths.

Warden uses slots for job-level weighted scheduling; it does not preempt a
running QPU job. Sessions are released explicitly by the scheduler epilog.

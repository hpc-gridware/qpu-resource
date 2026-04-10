# Quickinstall Admin Runbook

Copyright 2026 Pasqal and its contributors.

This runbook is for admins validating and operating QRMI integration on
Gridware/Open Cluster Scheduler (OCS), with Pasqal Cloud only.

The same core scheduler commands are available in `demo/qrmi/quickinstall.sh`.

## 0) Admin Preconditions

- You can run Docker commands on the host.
- QRMI repo exists at `/shared/qrmi`.
- Adapter repo exists at `/shared/gridware-adapter`.
- Gridware `settings.sh` is available at `/opt/ocs/default/common/settings.sh` in `ocs-master`.
- You understand the active resource model:
  - `qpu` is `STRING` with relop `==`
  - each host advertises one backend name (this runbook uses `qpu=EMU_FREE`)
  - jobs request `-l qpu=<backend>`

## 1) Pasqal Cloud Account Setup (`EMU_FREE`)

Pasqal documents free `EMU_FREE` access in the Explorer Offer:
- https://docs.pasqal.com/cloud/set-up/

Pasqal Cloud portal:
- https://portal.pasqal.cloud

### 1.1) Create account and join a project

Follow "Join Pasqal Cloud" in the docs above.

### 1.2) Find your project ID

Follow "Find your project ID" in the same guide.

### 1.3) Configure credentials in OCS containers

Create `~/.pasqal/config` in each node that can submit or execute jobs:

```bash
for c in ocs-master ocs-worker1 ocs-worker2; do
  docker exec "$c" /bin/bash -lc 'mkdir -p /root/.pasqal'
done
```

Then on each container, write:

```ini
username=<your_email>
password=<your_password_or_token_flow>
project_id=<your_project_id>
auth_endpoint=https://authenticate.pasqal.cloud/oauth/token
```

Example command for one container:

```bash
docker exec ocs-master /bin/bash -lc "cat > /root/.pasqal/config <<'EOF'
username=<your_email>
password=<your_password_or_token_flow>
project_id=<your_project_id>
auth_endpoint=https://authenticate.pasqal.cloud/oauth/token
EOF
chmod 600 /root/.pasqal/config"
```

Repeat for `ocs-worker1` and `ocs-worker2`.

## 2) OCS Local Setup (quickinstall)

This section targets `hpc-gridware/quickinstall` `containers/openSUSE/15.6`.

### 2.0) Obtain quickinstall

Recommended location: `/shared`

```bash
cd /shared
git clone https://github.com/hpc-gridware/quickinstall.git
export QUICKINSTALL_ROOT=/shared/quickinstall
```

### 2.1) Start cluster

```bash
cd "${QUICKINSTALL_ROOT}/containers/openSUSE/15.6"
docker compose up -d
```

Expected containers:
- `ocs-master`
- `ocs-worker1`
- `ocs-worker2`

### 2.2) Build adapter binary

From `/shared`:

```bash
docker run --rm --user $(id -u):$(id -g) \
  -e GOCACHE=/tmp/go-build \
  -e GOPATH=/tmp/go \
  -v "$PWD":/work \
  -w /work/gridware-adapter \
  golang:1.24 /bin/sh -lc \
  'export PATH=/usr/local/go/bin:$PATH && go build -buildvcs=false -o /work/gridware-adapter/adapter ./src/cmd/gridware-adapter'
```

Copy to master:

```bash
docker cp /shared/gridware-adapter/adapter ocs-master:/tmp/adapter
docker exec ocs-master /bin/bash -lc 'chown gridware:users /tmp/adapter && chmod +x /tmp/adapter'
```

### 2.3) Build queue hooks

Build prolog and epilog on host:

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
```

Copy hooks and QRMI shared lib to master:

```bash
docker exec ocs-master /bin/bash -lc 'mkdir -p /tmp/qrmi-hooks && chown gridware:users /tmp/qrmi-hooks'
docker cp /shared/gridware-adapter/bin/qrmi-ocs-prolog ocs-master:/tmp/qrmi-hooks/qrmi-ocs-prolog
docker cp /shared/gridware-adapter/bin/qrmi-ocs-epilog ocs-master:/tmp/qrmi-hooks/qrmi-ocs-epilog
docker cp /shared/qrmi/libqrmi-0.12.0/libqrmi.so ocs-master:/tmp/qrmi-hooks/libqrmi.so
docker exec ocs-master /bin/bash -lc 'chown gridware:users /tmp/qrmi-hooks/qrmi-ocs-prolog /tmp/qrmi-hooks/qrmi-ocs-epilog /tmp/qrmi-hooks/libqrmi.so && chmod +x /tmp/qrmi-hooks/qrmi-ocs-prolog /tmp/qrmi-hooks/qrmi-ocs-epilog'
```

### 2.4) Install `qrmi_config.json` for `EMU_FREE`

Write the runtime config on all OCS nodes:

```bash
for c in ocs-master ocs-worker1 ocs-worker2; do
  docker exec "$c" /bin/bash -lc "cat > /etc/slurm/qrmi_config.json <<'JSON'
{
  \"version\": \"1.0\",
  \"resources\": [
    {
      \"name\": \"EMU_FREE\",
      \"type\": \"pasqal-cloud\",
      \"environment\": {
        \"QRMI_PASQAL_CLOUD_PROJECT_ID\": \"<your_project_id>\",
        \"QRMI_PASQAL_CLOUD_AUTH_ENDPOINT\": \"https://authenticate.pasqal.cloud/oauth/token\"
      }
    }
  ]
}
JSON"
done
```

### 2.5) Setup QRMI support (automatic)

```bash
docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && /tmp/adapter setup-qrmi-support --hosts ocs-master,ocs-worker1,ocs-worker2 --host-value EMU_FREE --queue all.q --prolog /tmp/qrmi-hooks/qrmi-ocs-prolog --epilog /tmp/qrmi-hooks/qrmi-ocs-epilog'
```

Verify:

```bash
docker exec ocs-master /bin/bash -lc 'source /opt/ocs/default/common/settings.sh && qconf -sc | grep "^qpu "'
docker exec ocs-master /bin/bash -lc 'source /opt/ocs/default/common/settings.sh && for h in ocs-master ocs-worker1 ocs-worker2; do echo "== $h =="; qconf -se "$h" | grep "^complex_values"; done'
docker exec ocs-master /bin/bash -lc 'source /opt/ocs/default/common/settings.sh && qconf -sq all.q | egrep "^(prolog|epilog)"'
docker exec ocs-master /bin/bash -lc 'source /opt/ocs/default/common/settings.sh && qconf -sconf | grep "^reporting_params"'
```

Expected resource line:

```text
qpu qpu STRING == YES NO NONE 1000
```

### 2.6) Smoke test (`qsub -l qpu=EMU_FREE`)

```bash
docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && qsub -b y -terse -l qpu=EMU_FREE /bin/echo QUICKINSTALL_QSUB_OK'
```

Then inspect accounting:

```bash
docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && qacct -j <job_id>'
docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && qacct -j <job_id> | grep "^qrmi_"'
```

## 3) Pasqal Cloud Pulser Test on OCS

This runs a real Pasqal Cloud task from OCS using QRMI C bindings.

### 3.1) Build the QRMI C Pasqal Cloud example

```bash
cd /shared/qrmi/examples/qrmi/c/pasqal_cloud
mkdir -p build
cd build
cmake -DQRMI_ROOT=/shared/qrmi ..
make
```

### 3.2) Generate a Pulser sequence payload

```bash
source /shared/pyenv/bin/activate
python3 -c "import pulser; from pulser import Pulse, Sequence; from pulser.register import Register; reg=Register({'q0':(-2.5,-2.5),'q1':(2.5,-2.5),'q2':(-2.5,2.5),'q3':(2.5,2.5)}).with_automatic_layout(pulser.DigitalAnalogDevice); seq=Sequence(reg,pulser.DigitalAnalogDevice); seq.declare_channel('rydberg','rydberg_global'); seq.add(Pulse.ConstantPulse(100,2,2,0),'rydberg'); seq.measure('ground-rydberg'); open('/tmp/qrmi_pulser_seq.json','w').write(seq.to_abstract_repr())"
```

### 3.3) Copy binary and payload to `ocs-master`

```bash
docker cp /shared/qrmi/examples/qrmi/c/pasqal_cloud/build/pasqal_cloud ocs-master:/tmp/pasqal_cloud
docker cp /tmp/qrmi_pulser_seq.json ocs-master:/tmp/qrmi_pulser_seq.json
docker exec ocs-master /bin/bash -lc 'chmod +x /tmp/pasqal_cloud'
```

### 3.4) Submit the Pulser cloud job

```bash
docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && qsub -terse -cwd -V -q all.q@ocs-master -l qpu=EMU_FREE -o /tmp/pulser_c_ocs.out -e /tmp/pulser_c_ocs.out -b y /usr/bin/env LD_LIBRARY_PATH=/tmp/qrmi-hooks /tmp/pasqal_cloud EMU_FREE /tmp/qrmi_pulser_seq.json'
```

Inspect:

```bash
docker exec ocs-master /bin/bash -lc 'source /opt/ocs/default/common/settings.sh && qacct -j <job_id>'
docker exec ocs-master /bin/bash -lc 'sed -n "1,260p" /tmp/pulser_c_ocs.out'
```

Expected markers:
- `qrmi-ocs-prolog[INFO]: acquired 1 backend resource(s): EMU_FREE`
- `Selected resource: id=EMU_FREE type=pasqal-cloud`
- final result JSON line from QRMI task result

## 4) Troubleshooting

### 4.1) `qsub -l qpu=EMU_FREE` fails with type errors

Check that `qpu` is still `STRING`:

```bash
docker exec ocs-master /bin/bash -lc 'source /opt/ocs/default/common/settings.sh && qconf -sc | grep "^qpu "'
```

If needed, re-run `setup-qrmi-support` from section `2.5`.

### 4.2) Pasqal Cloud call returns `401 Unauthorized`

- Recheck `~/.pasqal/config` on the execution node (`ocs-master`/workers).
- Recheck `QRMI_PASQAL_CLOUD_PROJECT_ID` in `/etc/slurm/qrmi_config.json`.
- Verify account/project access in Pasqal portal.
- If credentials are correct and still failing, check Pasqal Cloud service status.

### 4.3) Job fails because QRMI shared library is missing

Confirm `libqrmi.so` is copied beside hook binaries:

```bash
docker exec ocs-master /bin/bash -lc 'ls -l /tmp/qrmi-hooks'
```

### 4.4) QRMI logs are missing or too verbose

- Set `RUST_LOG` directly for explicit control.
- Otherwise set `QRMI_OCS_LOG_LEVEL` in prolog runtime environment.
- Numeric levels map as: `2 error`, `3 info`, `4 debug`, `>=5 trace`.

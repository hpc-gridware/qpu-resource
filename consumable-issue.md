# Consumable `qpu` Issue

## Summary

Tried to make `qpu` consumable while keeping it as a backend-name selector (`STRING`).
The scheduler rejects this model.

## What Was Tested

1. `qpu` as `STRING`, `Relop ==`, `Consumable YES`
- Error:
  - `Consumable "qpu" can have only <= as an relational operator`

2. `qpu` as `STRING`, `Relop <=`, `Consumable YES`
- Error:
  - `Complex "qpu" of type "STRING" cannot be a consumable`

## Conclusion

In this OCS/Gridware environment, `STRING` complexes cannot be consumable.
So `qpu` cannot be both:
- a string backend selector (`qpu=test_eagle`)
- and a consumable resource.

## Working Configuration

Keep `qpu` as:

```text
qpu qpu STRING == YES NO NONE 1000
```

This supports backend selection via `-l qpu=<backend>`, but not consumable accounting on that same `STRING` complex.

## Solution: Pair the `STRING` Selector With Numeric Consumables

The `STRING` `qpu` complex stays as the backend selector. Capacity and other
quantitative facets of a quantum resource are modelled as **separate numeric
complexes** which *can* be consumable. The job request combines them with a
comma, which the scheduler treats as an **AND**: the job only starts once
*every* requested complex can be granted on the same host.

`gridware-adapter` should add a numeric consumable next to `qpu`, e.g.
`qpu_slots` (concurrent jobs the backend can run). Additional facets can be
modelled the same way and requested together — for example:

- `qpu_slots`  — concurrent jobs allowed on the backend (capacity / slots).
- `qpu_qubits` — qubits the job needs; host advertises the QPU's qubit count.
- `qpu_shots`  — measurement-shot budget consumed per job.

Each of those is its own complex entry. The scheduler enforces them
independently, but a single `qsub -l ...` line ties them together as one
atomic AND-request.

### Complex Entry Shape

`STRING` selector (unchanged):

```text
qpu qpu STRING == YES NO NONE 1000
```

Numeric consumable (example: `qpu_slots`):

```text
qpu_slots qpu_slots INT  <= YES JOB 0 0
```

Notes on the columns:
- `INT` (or `DOUBLE`) is required — `STRING` cannot be consumable.
- `Relop` must be `<=` for any consumable.
- `Consumable=JOB` decrements the host's pool by exactly the requested
  amount, **once per job**, regardless of how many slots the job is granted.
  See [`YES` vs `JOB`](#yes-vs-job-why-job-is-simpler) below for why this is
  almost certainly what you want for QPU accounting.
- `Default=0` keeps the resource invisible on hosts that do not advertise it
  (the prolog sees `0` and the request cannot be granted there).

### `YES` vs `JOB`: Why `JOB` Is Simpler

OCS/SGE supports three "true" consumable modes:

| `Consumable` | Charged amount per dispatch                             |
| ------------ | ------------------------------------------------------- |
| `YES`        | `request × granted_slots` (per-slot)                    |
| `JOB`        | `request` (per-job, slot-count ignored)                 |
| `HOST`       | `request` per distinct host the job lands on            |

For a parallel job that asks `-l qpu_shots=1024 -pe smp 4`:

- With `Consumable=YES` the scheduler debits **`1024 × 4 = 4096`** shots
  from the host pool. The user has to know to divide their per-job budget
  by the slot count, and changing the `-pe` request silently changes how
  much QPU resource they consume.
- With `Consumable=JOB` the scheduler debits **`1024`** shots, full stop.
  The `-l` value *is* the job's budget, independent of `-pe` / slots.

Quantum resources don't scale with CPU slots — a job either uses the QPU
or it doesn't, and one circuit run isn't "four times as many shots"
because the host code happens to use four CPU cores. `Consumable=JOB` is
therefore the right default for `qpu_*` complexes:

- The `-l` value reads exactly how the user expects ("this job uses
  N qubits / N shots / 1 slot").
- It cannot be accidentally inflated by `-pe`, MPI tasks or array-task
  fan-out.
- Hosts can still cap concurrency simply by setting an integer pool in
  `complex_values` (e.g. `qpu_slots=1`).

### Host Initialisation (`complex_values`)

Numeric consumables only become *available* once a host advertises a
non-zero pool through `complex_values`. There are two scopes:

- Per execution host — set in the host's `complex_values`. Required when
  capacity differs per host (typical for QPU slots tied to a particular
  emulator/backend).
- Cluster-wide — set on the special exec host named `global`. Useful when a
  single shared backend is reachable from any host and you only care about a
  total cap (e.g. "16 concurrent shots cluster-wide").

Per-host wins over `global`; both can be combined.

### Requesting From `qsub`

A single `-l` flag takes a comma-separated list of `key=value` pairs which
are combined as an AND:

```bash
# Backend EMU_FREE, 1 slot of capacity, needs 5 qubits, budget of 1024 shots.
qsub -l qpu=EMU_FREE,qpu_slots=1,qpu_qubits=5,qpu_shots=1024 my-job.sh
```

The job is dispatched only when *all four* are simultaneously satisfiable
on the same execution host. If any of the consumables would go negative,
the job stays queued — exactly the behaviour the original
`STRING`-as-consumable attempt was trying to achieve.

### `gridware-adapter` Wiring (go-clusterscheduler example)

The following Go sketch shows how `gridware-adapter` can register a numeric
consumable next to the existing `qpu` `STRING` selector and seed
`complex_values` on each execution host (and, optionally, `global` for a
cluster-wide cap).

```go
package main

import (
	"fmt"
	"strconv"

	qconf "github.com/hpc-gridware/go-clusterscheduler/pkg/qconf/v9.0"
)

// ensureNumericConsumable creates or refreshes a numeric consumable complex
// entry. Use it for qpu_slots, qpu_qubits, qpu_shots, etc.
func ensureNumericConsumable(qc qconf.QConf, name string) error {
	desired := qconf.ComplexEntryConfig{
		Name:        name,
		Shortcut:    name,
		Type:        qconf.ResourceTypeInt, // STRING cannot be consumable.
		Relop:       "<=",                  // Required for consumables.
		Requestable: "YES",
		Consumable:  qconf.ConsumableJOB, // Per-job, ignores slot count.
		Default:     "0", // Hosts without an explicit pool cannot grant it.
		Urgency:     0,
	}

	if _, err := qc.ShowComplexEntry(name); err != nil {
		return qc.AddComplexEntry(desired)
	}
	return qc.ModifyComplexEntry(name, desired)
}

// setHostCapacity advertises a per-host pool for a numeric consumable, e.g.
// qpu_slots=1 on the host that runs EMU_FREE.
func setHostCapacity(qc qconf.QConf, host, name string, amount int) error {
	cfg, err := qc.ShowExecHost(host)
	if err != nil {
		return fmt.Errorf("show exechost %q: %w", host, err)
	}
	if cfg.ComplexValues == nil {
		cfg.ComplexValues = map[string]string{}
	}
	cfg.Name = host
	cfg.ComplexValues[name] = strconv.Itoa(amount)
	return qc.ModifyExecHost(host, cfg)
}

// setGlobalCapacity puts a cluster-wide cap on the special "global" exec
// host. Per-host values stack on top of (and may further restrict) this.
func setGlobalCapacity(qc qconf.QConf, name string, amount int) error {
	return setHostCapacity(qc, "global", name, amount)
}

func wireQPUConsumables(qc qconf.QConf) error {
	for _, name := range []string{"qpu_slots", "qpu_qubits", "qpu_shots"} {
		if err := ensureNumericConsumable(qc, name); err != nil {
			return err
		}
	}
	if err := setHostCapacity(qc, "ocs-worker1", "qpu_slots", 1); err != nil {
		return err
	}
	if err := setHostCapacity(qc, "ocs-worker1", "qpu_qubits", 27); err != nil {
		return err
	}
	if err := setGlobalCapacity(qc, "qpu_shots", 100000); err != nil {
		return err
	}
	return nil
}
```

The existing `ensureComplexEntry` / `setExecHostResource` helpers in
`src/cmd/gridware-adapter/main.go` already cover the same primitives for the
`STRING` `qpu` selector — the numeric consumables follow the same shape with
`Type=INT`, `Relop="<="` and `Consumable=JOB`.

### Result

- `qpu` keeps its job — selecting *which* backend the job lands on.
- Numeric consumables (`qpu_slots`, `qpu_qubits`, `qpu_shots`, …) carry the
  *accounting* the scheduler refused to put on the `STRING` complex.
- A single `qsub -l qpu=<backend>,qpu_slots=...,qpu_qubits=...` request is
  an AND across all of them, so the job only starts when the backend
  selection *and* every capacity dimension can be honoured on the same
  host.

# Gate Steps Configuration (`.gates.toml`)

## Overview

`.gates.toml` defines verification gate steps for the `mol-decompose-with-gates`
formula. When a plan is decomposed into implementation tasks, gate beads are
automatically inserted between phases. Each gate bead receives the instructions
from these steps, which a gate polecat reads and executes as a review task.

These are **not** the refinery's merge queue gates (command-based, defined in
`settings/config.json` under `merge_queue.gates`). Decomposition gates are
prose-based review tasks executed by agents.

## Schema

The file uses TOML's [array of tables](https://toml.io/en/v1.0.0#array-of-tables)
syntax. Each `[[step]]` defines one gate:

```toml
[[step]]
name = "run-tests"                          # string, required
description = "Run the full test suite"     # string, required
instructions = """                          # string, required
Full prose instructions...
"""
```

### Fields

| Field          | Type   | Required | Description                                         |
|----------------|--------|----------|-----------------------------------------------------|
| `name`         | string | yes      | Short slug identifier (e.g. `run-tests`). Must be unique within the file. Used as the gate bead title suffix. |
| `description`  | string | yes      | One-line human summary. Appears in `bd list` output. |
| `instructions` | string | yes      | Full prose instructions the gate polecat reads and executes. Supports multi-line TOML strings. |

## Cascade Lookup

The `mol-decompose-with-gates` formula resolves gate steps from the first
non-empty source. Sources are checked in order; **no merging** between levels:

1. **Formula var `gate_steps`** — A list of step objects passed as a formula
   variable. Highest priority. Allows per-convoy customization.
2. **Rig-level** — `<rig-root>/.gates.toml` (e.g. `~/gt/gastown/.gates.toml`).
3. **HQ-level** — `<town-root>/.gates.toml` (e.g. `~/gt/.gates.toml`).

If all sources are absent or empty, the formula fails with:
> "no gate steps configured: set gate_steps var or create .gates.toml at rig or hq level"

## Default Steps

The shipped `.gates.toml` includes four default verification gates:

| Step                       | Purpose                                                      |
|----------------------------|--------------------------------------------------------------|
| `run-tests`                | Run the full test suite and report failures                  |
| `architectural-review`     | Pattern adherence, interface boundaries, composition > inheritance |
| `security-review`          | OWASP top 10, injection, auth/authz, input validation        |
| `adversarial-testing-review` | Edge cases, misuse scenarios, concurrency, resource exhaustion |

## Customization

To customize gates for a specific rig, copy the default `.gates.toml` to the
rig root and edit. To override for a single convoy, pass `gate_steps` as a
formula variable when invoking `mol-decompose-with-gates`.

### Adding a step

```toml
[[step]]
name = "performance-review"
description = "Check for performance regressions"
instructions = """
Run benchmarks and compare against baseline...
"""
```

### Removing a step

Delete the `[[step]]` block. The formula uses whatever steps are present.

### Per-convoy override

```bash
gt convoy stage --formula mol-decompose-with-gates \
  --var gate_steps='[{name="quick-check", description="Fast smoke test", instructions="Run make test"}]'
```

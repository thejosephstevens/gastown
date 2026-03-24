# Design: Remove stale `bd agent state` call from `gt sling`

**Bead**: gt-4ly
**Upstream**: steveyegge/gastown#3216, steveyegge/gastown#3254
**Status**: Design ready for review

---

## Problem

Every `gt sling` that spawns a new or reused polecat adds **~3 minutes of dead time** after the
tmux session starts. The delay appears as a series of warnings:

```
⚠ SetAgentState attempt 1 failed, retrying in 625ms: updating agent state: exit status 1
⚠ SetAgentState attempt 2 failed, retrying in 1.1s: ...
...
⚠ could not update agent state after retries: setting agent state after 10 attempts: ...
```

The retries are caused by `gt` calling `bd agent state <id> working` — a command that no longer
exists in beads v0.62.0.

---

## Root Cause

### The removed command

`beads v0.62.0` removed the `bd agent` subcommand group as part of the observable-states
deprecation (ZFC design directive gt-zecmc). Running it produces:

```
Error: unknown command "agent" for "bd"
```

### The call chain

```
StartSession()                              polecat_spawn.go:403
  → SetAgentStateWithRetry("working")       polecat/manager.go:339
    → SetAgentState("working")              polecat/manager.go:1946
      → beads.UpdateAgentState(id, "working")  beads/beads_agent.go:421
        → b.runWithRouting("agent", "state", id, "working")  ← FAILS
```

`UpdateAgentState` was written to:
1. Call `bd agent state <id> <state>` to update the dedicated `agent_state` DB column.
2. Sync the description text's `agent_state` field via `UpdateAgentDescriptionFields`.

Step 1 fails. Step 2 is never reached.

### Why the backoff is so long

`SetAgentStateWithRetry` retries 10 times with exponential backoff (500ms base, 30s cap).
The "unknown command" error is not recognized by `isDoltConfigError()` as a configuration
error, so all 10 attempts execute:

| Attempt | Wait before next |
|---------|-----------------|
| 1       | 500ms           |
| 2       | 1s              |
| 3       | 2s              |
| 4       | 4s              |
| 5       | 8s              |
| 6       | 16s             |
| 7–10    | 30s each        |

Total (without jitter): ~150s ≈ **2.5–3 minutes** per sling.

### Secondary call site

`manager.go:1644` also calls `m.beads.UpdateAgentState(agentID, "spawning")` during
`AllocateAndCreate`. This is a **single direct call** (no retry loop), so it fails fast
with a warning. It does not cause a multi-minute delay.

---

## ZFC Design Context (gt-zecmc)

The ZFC directive established that **tmux is the ground truth for observable states**.
Observable states — `working`, `spawning`, `done`, `idle` — should no longer be tracked
in beads. They are derived from tmux session presence.

Non-observable intentional-pause states (`stuck`, `awaiting-gate`) are still stored in beads
because they represent decisions that can't be discovered from tmux.

The `rig.go` display code already implements this correctly:

```go
// Per gt-zecmc design: tmux is ground truth for observable states.
if hasSession && displayState == polecat.StateDone {
    displayState = polecat.StateWorking   // session alive overrides bead
} else if !hasSession && displayState == polecat.StateWorking {
    displayState = polecat.State("stalled")  // bead stale overrides to stalled
}
```

The problem: `gt sling` still tries to track observable state in beads via the now-removed
`bd agent state` command.

---

## Impact Assessment

| Symptom | Cause |
|---------|-------|
| `gt sling` delays ~3min | `SetAgentStateWithRetry` retries 10x |
| `gt polecat list` shows `spawning` instead of `working` | `agent_state` column stays at "spawning"; description field also stale |
| `gt status` may show stale state | `GetAgentBead` prefers the column, which is never updated past "spawning" |

**Convoy lifecycle**: The bead description mentions "convoys show as immediately closed".
This could not be traced to a direct code path from the `bd agent state` failure.
The convoy closure is triggered only by `checkConvoyCompletion()` when issues are
explicitly closed. The 3-minute delay in `StartSession` should not directly cause convoy
closure. Recommend verifying this symptom separately after applying the primary fix.

---

## Proposed Fix

### Option A (Recommended): Update description only, drop column update

Per the ZFC directive, observable states should not be tracked in beads at all. The
`agent_state` field in the description is still useful for human-readable `bd show`
output. The DB column is no longer authoritative once tmux becomes ground truth.

**File: `internal/beads/beads_agent.go`**

```go
// UpdateAgentState updates the agent_state field in an agent bead's description.
//
// bd agent state was removed in beads v0.62.0 as part of observable-states
// deprecation (ZFC design gt-zecmc). The DB column is no longer maintained
// by gt; tmux session presence is the authoritative source for liveness.
// This function updates the description text only, for human-readable bd show output.
func (b *Beads) UpdateAgentState(id string, state string) (retErr error) {
    defer func() { telemetry.RecordAgentStateChange(context.Background(), id, state, nil, retErr) }()
    if err := b.UpdateAgentDescriptionFields(id, AgentFieldUpdates{AgentState: &state}); err != nil {
        return fmt.Errorf("updating agent state: %w", err)
    }
    return nil
}
```

Also remove the column-preference logic from `GetAgentBead` since the column is no longer
being updated:

```go
// Before (GetAgentBead in beads_agent.go):
fields := ParseAgentFields(issue.Description)
// Prefer the structured agent_state column when present.
// Some writers (for example, `bd agent state`) update the DB column directly
// without rewriting the description text...
if issue.AgentState != "" {
    fields.AgentState = issue.AgentState
}

// After:
fields := ParseAgentFields(issue.Description)
// Note: agent_state column is no longer maintained (bd agent state removed in
// beads v0.62.0, ZFC design gt-zecmc). Description text is authoritative.
```

**File: `internal/daemon/polecat_health_test.go`**

Update `TestCheckPolecatHealth_DBStateOverridesDescription` — this test verifies that the
DB column overrides the description, which is no longer correct. It should be renamed and
updated to verify that description text is the authoritative source.

### Option B (Minimal / band-aid): Fast-fail on "unknown command"

Add "unknown command" to `isDoltConfigError` so the retry loop exits immediately:

```go
func isDoltConfigError(err error) bool {
    // ...existing checks...
    strings.Contains(msg, "unknown command") || // bd agent removed (gt-4ly)
```

This reduces the delay from ~3 minutes to near-zero but does not fix the underlying issue.
The `agent_state` column remains stale at "spawning" permanently. The description field
also stays stale because `UpdateAgentDescriptionFields` is only called after the
`bd agent state` call succeeds.

**Not recommended** except as an emergency hotfix.

---

## Files Changed (Option A)

| File | Change |
|------|--------|
| `internal/beads/beads_agent.go` | `UpdateAgentState`: drop `runWithRouting("agent", "state", ...)`, use description-only path |
| `internal/beads/beads_agent.go` | `GetAgentBead`: remove column-preference block |
| `internal/daemon/polecat_health_test.go` | Update `TestCheckPolecatHealth_DBStateOverridesDescription` to reflect description-as-truth |

No changes needed to `polecat_spawn.go` or `polecat/manager.go` — the callers are correct.
The fix is entirely in the beads adapter layer.

---

## Test Plan

1. `go test ./internal/beads/...` — verify `UpdateAgentState` updates description correctly
2. `go test ./internal/polecat/...` — verify no regressions in polecat lifecycle
3. `go test ./internal/daemon/...` — verify updated health check test
4. Manual: `gt sling <bead> <rig>` — confirm no 3-minute delay, no `SetAgentState` warnings
5. Manual: `gt polecat list` — confirm state shows `working` for active polecats (not `spawning`)

---

## Non-Goals

- Restoring the `agent_state` DB column update via an alternative mechanism. Per ZFC,
  observable states should not be tracked in beads.
- Fixing the convoy lifecycle issue without further investigation (may be a separate bug).
- Changing `SetAgentStateWithRetry` callers — the callers are correctly using the retry
  mechanism for transient Dolt failures; the issue is the underlying command doesn't exist.

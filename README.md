# OpenShell #4009 reproduction (Go SDK)

Live reproduction of the stale-target stop scenario in
[NVIDIA/OpenShell#4009](https://github.com/NVIDIA/OpenShell/issues/4009), using only the
public Go SDK (`github.com/NVIDIA/OpenShell/sdk/go`, pinned in `go.mod`) against a running
Gateway. No database or internal API access.

This is evidence for a missing API capability, not a defect: unconditional `Stop` does what
it documents. The point is that an event-driven controller has no way to say "stop only the
execution I observed".

## What it does

For each of two scenarios it creates one disposable sandbox, then:

1. Waits for Ready and records execution **A**: `Sandbox.ID` and `Status.MainProcessStartedAtMs`.
2. Replaces the execution:
   - **stop/start**: `Stop`, `WaitStopped`, `Start`, `WaitReady` (same sandbox ID, new execution), or
   - **delete/recreate**: `Delete`, wait for NotFound, `Create` under the same name (new sandbox ID).
3. Records the replacement **B** and shows it differs from A.
4. Issues the delayed controller action for A: `Sandboxes().Stop(ctx, workspace, name)`.
   `Stop` takes only workspace and name, so the request cannot be bound to A.
5. Checks whether B was stopped, then deletes the sandbox.

Steps run sequentially; no concurrency is needed because the API has no precondition to race against.

## Run

```sh
go run . --address localhost:8080 --tls-dir /path/to/certs --image <sandbox-image>
```

Exit code `2` means the stale stop affected the replacement.

## Observed

Gateway image `ghcr.io/nvidia/openshell/gateway:374c035962dada63852ec1dea073fea618ed76c5`
(upstream `main` at that commit), SQLite backend, Kubernetes driver, SDK
`v0.0.0-20260930202246-912a077bd641`:

```
== scenario: stop/start
execution A observed: sandbox_id=ab6e796e-... main_started_ms=1791233593773
execution B (replacement) ready: sandbox_id=ab6e796e-... main_started_ms=1791233600462
  same sandbox id: true, same start time: false
delayed stop for A: Sandboxes().Stop(workspace, name)
  stop result: err=<nil>
  replacement B was STOPPED (phase=Stopped)
== scenario: delete/recreate
execution A observed: sandbox_id=9445b2d2-... main_started_ms=1791233611739
execution B (replacement) ready: sandbox_id=ceed82f9-... main_started_ms=1791233621052
  same sandbox id: false, same start time: false
delayed stop for A: Sandboxes().Stop(workspace, name)
  stop result: err=<nil>
  replacement B was STOPPED (phase=Stopped)
RESULT: STALE STOP AFFECTED THE REPLACEMENT in 2/2 scenarios
```

## Takeaways

- Stop/start keeps the sandbox ID, so an ID alone cannot fence it. Only `MainProcessStartedAtMs` differs.
- Delete/recreate changes the ID, but `Stop` cannot accept it.
- In both cases the Gateway accepts the request and stops the current replacement.
- This repo shows only the current behavior. A "fixed" run needs an SDK that exposes a conditional stop, which the pinned public SDK does not.

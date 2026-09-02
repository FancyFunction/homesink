# Homesink — Architecture Documentation

Design for the media sync system specified in [`../requirements.md`](../requirements.md).

**Stack:** Kotlin + Jetpack Compose client · Go + SQLite + ffmpeg server · rootless Podman quadlet with
health-gated auto-update.

| Doc | What is in it |
|---|---|
| [00-ARCHITECTURE.md](00-ARCHITECTURE.md) | System overview, principles, repo layout, storage layout, security model |
| [01-DECISIONS.md](01-DECISIONS.md) | **37 decisions** resolving everything the requirements left open, with reasoning |
| [02-API.md](02-API.md) · [api/openapi.yaml](api/openapi.yaml) | The wire contract, frozen first |
| [03-DATA-MODEL.md](03-DATA-MODEL.md) | SQLite schema, Room schema, invariants |
| [04-WORKPACKAGES-BACKEND.md](04-WORKPACKAGES-BACKEND.md) | 14 backend building blocks |
| [05-WORKPACKAGES-CLIENT.md](05-WORKPACKAGES-CLIENT.md) | 15 client building blocks + canonical German strings |
| [06-ALGORITHMS.md](06-ALGORITHMS.md) | Adaptive scheduling, selection defaults, media pipeline — fully specified |
| [07-DEPLOYMENT.md](07-DEPLOYMENT.md) | Mint/Ubuntu analysis, quadlet, install flow, distribution |
| [08-ROADMAP.md](08-ROADMAP.md) | Dependency graph, milestones, file ownership, risks |

**Implementing a work package?** Read `00`, `01`, then your `WP-*` section and its dependencies.
Do not read the other work packages, and do not edit files you do not own (`08 §4`).

**Open questions** needing a product decision are collected in `01 §13` and `07 §7`. None block
implementation; each has a stated default.

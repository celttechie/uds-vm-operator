# ADR 0001: Two-Stage Engineering Workflow (Python Kopf Prototype to Go Operator)

- **Status:** Accepted
- **Date:** 2026-11-10
- **Milestone:** v0.1.0

## Context
Writing Kubernetes operators directly in Go with `controller-runtime` is standard for production, but introduces iteration friction when discovering evolving upstream APIs (e.g., KubeVirt `VirtualMachine` and custom CDI PR #4340 `Upload.Checksum` schemas), printer columns, and +6% PVC sizing formulas against a live cluster.

## Decision
Adopt a disciplined **two-stage operator engineering workflow**:
1. **Stage 1 (Python `kopf` Prototype, archived in `archive/prototype-kopf/`):** Validate CRD schemas, NIST 800-53 controls (`SC-7`, `AC-3`, `SI-7`), and `+6%` filesystem overhead math using pure, side-effect-free resource builders and a single level-triggered `reconcile()` loop.
2. **Stage 2 (Production Go `controller-runtime` Operator in `pkg/controller/`):** Port the proven builders and unit tests 1:1 to Go, achieving zero-downtime live cluster takeover (`reconciledBy: "kopf-python"` $\rightarrow$ `reconciledBy: "controller-go"`).

## Consequences
- **Positive:** Minimal production container footprint (~28 MiB RSS, no Python runtime), `-race` concurrency verification, and sub-millisecond informer caching.
- **Negative:** One-time porting effort during v0.1.0; mitigated by strict 1:1 structural parity between Python and Go builders.

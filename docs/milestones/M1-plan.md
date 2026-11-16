# Milestone 1 — Monorepo Foundation: Plan, Acceptance Criteria, Results

Status: **Complete — awaiting owner review** (2026-11-16). CI green on run #2.

## Scope (roadmap item 1)
Repository structure, engineering rules, CI, the wire-protocol contract (schemas, compatibility rules, examples, cross-language fixtures), threat model, and ADR upkeep. **No product behaviour.** The agent and integration are buildable skeletons only. Telemetry, pairing, and entities are M2.

## Deliverables
| # | Deliverable | Location |
|---|---|---|
| 1 | Monorepo layout with per-component READMEs | `apps/*`, `packages/protocol`, `scripts`, `tests` |
| 2 | Engineering rules updated with concrete conventions (originals preserved) | `AGENTS.md`, `CLAUDE.md` |
| 3 | Protocol v1 JSON Schemas (2020-12): envelope and all message bodies | `packages/protocol/schemas/` |
| 4 | Compatibility rules and versioning policy | `packages/protocol/COMPATIBILITY.md` |
| 5 | Contract fixtures (valid and invalid) with expected outcomes | `packages/protocol/fixtures/` |
| 6 | Go validator (embedded schemas) + tests running every fixture | `packages/protocol/go` |
| 7 | Python validator tests running every fixture | `packages/protocol/python` |
| 8 | Agent skeleton (`deckbeacon version`), builds linux/amd64 and linux/arm64 | `apps/agent` |
| 9 | CI: Go fmt/vet/test/cross-build, Python lint/test, cross-language contract job, secret scan | `.github/workflows/ci.yml` |
| 10 | Threat model v1 | `docs/security/threat-model.md` |
| 11 | Validation results | this file, §Results |

## Acceptance criteria
- AC1: `go test ./...` passes in `apps/agent` and `packages/protocol/go`. `gofmt` and `go vet` are clean.
- AC2: `pytest` passes in `packages/protocol/python`. `ruff check` is clean.
- AC3: Every fixture under `fixtures/valid` validates, and every fixture under `fixtures/invalid` fails, **in both Go and Python**. The fixture set is enumerated by both test suites from the same directory, so neither language can silently skip files.
- AC4: The agent cross-builds for linux/amd64 and linux/arm64 with `CGO_ENABLED=0`.
- AC5: CI workflow is syntactically valid and runs every job above. Research spikes are excluded.
- AC6: Threat model covers every threat listed in spec 10, plus M0 risks R1–R12.
- AC7: No secrets in the repository (secret scan job, plus local scan before commit).

## Explicitly deferred
- Noise channel implementation and cross-language handshake test vectors → **M2 first task** (Go `flynn/noise`; Python implementation on `cryptography`, with interop tests against Go-generated vectors).
- HA integration code and `pytest-homeassistant-custom-component` → M2.
- Decky (M4) and dashboard (M8) toolchains. Placeholder READMEs only.
- Release signing and the mirror-repo publishing workflow → M9/M10.

## Results (2026-11-16)
Environment: Linux x86_64 container, Go 1.26.11, Python 3.13.18, jsonschema 4.26.0, santhosh-tekuri/jsonschema v6.0.3.

| AC | Result | Evidence |
|---|---|---|
| AC1 Go tests, fmt, vet | **pass** | `go test ./apps/agent/... ./packages/protocol/...` ok. Protocol: 4 top-level tests + 45 fixture subtests. Also passes with `GOWORK=off` (module mode). |
| AC2 Python tests, lint | **pass** | `ruff check`, `ruff format --check` clean. `pytest`: 46 passed (1 schema meta-check + 20 valid + 25 invalid). |
| AC3 cross-language fixtures | **pass** | 20 valid / 25 invalid. Each invalid fixture fails at exactly its pinned `error_path` in **both** Go and Python. `scripts/check_fixtures.py`: 0 problems. |
| AC4 cross-build | **pass** | `deckbeacon` builds for linux/amd64 (ELF x86-64) and linux/arm64 (ELF aarch64), `CGO_ENABLED=0`. |
| AC5 CI workflow | **pass** | `.github/workflows/ci.yml` (jobs: go, protocol-python, fixtures, secrets). Run #1 failed on govulncheck (real finding, below). [Run #2](https://github.com/Jafesu/DeckBeacon/actions/runs/43921535416): all 4 jobs green. |
| AC6 threat model | **pass** | `docs/security/threat-model.md`: T1–T17 cover every spec-10 actor and M0 R1–R12. |
| AC7 no secrets | **pass (local)** | Grep scan for private keys, tokens, JWTs, HA Cloud hostnames, and private IPs: none. Test keys for HV-R1 were kept outside the repo. gitleaks runs in CI. |

### Findings during M1
- **JSON integer precision** (found during HV-R1 measurement): an int64 nanosecond value sent as a JSON number was altered by a float64 decode. Protocol now caps all counters at 2^53−1 (`common#/$defs/counter`). Rule added to `COMPATIBILITY.md`. Fixture `envelope_seq_beyond_2_53` added.

- **Toolchain** (found by CI govulncheck, run #1): Go 1.24 is out of support, and two standard-library `net/url` vulnerabilities (GO-2026-6218, GO-2026-4601) were reachable via schema `$ref` resolution. Moved to Go 1.26, santhosh-tekuri/jsonschema v6.0.3, and `golang.org/x/text` v0.43.0. Bumped Actions to Node-24 majors.

### Known limitations
- Protocol fixtures are hand-authored. Message *semantics* (ordering, dedupe, TTL) are specified in `COMPATIBILITY.md` but enforced only from M2, when agent and integration code exist.
- `actionlint` could not run in the development sandbox. The workflow is validated by GitHub executing it.
- Runtime semantics are untested until M2 code exists (see above).

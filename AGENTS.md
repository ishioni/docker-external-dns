# AGENTS.md — Progress log for future agents

This file documents the implementation status, design decisions, known gaps, and
suggested next steps. Update it as work progresses.

## Status: v0.8 — Change policy support (2026-05-09)

The core pipeline is implemented and covered by an automated test suite (`go test -race ./...`). It now supports A and CNAME records, container-level target/type defaults, per-Traefik-router target/type/skip overrides, strict UniFi HTTP contract tests that exercise the real UniFi client against an in-process API simulator, and external-dns-style change policies.

Two real-world bugs were caught during manual testing and are now regression-guarded by tests:

- UniFi rejects TXT records that include a `ttl` field. Fix: `internal/provider/unifi/client.go` skips TTL when `RecordType == "TXT"`. Guard: `TestCreateTXT_OmitsTTL`.
- UniFi rejects TXT values containing unquoted commas. Fix: `internal/registry/txt.go` wraps the encoded value in `"..."`, strips on decode. Guard: `TestEncodeTXT_IsQuoted` + `TestDecodeTXT_StripsQuotes`.

The core pipeline:

```
Docker daemon → label parser → plan → UniFi static-DNS
                                   ↕
                            TXT ownership registry
```

### Files written

| File | Purpose |
|---|---|
| `cmd/docker-external-dns/main.go` | Entry point, signal handling, wiring |
| `internal/config/config.go` | Env-driven config with validation |
| `internal/config/config_test.go` | Config validation tests, including policy and reconcile interval |
| `internal/source/endpoint.go` | Shared `Endpoint` type |
| `internal/source/traefik.go` | Extracts hostnames from Traefik labels |
| `internal/source/docker.go` | Lists containers, streams Docker events |
| `internal/provider/unifi/types.go` | `DNSRecord` DTO matching UniFi wire format |
| `internal/provider/unifi/client.go` | HTTP client for UniFi static-dns CRUD |
| `internal/provider/unifi/errors.go` | Typed UniFi API/network/data errors |
| `internal/provider/unifi/client_test.go` | Strict UniFi API simulator tests via `httptest.NewServer` |
| `internal/registry/txt.go` | external-dns-compatible TXT ownership encode/decode |
| `internal/plan/plan.go` | Diffs desired vs current, produces Changes |
| `internal/controller/controller.go` | Reconcile loop with event debounce + periodic ticker |
| `internal/controller/types.go` | `Source` and `Provider` interfaces + domain `Event` type |
| `internal/controller/controller_test.go` | Reconcile tests with in-process fakes, policy coverage, and debounce test |
| `internal/controller/unifi_integration_test.go` | End-to-end fake source → real controller → real UniFi client → fake UniFi API tests |
| `Dockerfile` | Multi-stage build → scratch final image |
| `docker-compose.yml` | Example deployment |
| `.env.example` | Deployment env template; keep in sync with config and README |
| `Makefile` | `make build / run / test / vet / docker-build / docker-run` |
| `internal/source/traefik_test.go` | Table tests for label → endpoint extraction |
| `internal/source/docker_test.go` | Endpoints aggregation, name parsing, event filters |
| `internal/registry/txt_test.go` | TXT encode/decode round-trip + ownership tests |
| `internal/plan/plan_test.go` | Diff engine: create/update/delete/replace + safety guards |

## Key design decisions

- **Auth**: X-Api-Key PAT only (UniFi Network 9.0+). No username/password fallback to keep the surface small. Add it later if needed.
- **Record type**: Inferred automatically from the resolved target — IPv4 → A, anything else → CNAME (AAAA explicitly out of scope). No `record-type` label exists; the target string is the single source of truth.
- **Opt-in labels**: Only `external-dns.enabled=true` is required. Hostname extraction still reads Traefik router rules, but `traefik.enable=true` is no longer a gate.
- **TXT prefix**: Optional global `TXT_PREFIX` env var (default `""`). The full TXT key is `{TXT_PREFIX}{record_type_lowercase}-{hostname}`, e.g. with `TXT_PREFIX=talos.` the companion TXT for `postgres.example.com` lives at `talos.a-postgres.example.com`. An empty prefix gives the legacy `a-foo.example.com` format, which is wire-compatible with kubernetes-sigs/external-dns using `--txt-prefix=%{record_type}-`. The `TXT_OWNER` env var (default `docker-external-dns`) identifies which records this agent owns.
- **Policy**: `POLICY` defaults to `sync`. `sync` applies creates, updates, replacements, stale deletes, and orphan TXT cleanup. `upsert-only` applies creates, updates, and owned A/CNAME replacements, but skips stale deletes and orphan TXT cleanup. `create-only` applies only creates.
- **Ownership safety**: Records without matching owner TXT are never deleted.
- **Debounce**: Docker events are debounced 2s before triggering a reconcile to coalesce rapid restarts.

## Label vocabulary

Container-level defaults apply to all routers from the container:

```yaml
external-dns.enabled: "true"
external-dns.target: "<ip-or-hostname>"  # IPv4 → A, hostname → CNAME
```

Per-router overrides are matched by router name from `traefik.http.routers.<name>.rule`:

```yaml
external-dns.routers.<name>.target: "<ip-or-hostname>"
external-dns.routers.<name>.skip: "true"
```

Precedence per router: per-router override → container-level → global default (`DEFAULT_TARGET`). Record type is auto-detected from whichever target wins.

## Wire format reference

UniFi endpoint: `POST /proxy/network/v2/api/site/{site}/static-dns`

```json
{ "key": "foo.example.com", "record_type": "A", "value": "10.1.2.241", "ttl": 300, "enabled": true }
```

TXT ownership value:
```
heritage=external-dns,external-dns/owner=docker-external-dns,external-dns/resource=docker/myapp
```

Source for UniFi wire format: https://github.com/kashalls/external-dns-unifi-webhook

## Test coverage

Run with `make test` or `go test -race ./...`. All tests use stdlib only (no testcontainers, no Docker daemon or real UniFi controller needed).

| Package | What it covers |
|---|---|
| `internal/source` | Label → endpoint extraction: enable-flag gating, single/multi `Host()`, `\|\|` joining, `HostRegexp` skip, unsubstituted `${VAR}` skip, multi-router merging, container/router target and record-type overrides, router skip. |
| `internal/registry` | `TXTKey` and `ParseTXTKey` formatting/parsing, `EncodeTXT` always quoted, `DecodeTXT` round-trip + quote stripping, rejects non-heritage values, `IsOwnedBy` cross-owner matrix. |
| `internal/plan` | All Create/Update/Delete/Replace branches for A and CNAME plus the three safety rules: no update without our TXT, no update if TXT belongs to another owner, no delete without our TXT. |
| `internal/provider/unifi` | HTTP wire format: list shape, `_id`, `X-Api-Key`, `Accept`, `Content-Type`, A/CNAME include `ttl`, **TXT omits `ttl`**, PUT/DELETE URLs, typed API/network/data errors, dry-run makes no mutation calls. |
| `internal/controller` | Reconcile → apply flow: create/update/delete record+TXT pairs, CNAME pair creation, all three ownership safety rules, error-continues behaviour, debounce → reconcile event path, and integration tests through the real UniFi client against a strict fake UniFi API. |
| `internal/source` (docker) | Endpoints aggregation across multiple containers, name slash-stripping, ID fallback, list error propagation, event filter correctness, channel pass-through. |

## Known gaps / future work

- [x] **UniFi response validation**: strict tests now cover lower-case UniFi JSON fields, `_id` record IDs, request headers, TTL behavior, UniFi-style error JSON, invalid JSON, and controller flows through the real UniFi client.
- [ ] **Standalone non-Traefik hosts**: add explicit labels such as
      `external-dns.hosts.<name>.*` for endpoints that do not have Traefik
      router rules.
- [ ] **Prometheus metrics**: `records_created_total`, `records_deleted_total`,
      `reconcile_errors_total`, `reconcile_duration_seconds`.

## Dependency notes

- `github.com/docker/docker` — Docker SDK for Go; used for container listing and event streaming.
- No external HTTP client lib — uses stdlib `net/http`.
- No external logging lib — uses stdlib `log/slog` (Go 1.21+).
- Go 1.26+ required.

## Maintenance notes

- Keep `.env.example`, `README.md`, and `docker-compose.yml` in sync whenever environment variables or deployment-facing label examples change. Users are expected to copy `.env.example` for real deployments.

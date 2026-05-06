# AGENTS.md — Progress log for future agents

This file documents the implementation status, design decisions, known gaps, and
suggested next steps. Update it as work progresses.

## Status: v0.5 — CNAME and target override support (2026-05-07)

The core pipeline is implemented, manually verified against a real UniFi controller, and covered by an automated test suite (`go test -race ./...`). It now supports A and CNAME records, container-level target/type defaults, and per-Traefik-router target/type/skip overrides.

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
| `internal/source/endpoint.go` | Shared `Endpoint` type |
| `internal/source/traefik.go` | Extracts hostnames from Traefik labels |
| `internal/source/docker.go` | Lists containers, streams Docker events |
| `internal/provider/unifi/types.go` | `DNSRecord` DTO matching UniFi wire format |
| `internal/provider/unifi/client.go` | HTTP client for UniFi static-dns CRUD |
| `internal/registry/txt.go` | external-dns-compatible TXT ownership encode/decode |
| `internal/plan/plan.go` | Diffs desired vs current, produces Changes |
| `internal/controller/controller.go` | Reconcile loop with event debounce + periodic ticker |
| `internal/controller/types.go` | `Source` and `Provider` interfaces + domain `Event` type |
| `internal/controller/controller_test.go` | Reconcile tests with in-process fakes + debounce test |
| `Dockerfile` | Multi-stage build → scratch final image |
| `docker-compose.yml` | Example deployment |
| `docker-compose.mock.yml` | Local test stack: agent + traefik/whoami container |
| `Makefile` | `make build / run / mock / docker-mock / test / vet` |
| `internal/source/traefik_test.go` | Table tests for label → endpoint extraction |
| `internal/source/docker_test.go` | Endpoints aggregation, name parsing, event filters |
| `internal/registry/txt_test.go` | TXT encode/decode round-trip + ownership tests |
| `internal/plan/plan_test.go` | Diff engine: create/update/delete + safety guards |
| `internal/provider/unifi/client_test.go` | HTTP client tests via `httptest.NewServer` |

## Key design decisions

- **Auth**: X-Api-Key PAT only (UniFi Network 9.0+). No username/password fallback to keep the surface small. Add it later if needed.
- **Record type**: A and CNAME records are supported. The target field carries an IP for A records and a hostname for CNAME records; UniFi performs final validation.
- **Opt-in labels**: Only `external-dns.enabled=true` is required. Hostname extraction still reads Traefik router rules, but `traefik.enable=true` is no longer a gate.
- **TXT prefix**: Optional global `TXT_PREFIX` env var (default `""`). The full TXT key is `{TXT_PREFIX}{record_type_lowercase}-{hostname}`, e.g. with `TXT_PREFIX=talos.` the companion TXT for `postgres.ishioni.casa` lives at `talos.a-postgres.ishioni.casa`. An empty prefix gives the legacy `a-foo.example.com` format, which is wire-compatible with kubernetes-sigs/external-dns using `--txt-prefix=%{record_type}-`. The `TXT_OWNER` env var (default `docker-external-dns`) identifies which records this agent owns.
- **Ownership safety**: Records without matching owner TXT are never deleted.
- **Debounce**: Docker events are debounced 2s before triggering a reconcile to coalesce rapid restarts.

## Label vocabulary

Container-level defaults apply to all routers from the container:

```yaml
external-dns.enabled: "true"
external-dns.target: "<ip-or-hostname>"
external-dns.record-type: "A" # or "CNAME"
```

Per-router overrides are matched by router name from `traefik.http.routers.<name>.rule`:

```yaml
external-dns.routers.<name>.target: "<ip-or-hostname>"
external-dns.routers.<name>.record-type: "A" # or "CNAME"
external-dns.routers.<name>.skip: "true"
```

Precedence per router: per-router override → container-level → global default (`DEFAULT_TARGET_IP` / `A`).

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

Run with `make test` or `go test -race ./...`. All tests use stdlib only (no testcontainers, no Docker daemon needed).

| Package | What it covers |
|---|---|
| `internal/source` | Label → endpoint extraction: enable-flag gating, single/multi `Host()`, `\|\|` joining, `HostRegexp` skip, unsubstituted `${VAR}` skip, multi-router merging, container/router target and record-type overrides, router skip. |
| `internal/registry` | `TXTKey` and `ParseTXTKey` formatting/parsing, `EncodeTXT` always quoted, `DecodeTXT` round-trip + quote stripping, rejects non-heritage values, `IsOwnedBy` cross-owner matrix. |
| `internal/plan` | All Create/Update/Delete branches for A and CNAME plus the three safety rules: no update without our TXT, no update if TXT belongs to another owner, no delete without our TXT. |
| `internal/provider/unifi` | HTTP wire format: list shape, `X-Api-Key` header, A/CNAME include `ttl`, **TXT omits `ttl`**, PUT/DELETE URLs, error propagation, dry-run makes no calls. |
| `internal/controller` | Reconcile → apply flow: create/update/delete record+TXT pairs, CNAME pair creation, all three ownership safety rules, error-continues behaviour, debounce → reconcile event path. |
| `internal/source` (docker) | Endpoints aggregation across multiple containers, name slash-stripping, ID fallback, list error propagation, event filter correctness, channel pass-through. |

## Known gaps / future work

- [ ] **UniFi response validation**: first real deployment may reveal mismatches in
      JSON field casing (`Key` vs `key`). Check `_id` vs `id` in list vs create
      responses — UniFi sometimes returns different shapes.
- [ ] **Standalone non-Traefik hosts**: add explicit labels such as
      `external-dns.hosts.<name>.*` for endpoints that do not have Traefik
      router rules.
- [ ] **Username/password fallback**: for UniFi Network < 9.0 PAT support. The
      kashalls webhook does CSRF-token refresh on each response — mirror that.
- [ ] **Prometheus metrics**: `records_created_total`, `records_deleted_total`,
      `reconcile_errors_total`, `reconcile_duration_seconds`.
- [ ] **Multiple record types**: AAAA (IPv6), MX — unlikely needed but keep the
      `RecordType` field propagated through the pipeline so adding them is trivial.
- [ ] **Graceful cleanup on shutdown**: optionally delete all owned records when the
      agent exits (controlled by `CLEANUP_ON_EXIT` env var).
- [x] **CI**: `.github/workflows/ci.yml` runs `go vet` + `go test -race` on PRs. `.github/workflows/release.yml` builds multi-arch Docker image (amd64 + arm64, native runners) and pushes to `ghcr.io/ishioni/docker-external-dns` when a `v*` tag is pushed.

## Dependency notes

- `github.com/docker/docker` — Docker SDK for Go; used for container listing and event streaming.
- No external HTTP client lib — uses stdlib `net/http`.
- No external logging lib — uses stdlib `log/slog` (Go 1.21+).
- Go 1.23+ required.

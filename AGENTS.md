# AGENTS.md — Progress log for future agents

This file documents the implementation status, design decisions, known gaps, and
suggested next steps. Update it as work progresses.

## Status: v0.2 — manually verified end-to-end + automated test coverage (2026-05-05)

The core pipeline is implemented, manually verified against a real UniFi controller, and covered by an automated test suite (`go test ./...`).

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
| `Dockerfile` | Multi-stage build → scratch final image |
| `docker-compose.yml` | Example deployment |
| `docker-compose.mock.yml` | Local test stack: agent + traefik/whoami container |
| `Makefile` | `make build / run / mock / docker-mock / test / vet` |
| `internal/source/traefik_test.go` | Table tests for label → endpoint extraction |
| `internal/registry/txt_test.go` | TXT encode/decode round-trip + ownership tests |
| `internal/plan/plan_test.go` | Diff engine: create/update/delete + safety guards |
| `internal/provider/unifi/client_test.go` | HTTP client tests via `httptest.NewServer` |

## Key design decisions

- **Auth**: X-Api-Key PAT only (UniFi Network 9.0+). No username/password fallback to keep the surface small. Add it later if needed.
- **Record type**: A records only. CNAMEs excluded because they don't work with wildcard domains in UniFi.
- **Opt-in labels**: Both `traefik.enable=true` AND `external-dns.enabled=true` must be set. This avoids accidentally managing services that only intend to use Traefik.
- **TXT prefix**: `%{record_type}-` → `a-foo.example.com` for `foo.example.com`. Wire-compatible with kubernetes-sigs/external-dns default prefix.
- **Ownership safety**: Records without matching owner TXT are never deleted.
- **Debounce**: Docker events are debounced 2s before triggering a reconcile to coalesce rapid restarts.

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
| `internal/source` | Label → endpoint extraction: enable-flag gating, single/multi `Host()`, `\|\|` joining, `HostRegexp` skip, unsubstituted `${VAR}` skip, multi-router merging. |
| `internal/registry` | `TXTKey` formatting, `EncodeTXT` always quoted, `DecodeTXT` round-trip + quote stripping, rejects non-heritage values, `IsOwnedBy` cross-owner matrix. |
| `internal/plan` | All Create/Update/Delete branches plus the three safety rules: no update without our TXT, no update if TXT belongs to another owner, no delete without our TXT. |
| `internal/provider/unifi` | HTTP wire format: list shape, `X-Api-Key` header, A includes `ttl`, **TXT omits `ttl`**, PUT/DELETE URLs, error propagation, dry-run makes no calls. |

Not yet covered (would need an interface refactor):

- `internal/controller/controller.go` — full reconcile → apply flow. The controller currently depends on concrete `*source.DockerSource` and `*unifi.Client`. Extracting small interfaces (`Source.Endpoints`, `Provider.{List,Create,Update,Delete}Record`) would unlock a fake-backed reconcile test.
- `internal/source/docker.go` — Docker daemon interaction. Same blocker.

## Known gaps / future work

- [ ] **Controller-level reconcile test**: the seam needed for this is small —
      define `type Source interface { Endpoints(ctx) ([]*Endpoint, error); Events(ctx) (...) }`
      and `type Provider interface { ListRecords / CreateRecord / UpdateRecord / DeleteRecord }`,
      then have `Controller` accept those instead of concrete types. With that
      done, write a fake `Source` returning canned endpoints, a fake `Provider`
      that records calls, and assert the calls produced by one reconcile cycle.
- [ ] **UniFi response validation**: first real deployment may reveal mismatches in
      JSON field casing (`Key` vs `key`). Check `_id` vs `id` in list vs create
      responses — UniFi sometimes returns different shapes.
- [ ] **CNAME support**: add a per-container `external-dns.record-type=CNAME` label
      and `external-dns.target=traefik.example.com` override once tested with UniFi.
- [ ] **Per-container target override**: `external-dns.target=<ip>` label to override
      the global `TARGET_IP` for a single container.
- [ ] **Username/password fallback**: for UniFi Network < 9.0 PAT support. The
      kashalls webhook does CSRF-token refresh on each response — mirror that.
- [ ] **Prometheus metrics**: `records_created_total`, `records_deleted_total`,
      `reconcile_errors_total`, `reconcile_duration_seconds`.
- [ ] **Multiple record types**: AAAA (IPv6), MX — unlikely needed but keep the
      `RecordType` field propagated through the pipeline so adding them is trivial.
- [ ] **Graceful cleanup on shutdown**: optionally delete all owned records when the
      agent exits (controlled by `CLEANUP_ON_EXIT` env var).
- [ ] **CI**: add a GitHub Actions workflow (`go build`, `go vet`, `go test ./...`).

## Dependency notes

- `github.com/docker/docker` — Docker SDK for Go; used for container listing and event streaming.
- No external HTTP client lib — uses stdlib `net/http`.
- No external logging lib — uses stdlib `log/slog` (Go 1.21+).
- Go 1.23+ required.

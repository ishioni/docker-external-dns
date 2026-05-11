# dexd

External DNS, but for Docker!

`dexd` is a lightweight Go agent that watches your Docker daemon and
automatically creates/removes static DNS records on a **UniFi OS local
controller** (UDM/UDR/UDM-Pro) for services exposed via Traefik.

It mirrors the ownership-tracking model of
[kubernetes-sigs/external-dns](https://github.com/kubernetes-sigs/external-dns),
so it can safely coexist with a Kubernetes external-dns instance pointing at
the same UniFi controller.

## How it works

1. Scans running containers opted in with `dexd.enabled=true`.
2. Extracts hostnames from `traefik.http.routers.<name>.rule: Host(\`...\`)` labels.
3. Creates an **A** or **CNAME** record and a companion **TXT ownership record**
   on UniFi for each hostname.
4. Listens for Docker lifecycle events (start/stop/remove) and reconciles on a
   periodic interval — records are removed when containers disappear and new
   ones appear within seconds.

### Ownership safety

For every managed record at `foo.example.com`, a TXT record is created at
`{record_type}-foo.example.com` with value:

```text
heritage=external-dns,external-dns/owner=<TXT_OWNER>,external-dns/resource=docker/<container-name>
```

Records **without** a matching ownership TXT are never touched — externally
created records are safe.

## Deployment

```yaml
# .env
UNIFI_HOST=https://10.1.2.1
UNIFI_API_KEY=<PAT from UniFi Network settings>
DEFAULT_TARGET=10.1.2.241
```

```bash
docker compose up -d
```

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `UNIFI_HOST` | **required** | UniFi controller URL, e.g. `https://10.1.2.1` |
| `UNIFI_API_KEY` | **required** | Personal Access Token from UniFi Network |
| `DEFAULT_TARGET` | **required** | Default target. IPv4 → A record, hostname → CNAME |
| `UNIFI_SITE` | `default` | UniFi site name |
| `UNIFI_INSECURE_SKIP_VERIFY` | `true` | Skip TLS verification (self-signed certs) |
| `TXT_OWNER` | `docker-external-dns` | Scopes TXT ownership; change if running multiple instances |
| `TXT_PREFIX` | empty | Optional prefix for ownership TXT record names |
| `POLICY` | `sync` | Change policy: `sync`, `upsert-only`, or `create-only` |
| `DEFAULT_TTL` | `auto` | TTL for created A/CNAME records. Use `auto` to let UniFi choose, or a positive integer. |
| `RECONCILE_INTERVAL` | `5m` | How often to run a full reconcile |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `LOG_FORMAT` | `text` | `text` or `json` |
| `DRY_RUN` | `false` | List current UniFi records and log planned changes without mutating UniFi |
| `METRICS_ADDR` | `:8080` | Address for the Prometheus metrics HTTP server. Empty disables metrics. |

### Policy

`POLICY` controls which planned changes are applied:

| Policy | Creates | Updates | A/CNAME replacements | Deletes |
| --- | --- | --- | --- | --- |
| `sync` | yes | yes | yes | yes |
| `upsert-only` | yes | yes | yes | no stale-record or orphan-TXT cleanup |
| `create-only` | yes | no | no | no |

`upsert-only` allows A/CNAME replacements because they are updates by intent. UniFi may require deleting the old owned record before creating the replacement record with the same hostname.

## Metrics

Prometheus metrics are exposed at `/metrics` on `METRICS_ADDR`. The example
Docker Compose file publishes the default metrics listener on host port `8080`.

Useful alerting metrics:

```promql
time() - dexd_reconcile_last_success_timestamp_seconds > 900
increase(dexd_reconcile_total{result="error"}[10m]) > 0
increase(dexd_changes_total{result="error"}[10m]) > 0
increase(dexd_provider_errors_total[10m]) > 0
```

An example Prometheus Operator rule file is available at `deploy/prometheusrule.yaml`.

Useful dashboard metrics:

- `dexd_reconcile_total`
- `dexd_reconcile_duration_seconds`
- `dexd_reconcile_last_success_timestamp_seconds`
- `dexd_plan_desired_records`
- `dexd_plan_current_records`
- `dexd_plan_changes`
- `dexd_changes_total`
- `dexd_provider_requests_total`
- `dexd_provider_request_duration_seconds`
- `dexd_provider_errors_total`
- `dexd_docker_events_total`

## Container labels

Add `dexd.enabled=true` to any service you want managed:

```yaml
labels:
  dexd.enabled: "true"
  traefik.http.routers.myapp.rule: Host(`myapp.example.com`)
  # ... other traefik labels
```

Multiple hosts in a single rule are all created:

```yaml
traefik.http.routers.myapp.rule: Host(`foo.example.com`) || Host(`bar.example.com`)
```

`HostRegexp(...)` entries are skipped — they cannot be materialized into DNS records.

### Record type detection

Record type is inferred automatically from the target value: an **IPv4 address** produces an **A record**, and a **hostname** produces a **CNAME**. There is no `record-type` label — the target string is the single source of truth.

A container-level target override applies to every router from that container:

```yaml
labels:
  dexd.enabled: "true"
  dexd.target: "traefik.example.com"   # hostname → CNAME for all routers
```

Per-router target overrides take precedence, and each router's type is detected independently:

```yaml
labels:
  dexd.enabled: "true"
  traefik.http.routers.s3.rule: Host(`bucket.example.com`)
  traefik.http.routers.console.rule: Host(`console.example.com`)
  dexd.routers.console.target: "traefik.example.com"
  # bucket.example.com → A (uses DEFAULT_TARGET, an IP)
  # console.example.com → CNAME (hostname override)
```

### Rename compatibility

`dexd` still accepts the old `external-dns.*` Docker labels as compatibility
aliases. Prefer `dexd.*` for new deployments; if both are present, `dexd.*`
wins.

The default `TXT_OWNER` remains `docker-external-dns` so existing records
created before the rename are still recognized. Change it only if you are
starting from a clean zone or intentionally want a separate ownership scope.

## Getting a UniFi PAT

In UniFi Network, go to **Settings → Admins & Users → [your admin] → API Access**
and create a new Personal Access Token. UniFi Network 9.0+ is required for PAT
support.

## Building

```bash
go build ./cmd/dexd
```

or with Docker:

```bash
docker build -t dexd .
```

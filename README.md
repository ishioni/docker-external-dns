# docker-external-dns

`docker-external-dns` is a lightweight Go agent that watches your Docker daemon
and automatically creates/removes static DNS records on a **UniFi OS local
controller** (UDM/UDR/UDM-Pro) for services exposed via Traefik.

It mirrors the ownership-tracking model of
[kubernetes-sigs/external-dns](https://github.com/kubernetes-sigs/external-dns),
so it can safely coexist with a Kubernetes external-dns instance pointing at
the same UniFi controller.

## How it works

1. Scans running containers opted in with `external-dns.enabled=true`.
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
| `RECONCILE_INTERVAL` | `5m` | How often to run a full reconcile |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `LOG_FORMAT` | `text` | `text` or `json` |
| `DRY_RUN` | `false` | List current UniFi records and log planned changes without mutating UniFi |

## Container labels

Add `external-dns.enabled=true` to any service you want managed:

```yaml
labels:
  external-dns.enabled: "true"
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
  external-dns.enabled: "true"
  external-dns.target: "traefik.example.com"   # hostname → CNAME for all routers
```

Per-router target overrides take precedence, and each router's type is detected independently:

```yaml
labels:
  external-dns.enabled: "true"
  traefik.http.routers.s3.rule: Host(`bucket.example.com`)
  traefik.http.routers.console.rule: Host(`console.example.com`)
  external-dns.routers.console.target: "traefik.example.com"
  # bucket.example.com → A (uses DEFAULT_TARGET, an IP)
  # console.example.com → CNAME (hostname override)
```

## Getting a UniFi PAT

In UniFi Network, go to **Settings → Admins & Users → [your admin] → API Access**
and create a new Personal Access Token. UniFi Network 9.0+ is required for PAT
support.

## Building

```bash
go build ./cmd/docker-external-dns
```

or with Docker:

```bash
docker build -t docker-external-dns .
```

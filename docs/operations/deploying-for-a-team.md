---
id: deploying-for-a-team
title: Deploying for a Team (Kubernetes + Keycloak)
sidebar_label: Deploying for a Team
description: 'Run the server edition (mcpproxy-server) on Kubernetes behind an ingress with Keycloak SSO: single-replica Deployment, group-based access, audit-to-stdout, probes, and the persistent-volume rule for --config/--data-dir.'
keywords: [kubernetes, deployment, keycloak, oidc, server edition, ingress, public_url, trusted_proxies, access, audit_log, single replica, probes]
---

# Deploying for a Team (Kubernetes + Keycloak)

This is a worked walkthrough of putting the **server edition**
(`mcpproxy-server`, the Docker image built from the repository `Dockerfile`
with `-tags server`) in front of a small team, behind an ingress, with
Keycloak as the identity provider. Every setting named here is documented in
full in [`server_edition`](../configuration/config-file.md#server-edition) and
[`audit_log`](../configuration/config-file.md#audit_log-edition-neutral-jsonl-audit-record);
this page assembles them into one deployment and states the constraints the
reference pages leave implicit (single replica, volume layout, `Recreate`).
The complete runnable example behind every JSON fragment below is
[`docs/operations/examples/deploying-for-a-team.json`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/operations/examples/deploying-for-a-team.json)
— it is loaded and validated by `internal/config/deploy_guide_example_test.go`
(`-tags server`) on every CI run, so it can never silently drift from what
`Config.Validate` actually accepts.

Any other OIDC provider (Okta, Auth0, Authentik, Entra ID) drops into the same
manifest — only the `oauth.issuer_url`/`client_id`/`client_secret` and the
groups-claim table in [Server Multi-User Authentication](../development/server-edition-multiuser-auth.md)
change; the Kubernetes shape below is provider-neutral.

## The single-replica contract

**Run exactly one replica.** The server edition holds pending OAuth login
state (the PKCE verifier + nonce between `/auth/login` and `/auth/callback`),
the SSE `/events` per-frame principal, and the entire tool index/BBolt config
database in-process, with no shared cross-replica store. A second replica
would answer a fraction of logins with `state mismatch` (whichever pod didn't
see the redirect) and open a second, independent `config.db` if it also
mounted its own volume, or fail to open a shared one (BBolt takes an exclusive
file lock — a second process against the same `config.db` is a boot-time DB
lock, exit code 3). This is documented as a hard limit, not a tuning knob: see
[Still-open Spec 105 items / single-replica assumption](../features/agent-tokens.md#server-edition-incident-response)
and [Server Architecture](../development/server-edition-multiuser-auth.md#server-architecture).

That single-instance requirement, not scaling headroom, is why the Deployment
below uses `replicas: 1` and `strategy: Recreate` rather than
`RollingUpdate`: a rolling update briefly runs the old and new pod together
against the same PVC, and the old pod's still-open BBolt lock makes the new
pod's boot fail with exit code 3 (database locked). `Recreate` tears the old
pod down — releasing the lock — before the new one starts, at the cost of a
short outage on every deploy. There is no StatefulSet, headless Service, or
pod-anti-affinity trick that turns this into a safe multi-replica rollout; the
fix is a faster `Recreate` (small image, `initialDelaySeconds` tuned to your
storage) or accepting the gap.

## Persistent volume: `--config` and `--data-dir`

The container's `ENTRYPOINT` is
`mcpproxy serve --listen 0.0.0.0:8080` (see the repository `Dockerfile`); a
Kubernetes `args` override adds `--config` and `--data-dir` explicitly rather
than relying on the image's defaults, because both must point inside the
**same** mounted volume:

- `--data-dir /data` is where `config.db` (BBolt — sessions, users, agent
  tokens, personal servers), `index.bleve/` (the BM25 tool index) and
  `logs/` live. This is the directory that must survive a pod restart.
- `--config /data/mcp_config.json` keeps the JSON config file **on the same
  volume** as the data directory. Putting the config file on a separate
  ConfigMap-backed mount while `--data-dir` is a PVC works for the first
  boot, but every runtime edit through `PATCH /api/v1/config` or the Settings
  UI writes back to the config file path — on a read-only ConfigMap mount
  that write fails, and on two different volumes a pod reschedule can see
  them drift out of sync (the file watcher hot-reloads whatever
  `--config` currently resolves to, independent of `--data-dir`).

Seed the initial file into the PVC once (an init container, or `kubectl cp`
after the first boot) rather than mounting it from a ConfigMap:

```yaml
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: mcpproxy-data
```

## The config file

The block below is the `server_edition`-relevant slice of
[`deploying-for-a-team.json`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/operations/examples/deploying-for-a-team.json)
(trimmed — the full file also seeds two `mcpServers` entries so the access
map below has something to grant):

```json
{
  "listen": "0.0.0.0:8080",
  "trusted_proxies": ["10.42.0.0/16"],
  "trusted_hosts": ["mcp.example.com"],
  "require_mcp_auth": true,
  "audit_log": {
    "enabled": true,
    "stdout": true
  },
  "server_edition": {
    "enabled": true,
    "admin_emails": ["platform-team@example.com"],
    "public_url": "https://mcp.example.com",
    "session_cookie_secure": "auto",
    "credential_encryption_key": "${env:MCPPROXY_CRED_KEY}",
    "oauth": {
      "provider": "oidc",
      "issuer_url": "https://keycloak.example.com/realms/team",
      "client_id": "mcpproxy",
      "client_secret": "${env:OIDC_CLIENT_SECRET}",
      "scopes": ["openid", "profile", "email", "groups"],
      "groups_claim": "groups",
      "email_verified_policy": "refuse_false",
      "display_name": "Example Keycloak"
    },
    "access": {
      "group_servers": {
        "mcpproxy-engineering": ["github", "ast-grep"],
        "mcpproxy-admins": ["*"]
      },
      "default_servers": []
    }
  }
}
```

### `public_url` and `trusted_proxies` — why both

`10.42.0.0/16` is MicroK8s's default pod CIDR; use your cluster's actual pod
or ingress-controller source range. Both keys matter independently, and
leaving either out produces a specific, documented failure — not a generic
misconfiguration:

- Without `public_url`, the OAuth `redirect_uri` is derived from the
  in-cluster request (`Host`/`X-Forwarded-Host`), so it depends on the
  ingress forwarding the *public* hostname rather than an internal Service
  name — set it explicitly and this class of bug disappears.
- Without `trusted_proxies` naming the ingress's pod range, `X-Forwarded-Proto`
  is ignored, `session_cookie_secure: auto` falls back to "not https", and
  the session cookie is issued **without** `Secure` even though the browser
  reached the service over TLS at the ingress.

Full mechanics: [`public_url` and `trusted_proxies` in a container](../configuration/config-file.md#public_url-and-trusted_proxies-in-a-container).

### `session_cookie_secure`

Leave it at the default `auto`. It resolves to `Secure` because `public_url`
is `https://…` — no need to hardcode `true`, and hardcoding `false` here would
be refused at boot (`session_cookie_secure=false cannot be combined with an
https public_url or tls.enabled`).

### The `access` block: onboarding is adding someone to a Keycloak group

`server_edition.access` is what turns "the platform team added Priya to the
`mcpproxy-engineering` Keycloak group" into "Priya can call the `github` and
`ast-grep` servers, and nothing else." It is **absent by default**
(today's Shared-only semantics: every signed-in tenant sees every server in
`mcpServers`); the moment the block is present, as above, it becomes the only
source of grant, with no silent allow-all — `mcpproxy-admins: ["*"]` is the
one way to grant every shared server, and a user in neither group (and with no
`default_servers` entry) is entitled to none. See
[Group access map and entitlement](../development/server-edition-multiuser-auth.md#group-access-map-server_editionaccess-and-entitlement-spec-107-pr-c)
for the grant formula, and the Keycloak column of the
[groups-claim table](../configuration/config-file.md#groups-claim-by-identity-provider) —
untick **Full group path** on the client scope's Group Membership mapper, or
the claim carries `/engineering/mcpproxy` instead of the bare name the map
above expects.

**Staleness bound and the upgrade path**, if you add this block to a
deployment that has been running without one:

```
bound = session_ttl + max(bearer_token_ttl, longest owned agent-token expiry ≤ 365 days)
```

groups (and the admin role) refresh only at login, but every already-issued
session, bearer JWT and owned agent token is narrowed to the new grant on its
very next request/authentication — no restart, no waiting for that bound, no
re-login required to *lose* access. The full state table (pre-upgrade user
records, live sessions, admin-exempt accounts, provider/subject rebind on an
IdP migration) is the
[Upgrade-state table](../development/server-edition-multiuser-auth.md#upgrade-state-table).
An administrator `disable` → `enable` is the immediate remedy when you cannot
wait for the bound.

**Open Spec 105 items.** Three surfaces still leak the *existence* (never the
content) of a group-excluded server on `main` today: `retrieve_tools`'s
`usage_summary`/`session_risk` statistics, the "Available servers" error text,
and the scope-denial text. This deployment guide adds nothing new to that
leak and it closes automatically the moment the corresponding Spec 105 items
merge — see
[Still-open Spec 105 items](../features/agent-tokens.md#server-edition-incident-response).

### Secrets via `${env:...}`

`oauth.client_secret` and `credential_encryption_key` are the two secret
fields server_edition ever reads, and neither is ever readable back through
the API — `client_secret` is masked in every response and log, and
`credential_encryption_key` never has a Settings row. Reference them as
`${env:VAR_NAME}` in the config file and supply the real value only as an
environment variable, sourced from a Kubernetes `Secret`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: mcpproxy-secrets
type: Opaque
stringData:
  OIDC_CLIENT_SECRET: "<keycloak client secret>"
  MCPPROXY_CRED_KEY: "<32+ byte random string>"
```

```yaml
        envFrom:
        - secretRef:
            name: mcpproxy-secrets
```

Never put either value directly in the config file or a ConfigMap — the file
is a Kubernetes object anyone with `get configmaps` in the namespace can read;
`${env:}` plus a `Secret` keeps the value out of it and out of `kubectl get
configmap -o yaml`. `MCPPROXY_CRED_KEY` is used only as a fallback when
`credential_encryption_key` is empty; the example config sets both to the
same `${env:MCPPROXY_CRED_KEY}` reference so there is exactly one secret to
rotate. A missing `${env:...}` reference is refused at boot as if the field
were never set (`server_edition.oauth.client_secret is required`), never sent
to the IdP as the literal placeholder text.

## Audit to stdout

```json
{ "audit_log": { "enabled": true, "stdout": true } }
```

Under the HTTP transport (this deployment — the container never runs native
stdio), `stdout` writes one JSON line per authorization decision and tool
call to the container's stdout, which `kubectl logs` and any node-level log
shipper (Fluent Bit, Vector, Promtail) already scrapes with zero extra
config — no volume, no rotation to manage. This is in fact the **default**
the moment `audit_log` is left out entirely on the server edition: the block
above is written for clarity, not because it changes behavior. If you would
rather rotate to a file on the data volume instead, set
`"audit_log": {"path": "/data/logs/audit.jsonl"}` — `stdout` and `path` are
mutually exclusive when both are set explicitly (`path` wins, `stdout` is
dropped with a warning); rotation defaults to 50 MB / 10 backups / 90 days,
gzip'd. Schema, event vocabulary and a vendor-neutral log-shipper recipe:
[Audit Log](../features/audit-log.md).

The one case that does **not** apply to this deployment but is worth knowing:
a native **stdio** transport can never use `stdout` for audit lines (stdout
carries JSON-RPC there) — an absent block resolves to disabled with one WARN,
and an explicit `stdout: true` with no `path` fails boot with exit code 4.
This guide's container always serves HTTP, so that branch never triggers
here; it matters only if you also run `mcpproxy-server` as a local stdio MCP
server for a single developer.

## Kubernetes manifests

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcpproxy
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: mcpproxy
  template:
    metadata:
      labels:
        app: mcpproxy
    spec:
      containers:
      - name: mcpproxy
        image: ghcr.io/smart-mcp-proxy/mcpproxy-server:latest
        args:
        - "serve"
        - "--listen=0.0.0.0:8080"
        - "--config=/data/mcp_config.json"
        - "--data-dir=/data"
        envFrom:
        - secretRef:
            name: mcpproxy-secrets
        ports:
        - containerPort: 8080
        volumeMounts:
        - name: data
          mountPath: /data
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 15
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: mcpproxy-data
```

`/healthz` and `/readyz` are unauthenticated by design (a load balancer never
carries an API key or a session), so no extra header configuration is needed
in either probe. `/readyz` fails while the tool index is still building on
first boot or during a large reindex; `/healthz` is the narrower liveness
signal and should not flap during that window — keep the two probes distinct
rather than pointing both at the same path.

### Service and Ingress

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mcpproxy
spec:
  selector:
    app: mcpproxy
  ports:
  - port: 80
    targetPort: 8080
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcpproxy
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-buffering: "off"  # keeps SSE (/events) streaming
spec:
  ingressClassName: nginx
  tls:
  - hosts: ["mcp.example.com"]
    secretName: mcpproxy-tls
  rules:
  - host: mcp.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: mcpproxy
            port:
              number: 80
```

`nginx.ingress.kubernetes.io/proxy-buffering: "off"` matters specifically
because `GET /events` is a long-lived SSE stream that re-resolves the tenant
session principal before every frame (Spec 107 PR-C) — a buffering proxy
delays or coalesces those frames, which reads to the client as a stalled
connection rather than a slow one. Confirm your ingress controller forwards
`X-Forwarded-Proto` and `X-Forwarded-For` (nginx does by default); those are
exactly the headers `trusted_proxies` above tells `mcpproxy-server` to trust
from the ingress-controller pod range.

## Keycloak client setup

1. Create a confidential OIDC client (e.g. `mcpproxy`) in the `team` realm.
2. Valid redirect URI: `https://mcp.example.com/api/v1/auth/callback` —
   exact match, this is why `public_url` above is set explicitly rather than
   derived from the request.
3. Add a **Group Membership** mapper to the client's dedicated scope (or a
   shared scope included by default): token claim name `groups`, **untick**
   "Full group path" so the claim carries `mcpproxy-engineering` rather than
   `/mcpproxy-engineering`.
4. Create the two groups referenced by the config above
   (`mcpproxy-engineering`, `mcpproxy-admins`) and add team members to them.
5. Copy the client's credential into the `OIDC_CLIENT_SECRET` value of the
   `mcpproxy-secrets` Secret above.

## Verifying the deployment

```bash
kubectl apply -f secret.yaml -f deployment.yaml -f service.yaml -f ingress.yaml
kubectl rollout status deployment/mcpproxy
kubectl logs -f deployment/mcpproxy | grep '"event":"authz"'   # audit lines on stdout
curl -s https://mcp.example.com/healthz
curl -s https://mcp.example.com/api/v1/auth/provider              # {"display_name":"Example Keycloak"}
```

Sign in through `https://mcp.example.com` with a Keycloak account in
`mcpproxy-engineering`; the Web UI should show `github` and `ast-grep` and
nothing else. Add the account to `mcpproxy-admins` instead (or to
`admin_emails` directly) to see every configured server.

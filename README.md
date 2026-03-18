# Flock

Flock is Kestrel's identity lifecycle and SaaS provisioning service, owned by EntSec Identity
Platforms (Perch queue `FLOCK`). It does three things:

1. **Lifecycle.** Reads joiner, mover and leaver events from the People Systems HR feed and turns
   them into actions: create the account, update it, deactivate it.
2. **Identity sync.** Keeps every managed SaaS tenant in line with the HR roster on a fixed interval
   (default 15 minutes), so drift and missed events are corrected.
3. **Single sign-on.** Federates tenants with the Kestrel identity provider
   (`https://sso.kestrel.example`) and, where configured, enforces SSO so people cannot log in with
   a local password.

```
HR feed ──► lifecycle (joiner / mover / leaver) ──► reconciler ──► connectors ──► SaaS tenants
 roster  ─────────────────────────────────────────►      │          salesforce     Salesforce orgs
                                                         │          workspace      Kestrel Workspace
                                    config/tenants.yaml ─┘          slack (stub)
```

## Tenants

Each managed SaaS instance is a **tenant** with one entry in
[`config/tenants.yaml`](config/tenants.yaml). The reconciler only acts on tenants with
`enabled: true`; a disabled tenant is skipped on every pass and logged as `tenant disabled: <name>`.

**Enabling a tenant requires a CHG ticket.** The steps (Perch ticket, connector checklist, CL with
the CHG number, test table update, promotion to `main`) are in [docs/tenants.md](docs/tenants.md).
Connector-specific onboarding is in [docs/salesforce-connector.md](docs/salesforce-connector.md).

Connector defaults (API versions, rate limits, Strongbox references) live in
[`config/connectors.yaml`](config/connectors.yaml). No credential is stored in this repository:
`svc.flock` keys and API tokens are read at runtime from Strongbox.

## Layout

| Path | What |
|---|---|
| `cmd/flockd` | The daemon: loads the config, polls HR events, runs the reconcile loop |
| `internal/config` | Parses `tenants.yaml` and `connectors.yaml` |
| `internal/lifecycle` | HR events → per-tenant actions (`joiner.go`, `mover.go`, `leaver.go`) |
| `internal/hrfeed` | HR feed client (roster and event stream) |
| `internal/reconcile` | The control loop; skips disabled tenants |
| `internal/connectors` | Connector interface (`Provision`, `Deprovision`, `EnforceSSO`, `Reconcile`) and implementations |
| `internal/secrets` | Strongbox reference resolver |
| `deploy/` | Service manifest applied by Flightpath |

## Develop

```sh
go vet ./...
go build ./...
go test ./...
go run ./cmd/flockd -once -tenants /tmp/one-tenant.yaml   # needs a Strongbox render directory
```

Branches follow Flightpath: CLs go to `develop`, promotions go `develop` → `uat` → `main`. Only
`main` is deployed. `main` needs two flock-team approvals and green `ci` and `flightpath/reviewer`
checks.

---

Kestrel Technologies is a fictional company created for an educational workshop. Nothing in this repository is a real credential, person or product. Mirrored from Roost by `roost-mirror`; CL metadata is preserved in commit trailers (`Roost-CL:`, `Reviewed-by:`).

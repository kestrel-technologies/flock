# Flock

Identity lifecycle for Kestrel: reads joiner / mover / leaver events from the HR feed and provisions
and deprovisions accounts in SaaS tenants through per-tenant connectors.

Tenants are listed in `config/tenants.yaml`. Only tenants with `enabled: true` are managed.

```sh
go test ./...
go run ./cmd/flockd -tenants config/tenants.yaml
```

Owner: EntSec Identity Platforms (Perch queue `FLOCK`).

---

Kestrel Technologies is a fictional company created for an educational workshop. Nothing in this repository is a real credential, person or product. Mirrored from Roost by `roost-mirror`; CL metadata is preserved in commit trailers (`Roost-CL:`, `Reviewed-by:`).

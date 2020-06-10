# Deploying flockd

`service.yaml` is the manifest Flightpath applies when a promotion reaches `main` (project
`kcp-entsec-identity-prod`). The tenant list ships inside the image (`/etc/flock/tenants.yaml`), so a
change to `config/tenants.yaml` takes effect only after it is promoted to `main` and deployed; `develop`
and `uat` builds run the test suite but do not touch any tenant.

Secrets are rendered into `/var/run/strongbox` by the Strongbox agent under the `flockd-prod` role.
Nothing in this repository or in the manifest holds a credential.

Rollback: re-deploy the previous image tag (the previous `VERSION`) from the Flightpath release page.

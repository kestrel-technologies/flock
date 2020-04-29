# Tenants

A tenant is one SaaS instance that Flock manages: a Salesforce org, the Workspace directory, and so
on. Every tenant has exactly one entry in [`config/tenants.yaml`](../config/tenants.yaml), and
flockd only acts on tenants whose entry says `enabled: true`.

## Fields

| Field | Required | Meaning |
|---|---|---|
| `connector` | always | `salesforce`, `workspace` or `slack` (stub) |
| `enabled` | always | `true`: flockd provisions, deprovisions and reconciles this tenant. `false`: flockd skips it |
| `login_url` | Salesforce, when enabled | The org's My Domain URL; API calls and the token exchange go here |
| `service_user` | Salesforce, when enabled | The `svc.flock` user in the org; provisioned usernames use its domain |
| `enforce_sso_profiles` | no | `all`: every internal-user profile gets "Is Single Sign-On Enabled" on each pass (see [salesforce-connector.md](salesforce-connector.md)). `none` or absent: profiles are left alone |
| `sso_exempt_profiles` | no | Profiles skipped by SSO enforcement (the break-glass profile named in the CHG) |
| `note` | no | Free text shown in the reconciler log while the tenant is disabled |

A disabled tenant may be parked as a one-line flow mapping; only `connector` and `enabled` are
checked while it is off:

```yaml
  example-prod: { connector: salesforce, enabled: false, note: "reason and CHG number" }
```

## Adding a tenant

1. Open a Perch ticket in the `FLOCK` queue with the tenant name, its owner team and the connector.
2. The tenant's admins complete the connector checklist (for Salesforce:
   [salesforce-connector.md](salesforce-connector.md#onboarding-checklist)). Flock does not create
   its own service user or install SAML settings in someone else's tenant.
3. Add the entry with `enabled: false` in a CL that references the ticket. CI parses the file.

## Enabling or disabling a tenant

Changing `enabled` on a production tenant is a change to who can sign in to that system and needs a
**CHG ticket** in Perch, approved by the tenant's owner and by Identity Platforms.

1. Put the CHG number in the CL title, e.g. `chore(tenants): enable example-prod [CHG-1234]`.
2. When disabling for a planned window, add a `note` with the reason and the CHG so the next person
   knows what to undo.
3. The change takes effect when the CL is promoted to `main` and deployed (see
   [`deploy/README.md`](../deploy/README.md)). Merging to `develop` or `uat` changes nothing in any
   tenant.

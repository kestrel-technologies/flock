# Salesforce connector

The Salesforce connector (`internal/connectors/salesforce`) keeps a Salesforce org in line with the
HR roster and makes the Kestrel identity provider the only way people sign in.

- **Users:** created, updated and deactivated through the org's SCIM 2.0 endpoint
  (`/services/scim/v2/Users`). Salesforce users cannot be deleted; leavers are deactivated.
- **Single sign-on:** with `enforce_sso_profiles: all`, every pass turns on the profile permission
  **"Is Single Sign-On Enabled"** (`PermissionsIsSsoEnabled`) on every internal-user profile, except
  API-only profiles and the profiles in `sso_exempt_profiles`. Users on those profiles must log in
  through SAML; their Salesforce password no longer works.
- **Authentication:** OAuth 2.0 JWT bearer as the tenant's `svc.flock` user. Flock holds no password
  for any org.

## What Flock touches and what it does not

| Flock manages | Flock never touches |
|---|---|
| Users whose federation id (SCIM `externalId`) Flock set: people from the HR roster | Users without a federation id set by Flock: service users, the break-glass account, accounts created by hand |
| "Is Single Sign-On Enabled" on internal-user profiles | Org-wide settings such as "Disable login with Salesforce credentials", login IP ranges, session settings: these belong to the tenant's admins |
| The profile assigned at provisioning (`default_profile` in `config/connectors.yaml`) | Permission sets, roles, sharing |

## Onboarding checklist

Done by the tenant's Salesforce admins, with the Flock team on the ticket. Flock cannot bootstrap
itself into an org.

1. **SAML single sign-on settings.** Setup → Single Sign-On Settings → New from Metadata URL:
   `https://sso.kestrel.example/saml/metadata/<tenant>`. This installs the Flock-issued metadata:
   issuer `https://sso.kestrel.example`, SAML 2.0, identity in the **Federation ID** field,
   login URL `https://sso.kestrel.example/saml/login`. Enable SAML in Setup → Single Sign-On
   Settings. Configuring SAML does not by itself force anyone to use it; enforcement is the profile
   permission above, applied by Flock once the tenant is enabled.
2. **Profile "Flock Provisioning".** Clone the Salesforce Platform profile; grant API Enabled,
   API Only User and Manage Users; nothing else.
3. **Service user.** Onboarding creates the `svc.flock` service user: username
   `svc.flock@<tenant-domain>` (the value of `service_user` in `config/tenants.yaml`), Federation ID
   `svc.flock@kestrel.example`, profile Flock Provisioning, email to the Flock team list.
4. **Connected app "Flock Provisioning".** OAuth enabled, "Use digital signatures" with the
   certificate the Flock team attaches to the ticket, scope `api`, permitted
   users "Admin approved users are pre-authorized", profile Flock Provisioning.
5. **Strongbox.** The Flock team stores the connected app's consumer key at
   `strongbox://entsec-identity/flock/salesforce/<tenant>/client-id`. The signing key is generated
   in Strongbox at `.../<tenant>/jwt-key` and never leaves it. Nothing goes into this repository.
6. **Tenant entry.** Add the tenant to `config/tenants.yaml` with `enabled: false`, then follow
   [tenants.md](tenants.md#enabling-or-disabling-a-tenant) to enable it under a CHG ticket.

## Checking a tenant before and after enabling it

Run one reconcile pass from a workstation against a sandbox tenant (e.g. `vantage-uat`) with a
tenants file that lists only that tenant, enabled, and a Strongbox render directory you are allowed
to read:

```sh
STRONGBOX_ROOT=$HOME/.strongbox/render go run ./cmd/flockd -once -tenants /tmp/one-tenant.yaml
```

The JSON log shows `sso enforced on profile` for every profile it changed and one `reconciled` line
with counts. Then confirm in the org itself, for example with the Salesforce CLI:

```sh
sf data query --target-org <alias> \
  -q "SELECT Name, PermissionsIsSsoEnabled FROM Profile WHERE UserType = 'Standard' ORDER BY Name"
sf data query --target-org <alias> \
  -q "SELECT Username, IsActive FROM User WHERE Username LIKE 'svc.flock@%'"
```

A tenant that is disabled in `config/tenants.yaml` on `main` is not reconciled in production, whatever
the other branches say.

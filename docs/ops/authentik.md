# Authentik setup (AUTH-007)

Manual test status: **NOT RUN** against a real Authentik instance. Automated tests use `mockoidc` only. Record the date and result here once run.

1. Create an OAuth2/OpenID provider: client type confidential, redirect URI `<C8_PUBLIC_URL>/auth/callback`, scopes `openid profile email groups`.
2. Add a scope mapping named `groups` returning `{"groups": [g.name for g in request.user.ak_groups.all()]}` and attach it to the provider.
3. Create an application using the provider; its issuer is `https://<authentik>/application/o/<slug>/` (`C8_OIDC_ISSUER`).
4. Put the client id in `C8_OIDC_CLIENT_ID` (ConfigMap) and the client secret in `C8_OIDC_CLIENT_SECRET` (Secret).
5. Create a product approver group (e.g. `shop-approvers`) and set it as the namespace `approver_group` (global_admin only).
6. Map groups to roles with role bindings, or set `C8_BOOTSTRAP_ADMIN_GROUP` for the first global_admin group.

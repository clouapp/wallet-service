# Tenant account provisioning

`POST /internal/v1/tenants` creates the Macro Wallets account and account API
token for one platform tenant. The caller sends JSON with `external_id`, `name`,
and `environment` (`test` or `prod`) and authenticates with
`X-Tenant-Provisioning-Key`. Set `TENANT_PROVISIONING_KEY` to a random secret of at
least 32 characters, independent of `APP_KEY` and `JWT_SECRET`.

The same `external_id` returns the same account and an API token for the same
token row. This lets a provisioning job resume after an HTTP success if its
local settings write failed. The response has `Cache-Control: no-store`; the
plaintext token is not stored in Macro Wallets. Different tenants receive
different accounts and API tokens. A retry with a different environment is
rejected. Do not log the request header or API token response.

Rotating `TENANT_PROVISIONING_KEY` requires coordinated token reprovisioning:
the service will refuse to reissue an existing token when its derived hash no
longer matches the stored row. Existing issued tokens remain valid until
revoked separately.

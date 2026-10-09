> **Historical.** A dated record of the choices taken while the alignment work was open. It is not
> maintained: the current invariants are in `CLAUDE.md` and the shape of the code is in
> `.ai/guidelines/`; where this file and those disagree, they win.

# Alignment decisions for review

Record of choices taken where the alignment prompt left the decision open.
Checked against `origin/feat/api-token-scope` at `1b030d8aa83a9c81cb0506a13d8f3296a18cc1f0`.
This file does not mark the alignment goal complete.

Plan: `macro-wallets-alignment-prompt.md`. Nineteen decisions.

## 1. Error envelope on both surfaces

**Decision.** Every failure on the dashboard (`/v1`) and the external API (`/api/v1`) uses `{"error":{"code","message"}}`. Extra fields stay inside `error`. The Markets client reads both this object and the older string. That dual-read is [clouapp/back#418](https://github.com/clouapp/back/pull/418), not this repository.

**Plan.** Parte 1, §2 item 1 (B2.1): slotkit envelope `{"error":{"code","message"}}` on both surfaces, rather than a frozen string on `/api/v1`.

**Evidence.** `1d6dee901cc1233d5816e70d3587c3ccb5dc9363` (`feat: return one error envelope on every failure`). `.ai/guidelines/http-error-contract.md`. `app/http/responses/render.go`.

## 2. Validation stays HTTP 422 with an errors map

**Decision.** A form-request failure stays HTTP 422. The body is the envelope plus `"errors"` as a per-field list of messages. The dashboard still also reads Goravel's raw 422 map `{field:{rule:message}}` when the body has no `error` and no `message`.

**Plan.** Parte 1, §2 item 2 (B2.2): keep 422 with the field map. Parte 2, F1 item 3 and F5: the HTTP layer accepts both 422 shapes while B2.2 is open.

**Evidence.** `app/http/responses/render.go` (`ValidationFailed`, `FieldsFailed`). Front: [macro-wallets-front#20](https://github.com/clouapp/macro-wallets-front/pull/20) `lib/api/errors.ts` (`readFieldErrors`), commit `c7bf06ece49f4f2367554f0cce67474249de5f26`.

## 3. B2.3 contract snapshot

**Decision.** Success bodies stay byte-for-byte on `ctx.Response().Json`. The recorded contract is the HTTP snapshot (method, path, status, body), rewritten only with `go test ./tests/contract -update-contract`.

**Plan.** Parte 1, §2 item 3 (B2.3): success JSON stays equal unless a change is decided, proved by the diff in §6. Fase 0 item 6: record the current contract.

**Evidence.** `f7dfef48bd14b0a78641dc39da35eeff8cd713b3` (`test: record the HTTP contract snapshot`). `tests/contract/snapshot.go`. `tests/contract/testdata/http_contract.txt`.

## 4. B2.4 dashboard and external controllers

**Decision.** Handlers are split into `app/http/controllers/dashboard/<feature>/` (session, `/v1`) and `app/http/controllers/external/<feature>/` (API token, `/api/v1`). A handler is not shared between the two surfaces.

**Plan.** Parte 1, §2 item 4 (B2.4): `controllers/{dashboard,external}/<feature>/`, with the shared handlers split into two thin handlers on the same service.

**Evidence.** `3df449dc0c065873ff8a8bcbfe5e7b685544c068` (`refactor: split wallet controllers by HTTP surface`). `.ai/guidelines/http-layer.md`.

## 5. B2.5 credential mail job, Queue refused

**Decision.** Reset, invite, and add-user mail go through `SendCredentialMailJob`. The token is minted when the job runs. `facades.Mail().Queue()` returns `mail: queue is refused`.

**Plan.** Parte 1, §2 item 5 (B2.5): slotkit `Send()` inside a job that carries no credential, and `Queue()` refuses, because reset and invite mail carry a credential.

**Evidence.** `d7a59d886f3132c25bfc82a04332ff7416ab542b` (`fix(mail): refuse facades.Mail().Queue()`). `148db56fbc38b50c0fc349db31afbbdc40b1ff59` (`fix(mail): mint reset and invite tokens inside a job`). `1b030d8aa83a9c81cb0506a13d8f3296a18cc1f0` (`fix(mail): dispatch the add-user invite through the credential job`). `app/providers/mail/facade.go`. `app/jobs/send_credential_mail.go`.

## 6. B2.6 concrete repositories

**Decision.** A repository is a concrete struct with `db.Base` and no interface of its own. `NewX` returns the struct. Services declare the ports they need.

**Plan.** Parte 1, §2 item 6 (B2.6): slotkit concrete repositories, never mocked; services test against ports.

**Evidence.** `802c4078e957083079a0440f7d0604b7927cb481` (`refactor: persist wallets and addresses through concrete repositories`). `app/repositories/wallet_repository.go`. `.ai/guidelines/README.md` (deliberate choices).

## 7. Account roles

**Decision.** The stored account roles are `owner`, `admin`, `auditor`, and `user`. Rank is owner > admin > user = auditor. A stored `viewer` is rewritten to `auditor`. The check constraint is `role IN ('owner','admin','auditor','user')`.

**Plan.** S7: one vocabulary, a CHECK, and a migration `viewer → auditor`. §9.3: rank `owner > admin > user = auditor`.

**Evidence.** `9e22bf617460803c093676c919e5ad1490886adb` (`fix(security): use one account role vocabulary`). `app/models/account_role.go`. `M00000000000470AccountUsersRoleCheck` in `database/migrations/00000000000420_create_activity_log_table.go`.

## 8. Wallet roles

**Decision.** Wallet roles are the set `admin`, `spender`, `approver`, and `viewer`.

**Plan.** §9.3: wallet roles are a validated set `{admin, spender, approver, viewer}`.

**Evidence.** `2a50a4d0129a670442f2f94d3cf01b31ee050e49` (`fix(wallets): store only the wallet role set`). `app/models/wallet_role.go`.

## 9. platform_admins, no grant route

**Decision.** `platform_admins` exists. The row is `user_id` (primary key, foreign key to `users`). It stores no password and no role. Provisioning stays outside this service. There is no grant route. `PlatformAdminRepository` only answers `Contains`.

**Plan.** §0.5 and §9.3: create `platform_admins` with its own guard under `/v1/platform`. Appendix B item 350 and the S3.4 table sketch describe a fuller staff row (name, email, password). This branch stored the membership only.

**Evidence.** `bb6cb89920eeae7bbbe05a2a1b67e5602139677a` (`feat: let a platform admin pause withdrawals and sweep globally`). `database/migrations/00000000000310_create_platform_admins_table.go`. `app/repositories/platform_admin_repository.go`.

## 10. Removing a member revokes minted API tokens

**Decision.** `RemoveMember` revokes the API tokens that member minted for the account (`DeleteByAccountAndCreator`). `UpdateMember` leaves those tokens in place when the member is suspended.

**Plan.** §0.5: whether removing a member revokes the API tokens they created.

**Evidence.** `1de9bb5918ee6cd976e7cab984c3182bae8af27d` (`feat: let owners and admins change a member's role and status`) for the remove path. `6c4eff1bae9ba12bae58ec1e1230a7c7c4485a7a` (`fix(accounts): keep API tokens when a member is suspended`). `app/services/account/service.go`.

## 11. Who may move funds

**Decision.** Owner and admin may withdraw, sweep, and create wallets. Owner, admin, and user may generate addresses. Auditor is read-only. The retired label `viewer` is treated as auditor. An unknown role is refused. Fund routes stay on the account-role fund guard, and the union is not consulted there.

**Plan.** §0.5 and S3: the permission on each fund-moving handler is a product decision. §9.3: auditor only reads; user operates and does not administer.

**Evidence.** `eeac3fdbedd8e8027f6f70535c9c8b1ec09daead` (`fix(security): require a role before moving funds`). `app/policies/fund_movement.go`.

## 12. Feature-flag gates and catalog defaults

**Decision.** Four flags have no runtime gate: `deposit-scan-enabled`, `wallet-creation-enabled`, `webhook-delivery-enabled`, and `api-request-signature-required`. They are stored and listed. Catalog defaults: those three plus `withdrawals-enabled` and `sweep-enabled` are true; `api-request-signature-required` and `user-2fa-required` are false. `withdrawals-enabled` and `sweep-enabled` are gated in the withdrawal and sweep services. `user-2fa-required` is enforced on account routes together with `account_security.require_2fa`. Every catalog flag is scoped `global` and `account`.

**Plan.** §9.2 and Appendix C S2: the seven flags, the defaults in the catalog sketch, and enforcement points for scan, wallet creation, webhook delivery, and request signature as well as withdrawals, sweep, and 2FA.

**Evidence.** `app/services/features/catalog.go` (introduced in `863730dcaed3dbe278976de9ba1a85d7bffe6d73`). Defaults: `db1bb85445b05fe28d1fd1244712c2e2de1d5d1f`, `6b8ee0170613f61b5378f79594d2edb5a62cb209`. Gates: `34111353a63d25f36e37a3f85b106896ccf29b18` (`app/providers/vault_container.go`) and `9b1bd878a095f016056e0af9b3d0bc956d6ac3e0` (`app/http/middleware/totp_enrollment.go`).

## 13. session_idle_minutes and account_webhooks stay stored

**Decision.** `session_idle_minutes` is stored on `account_security` and validated there. Session auth does not read it. `account_webhooks` is stored and returned by the settings API. Webhook delivery reads the platform `webhook_delivery` group, not `account_webhooks`.

**Plan.** S1.4.4: `account_security` holds `require_2fa` and `session_idle_minutes`; `account_webhooks` holds `default_events` and the signing fields. The line says to add them as product asks.

**Evidence.** `app/services/settings/groups_account.go`. `app/services/settings/require_2fa.go`. `app/providers/vault_container.go` (`EffectiveWebhookDelivery`).

## 14. GET /api/v1/limits was not added

**Decision.** There is no `GET /api/v1/limits`. Sweep limits stay on the dashboard and platform settings routes.

**Plan.** S1.4.6: optional read-only `GET /api/v1/limits` for integrators. External `/api/v1` has no settings endpoints.

**Evidence.** `routes/admin.go` registers the dashboard and platform settings paths. No route file registers `/api/v1/limits`.

## 15. Dashboard URLs stay under /dashboard

**Decision.** The front keeps its pages under `/dashboard/*`. `/dashboard` redirects to `/dashboard/assets`.

**Plan.** §0.5 and Parte 2, F2 item 3: whether routes leave `/dashboard/*` is a product decision. F2 marks leaving that prefix as optional.

**Evidence.** [macro-wallets-front#20](https://github.com/clouapp/macro-wallets-front/pull/20), branch `feat/account-activity`, `app/(dashboard)/dashboard/page.tsx`.

## 16. RBAC pivot tables were not created

**Decision.** `model_has_roles`, `role_has_permissions`, and `model_has_permissions` have no migration. Account and wallet roles stay on `account_users.role` and `wallet_users.roles`. The architecture test only names the pivot tables so a later read outside `app/policies` would fail. S3.4.2's seed into `permissions`, `roles`, and `role_has_permissions` with `guard='account'` stays code-only: those tables are not created and the account role catalog is not seeded.

**Plan.** §9.3 and Appendix B item 360: Spatie-style `roles`, `permissions`, `role_has_permissions`, and `model_has_roles`.

**Evidence.** `tests/architecture/rbac_guard_test.go`. No migration creates those tables.

## 17. Archive denial for an account user

**Decision.** `TestArchiveWallet_ForbiddenForAccountUser` expects HTTP 403 (`AssertForbidden`) and the message `only wallet/account owners and admins may archive wallets`. The caller is an account role `user` on the same account as the wallet, with `view_all_wallets` false and no wallet membership. `WalletContext` answers that archive request with HTTP 404 `wallet not found` and stops before `ArchiveWallet`. The test expects 403, the handler returns 404, and that mismatch was left in place.

**Plan.** Appendix A, authorization: resolve the resource, then answer 404 when it is not the caller's, then authorize. This test is the same-account denial. The test source still asserts 403; the live 404 was left in place.

**Evidence.** `0a496d6391b2f490b966d39b6e3f6726fccf7242` added the assertion. `db6c491854db1424072c020a8e75964d702c4a8c` set the message comparison. `app/http/controllers/contract_gaps_test.go`. `app/http/middleware/wallet_context.go` (`WalletContext`). `app/http/controllers/dashboard/wallets/settings_controller.go` (`ArchiveWallet`).

## 18. Appendix B item 300 is migration 590

**Decision.** "300 seed platform settings from env" is `00000000000590_seed_platform_settings_from_env`. `00000000000300` was already the features table. The Appendix B one-liner does not name environment variables, so the migration uses the S1.4.3 field map in `PlatformSettingsSeed` (mail, price, and provider groups; blanks skipped; secrets sealed). Inserts use `ON CONFLICT ("group", "key") WHERE account_id IS NULL DO NOTHING`. `Down()` is a no-op so a later operator edit is kept.

**Plan.** Appendix B: `300 seed platform settings from env`. S1.4.3: `PlatformSettingsSeed` per group, values sealed, blanks skipped, `ON CONFLICT DO NOTHING`.

**Evidence.** `40f518f3ece7da0ae82cfb83e8612b471ff8c640` (`feat(settings): seed platform mail, price, and provider values from env`). `database/migrations/00000000000590_seed_platform_settings_from_env.go`. `app/services/settings/platform_seed.go`.

## 19. One pull request per repository

**Decision.** The alignment work for the wallet service is one pull request, [wallet-service#95](https://github.com/clouapp/wallet-service/pull/95) (`feat/api-token-scope`). The front work is one pull request, [macro-wallets-front#20](https://github.com/clouapp/macro-wallets-front/pull/20) (`feat/account-activity`). Groups share those two pull requests.

**Plan.** §0.2: one group becomes one pull request, and groups from different areas stay apart.

**Evidence.** The two open pull requests above. This branch carries the groups listed in `git log` through `1b030d8aa83a9c81cb0506a13d8f3296a18cc1f0`.

The settings cache key includes the scope (`settings:platform:<group>` and `settings:account:<uuid>:<group>`) so a platform document and an account document that share a group name stay apart.

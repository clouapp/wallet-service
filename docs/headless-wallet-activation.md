# Headless API wallets and pending test-wallet repair

External `POST /api/v1/wallets` now provisions an active headless wallet. The existing authenticated account scope and `wallets.create` permission still apply. The account environment must match an active chain in the current catalogue before key generation; missing environment, catalogue failure or a network mismatch refuses creation. There is no body option that changes the activation mode and no public external activation endpoint.

The wallet, generated base address and address link commit in one database transaction. A failure rolls those rows back, so no active wallet is left without its generated address. The external result contains no activation code and does not add any customer share, passphrase or service share to the existing response. Creating an API wallet does not sign, broadcast, fund or alter existing wallets.

Dashboard creation remains pending and returns its one-time code for the existing manual KeyCard acknowledgement. Dashboard activation still validates that code with the domain service. Activation now takes a wallet row lock in its transaction, and the repository updates only a pending row with an activation code, requiring one affected row. A concurrent freeze or an already-active row cannot be overwritten.

## Existing pending test wallets

Existing wallets are not silently reclassified by a sync or deployment. A privileged operator can inspect **one exact wallet** through the new command:

```sh
go run . artisan wallets:activate-test \
  --account=<exact-account-uuid> \
  --wallet=<exact-wallet-uuid> \
  --expect-chain=arbitrum
```

Default execution is a dry run. To apply that selected repair, repeat the exact selection with `--apply`. The command refuses production accounts, mainnet or inactive chains, inactive accounts, foreign wallets, different chains, nonpending wallets, any freeze metadata, and missing or malformed server-held codes. There is no production override, bulk option, credential argument or guessed-code retry.

The service locks the selected chain, account and wallet in a fixed order. Inside that transaction it passes the legitimately stored code directly to the existing `ActivateWallet` domain method. The credential remains in server process memory; it is never printed, returned, cached, logged, placed in an argument, or sent through a new HTTP route. The command prints only account/wallet identifiers, chain, status and whether it applied the metadata change. It does not touch a key, address, balance, transfer or funding journal.

If apply fails or its acknowledgement is lost, inspect the authenticated wallet inventory before retrying. An active wallet needs no repair; a repeated apply is refused. A pending wallet without its legitimate code requires separate operator attention; do not replace the wallet, rotate its keys, fabricate a code or set its SQL status. The command does not reopen a frozen or archived wallet.

## Validation

Service units cover the two creation modes, transactional rollback at address/link failures, exact scoped repair, default dry run, secret-free reports/errors and simultaneous apply attempts with one winner. Command tests cover explicit apply and failure exits. Repository tests verify the pending-only update and refusal of frozen, archived, active and code-less rows. External HTTP feature tests verify active creation without activation credentials or share exposure, unchanged dashboard creation, environment refusal before key generation, and rollback when base-address persistence fails.

Integration tests use the existing `tests/feature/support/testdb` template/worker clone guards and an isolated `vault_unit_test_activation_*` database with non-live Redis indexes. They must never migrate or truncate `vault` or `vault_test`. No test of this change requires an on-chain transaction or production activation.

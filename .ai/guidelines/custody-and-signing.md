# Custody and signing

> Status: HOLDS. Key material stays in `app/services/mpc` and the custody
> services, and nothing of it is on the wire by construction: models carry no wire
> tags (`TestModels_Carry_NoWireTags`) and no resource has a key column
> (`app/http/resources/key_columns_test.go`). Logging is covered by
> `errors-and-logging.md`.

## The model

A wallet is a **2-of-2 MPC** co-signing wallet:

- **share A** belongs to the customer, encrypted with the customer's
  **passphrase** (Argon2id → AES-GCM; `mpc.EncryptShare` / `DecryptShare`) and
  stored on the wallet row (`MPCCustomerShare`, `MPCShareIV`, `MPCShareSalt`);
- **share B** belongs to the service and lives in **AWS Secrets Manager**
  (`MPCSecretARN` on the wallet row; the value never in the database);
- neither party can sign alone. A signature needs the passphrase (or the
  customer-held material) AND share B, combined in process for one signing
  operation.

Curves: secp256k1 (EVM, BTC) and ed25519 (SOL). HD derivation (`hdkey`,
`MPCChainCode`, `AddressIndex`) produces deposit addresses.

## Rules

- **Key material lives only in `app/services/mpc`** (keygen, keystore, signing)
  **and is reached only through the custody services** (wallets, withdrawals,
  sweep). No controller, job, command or adapter handles a share, a passphrase
  or a derived key.
- Secrets Manager is a **port** (`SecretStore`) implemented in
  `app/adapters/secretsmanager`; services never import the AWS SDK.
- **Nothing of it is ever returned, logged, queued or cached.** Not in a
  resource, not in an error wrap, not in a job payload, not in Redis, not in a
  webhook. A model carries no wire tag, so a key column cannot leak by a missing
  `json:"-"`; the resource simply does not have the field.
- A passphrase arrives in exactly one form request field, crosses to the service
  as an input struct field, and is discarded after the operation. It is never stored, compared
  as plain text, or logged.
- **Signing is one entry point per curve**, inside the custody service, taking
  an unsigned transaction built by the chain adapter and returning a signed one.
  The adapter builds and broadcasts; it never sees key material.
- **Do not reorder signing operations "in passing".** Build → policy checks
  (limits, whitelist, approvals, environment) → fetch share B → combine → sign →
  broadcast → persist state. A refactor that moves a step needs the testnet e2e
  (see `testing.md`).
- A recovery-material download (if exposed) is its own route with its own
  permission and re-authentication.
- Zero buffers holding share or key bytes after use where the library allows.
- `facades.Crypt()` (APP_KEY) protects TOTP secrets, RPC URLs and webhook
  secrets at rest — **it is not used for MPC shares**, which have their own
  envelope.

## Tests

- Unit tests for keygen/sign/round-trip per curve, with mocked `SecretStore`.
- A test that no resource type has a field named like a key column.
- The testnet e2e (`scripts/e2e`, `tools/e2e-funder`) for every change that
  touches signing, withdrawal or sweep.

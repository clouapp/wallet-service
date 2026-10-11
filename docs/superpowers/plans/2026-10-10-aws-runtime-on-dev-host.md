# AWS runtime on the dev host — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** the dev host (Forge, no Lambda) runs on the real AWS resources of stack `waas-dev`:
- MPC share B lives in AWS Secrets Manager, sealed with the master KMS key;
- outbound webhooks travel through the SQS webhook queue.

**Architecture:**
1. The app builds its AWS configuration from its own config (`vault.aws.profile`, `vault.aws.region`, `vault.aws.endpoint_url`), not from the process environment, which a supervisor process does not inherit.
2. `MASTER_KEY_REF` selects the KMS key that seals every new share. `secrets:migrate-shares` copies the existing shares from LocalStack and rewrites `wallets.mpc_secret_arn`, verifying the bytes.
3. A new process mode, `LAMBDA_MODE=webhook_poller`, long-polls the webhook queue and hands each batch to the same `handleWebhookWorker` the Lambda runs. On the dev host it replaces the Lambda; it never runs beside it, so "one consumer per queue" still holds.

**Tech Stack:** Go 1.25, Goravel v1.17, aws-sdk-go-v2 (config v1.27.0, sqs v1.31.0, secretsmanager v1.41.4), aws-lambda-go v1.47.0 events, testify.

**Context:** see `docs/AWS_DEV_STACK.md`. Stack `waas-dev` is deployed in account 430776404482. The host has the AWS profile `waas-dev` (IAM user `waas-dev-server`). The host app still uses LocalStack (`default` profile, `AWS_ENDPOINT_URL=http://127.0.0.1:4566`).

---

## File structure

| File | Responsibility |
|---|---|
| `config/vault.go` | add `vault.aws.profile` (`AWS_PROFILE`) |
| `app/providers/aws_config.go` (new) | `awsLoadOptions(region, profile)`: the load options from app config |
| `app/providers/app_service_provider.go` | load the AWS config with those options |
| `app/adapters/queue/sqs/client.go` | `NewClient(cfg, endpoint)`: the SQS client honours `vault.aws.endpoint_url` |
| `app/providers/webhook_service_provider.go` | build the SQS client through `NewClient` |
| `app/adapters/secretsmanager/kms_key.go` (new) | `KMSKeyID(masterKeyRef)`: the KMS key a reference names, or "" |
| `app/adapters/secretsmanager/wallet_store.go` | seal created shares with that key; `Lookup(name)` |
| `app/services/wallet/service.go` | export `ShareBSecretName(walletID)` |
| `app/providers/wallet_service_provider.go` | pass the key to the wallet store |
| `app/repositories/wallet_repository.go` | `SetMPCSecretARN` |
| `app/services/sharemigration/service.go` (new) | copy share B source → target, verify, rewrite the ARN |
| `app/console/commands/secrets_migrate_shares.go` (new) | `secrets:migrate-shares {--from-endpoint=} {--apply}` |
| `bootstrap/commands.go` | register the command |
| `app/adapters/queue/sqs/poller.go` (new) | long-poll a queue, hand batches to a handler, delete what succeeded |
| `main.go` | `webhook_poller` mode |
| `.ai/guidelines/queues-and-workers.md`, `docs/AWS_DEV_STACK.md` | the dev host's consumer and the cutover |

---

### Task 1: AWS config from app config (profile, region)

**Files:**
- Modify: `config/vault.go` (the `"aws"` map)
- Create: `app/providers/aws_config.go`
- Test: `app/providers/aws_config_test.go`
- Modify: `app/providers/app_service_provider.go`

- [ ] **Step 1: Write the failing test** — `app/providers/aws_config_test.go`

```go
package providers

import (
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadOptionsFor(t *testing.T, region, profile string) awsconfig.LoadOptions {
	t.Helper()
	var options awsconfig.LoadOptions
	for _, apply := range awsLoadOptions(region, profile) {
		require.NoError(t, apply(&options))
	}
	return options
}

func TestAWS_LoadOptions_UseTheConfiguredRegionAndProfile(t *testing.T) {
	options := loadOptionsFor(t, "us-east-1", "waas-dev")
	assert.Equal(t, "us-east-1", options.Region)
	assert.Equal(t, "waas-dev", options.SharedConfigProfile)
}

func TestAWS_LoadOptions_LeaveTheProfileToTheSDKWhenEmpty(t *testing.T) {
	options := loadOptionsFor(t, "us-east-1", "")
	assert.Equal(t, "us-east-1", options.Region)
	assert.Empty(t, options.SharedConfigProfile)
}
```

- [ ] **Step 2: Run it, expect FAIL** (`undefined: awsLoadOptions`): `go test ./app/providers/ -run TestAWS_LoadOptions -count=1`

- [ ] **Step 3: Implement** — `app/providers/aws_config.go`

```go
package providers

import awsconfig "github.com/aws/aws-sdk-go-v2/config"

// awsLoadOptions turns vault.aws.region and vault.aws.profile into SDK load options.
// A process under supervisor does not inherit .env, so AWS_REGION/AWS_PROFILE there
// only reach the SDK through the app config. An empty profile keeps the SDK's own
// resolution (environment, then the default profile).
func awsLoadOptions(region, profile string) []func(*awsconfig.LoadOptions) error {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(profile))
	}
	return options
}
```

`config/vault.go`, in `"aws"`:

```go
		"aws": map[string]any{
			"region":       envString("AWS_REGION", "us-east-1"),
			"endpoint_url": envString("AWS_ENDPOINT_URL", ""),
			// Named profile in ~/.aws the SDK uses; empty keeps its default resolution.
			"profile": envString("AWS_PROFILE", ""),
		},
```

`app/providers/app_service_provider.go`:

```go
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsLoadOptions(
			facades.Config().GetString("vault.aws.region"),
			facades.Config().GetString("vault.aws.profile"),
		)...)
```

- [ ] **Step 4: Run, expect PASS**: `go test ./app/providers/ -run TestAWS_LoadOptions -count=1` and `go build ./...`
- [ ] **Step 5: Commit** `feat(aws): region and profile come from the app config`

### Task 2: SQS client honours vault.aws.endpoint_url

**Files:** modify `app/adapters/queue/sqs/client.go` and `app/providers/webhook_service_provider.go`. Test: `app/adapters/queue/sqs/client_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestSQS_NewClient_PointsAtTheConfiguredEndpoint(t *testing.T) {
	client := NewClient(aws.Config{Region: "us-east-1"}, "http://127.0.0.1:4566")
	require.NotNil(t, client.Options().BaseEndpoint)
	assert.Equal(t, "http://127.0.0.1:4566", *client.Options().BaseEndpoint)
}

func TestSQS_NewClient_KeepsTheDefaultEndpointWhenEmpty(t *testing.T) {
	assert.Nil(t, NewClient(aws.Config{Region: "us-east-1"}, "").Options().BaseEndpoint)
}
```

- [ ] **Step 2: Run, expect FAIL**: `go test ./app/adapters/queue/sqs/ -run TestSQS_NewClient -count=1`
- [ ] **Step 3: Implement**

```go
// NewClient builds the SQS client. An empty endpoint keeps the SDK's resolver;
// otherwise every call goes there (LocalStack), as the Secrets Manager client does.
func NewClient(cfg aws.Config, endpoint string) *awssqs.Client {
	return awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
}
```

In `webhook_service_provider.go`: `SQS: queuesqs.New(queuesqs.NewClient(*awsCfg, facades.Config().GetString("vault.aws.endpoint_url")), …)`. Drop the then-unused `sqs` import.

- [ ] **Step 4: PASS**, then `go build ./...`
- [ ] **Step 5: Commit** `feat(aws): the SQS client follows vault.aws.endpoint_url`

### Task 3: New shares are sealed with the master key

**Files:**
- Create: `app/adapters/secretsmanager/kms_key.go`, `kms_key_test.go`
- Modify: `wallet_store.go` (+ `wallet_store_test.go`)
- Modify: `app/services/wallet/service.go`
- Modify: `app/providers/wallet_service_provider.go`

- [ ] **Step 1: Failing tests**

```go
func TestKMS_KeyID_NamesOnlyAKMSReference(t *testing.T) {
	cases := map[string]string{
		"arn:aws:kms:us-east-1:430776404482:key/08bad03a-3e1f-4c9b-99f1-68f58f68d0db": "arn:aws:kms:us-east-1:430776404482:key/08bad03a-3e1f-4c9b-99f1-68f58f68d0db",
		"alias/waas-dev-master":                "alias/waas-dev-master",
		"08bad03a-3e1f-4c9b-99f1-68f58f68d0db": "08bad03a-3e1f-4c9b-99f1-68f58f68d0db",
		"local:dev-master-key":                 "",
		"":                                     "",
		"  ":                                   "",
	}
	for ref, want := range cases {
		assert.Equal(t, want, KMSKeyID(ref), ref)
	}
}
```

Wallet store, with the existing fake API in `wallet_store_test.go`:
- `TestWallet_Store_Create_SealsWithTheMasterKey`: `KmsKeyId == "alias/waas-dev-master"`.
- `TestWallet_Store_Create_KeepsTheDefaultKeyWithoutOne`: `KmsKeyId == nil`.
- `TestWallet_Store_Lookup_ReturnsTheARNOfAName`: DescribeSecret returns the ARN; found.
- `TestWallet_Store_Lookup_ReportsAMissingSecret`: `*smtypes.ResourceNotFoundException` gives found=false and no error.

- [ ] **Step 2: Run, expect FAIL**
- [ ] **Step 3: Implement**

`kms_key.go`:

```go
package secretsmanager

import (
	"regexp"
	"strings"
)

var kmsKeyIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// KMSKeyID is the KMS key a MASTER_KEY_REF names (key ARN, alias, alias ARN or key
// id), or "" for a local reference ("local:…") or none, which leaves Secrets
// Manager on its default key.
func KMSKeyID(masterKeyRef string) string {
	ref := strings.TrimSpace(masterKeyRef)
	switch {
	case strings.HasPrefix(ref, "arn:aws:kms:"), strings.HasPrefix(ref, "alias/"), kmsKeyIDPattern.MatchString(ref):
		return ref
	default:
		return ""
	}
}
```

`wallet_store.go`:
- `walletAPI` gains `DescribeSecret`;
- `WalletStore` gains `kmsKeyID string`;
- `NewWalletStore(client *awssm.Client, kmsKeyID string)`;
- `Create` sets `KmsKeyId: aws.String(s.kmsKeyID)` when not empty.

```go
// Lookup returns the ARN of the secret named name, and false when there is none.
func (s *WalletStore) Lookup(ctx context.Context, name string) (string, bool, error) {
	if s == nil || s.api == nil {
		return "", false, fmt.Errorf("secrets manager: client is nil")
	}
	out, err := s.api.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String(name)})
	var missing *smtypes.ResourceNotFoundException
	if errors.As(err, &missing) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return aws.ToString(out.ARN), true, nil
}
```

`wallet/service.go`: extract the name used at line ~194:

```go
// ShareBSecretName is the Secrets Manager name of a wallet's share B.
func ShareBSecretName(walletID uuid.UUID) string {
	return fmt.Sprintf("vault/wallet/%s/share-b", walletID.String())
}
```

`wallet_service_provider.go`:

```go
Secrets: sweepsecrets.NewWalletStore(secrets, sweepsecrets.KMSKeyID(facades.Config().GetString("vault.master_key_ref"))),
```

- [ ] **Step 4: PASS**: `go test ./app/adapters/secretsmanager/ ./app/services/wallet/ -count=1`; `go build ./...`
- [ ] **Step 5: Commit** `feat(custody): shares are sealed with the KMS key MASTER_KEY_REF names`

### Task 4: `secrets:migrate-shares`

**Files:**
- Modify: `app/repositories/wallet_repository.go`: `SetMPCSecretARN(ctx, id, arn)` → `r.updateColumn(ctx, id, "mpc_secret_arn", arn, "set wallet secret arn")`
- Create: `app/services/sharemigration/service.go`, `service_test.go`
- Create: `app/console/commands/secrets_migrate_shares.go`, test in `commands_test.go`
- Modify: `bootstrap/commands.go`

**Behaviour, per wallet with a non-empty `mpc_secret_arn`:**
1. `name := wallet.ShareBSecretName(w.ID)`, then `target.Lookup(name)`.
2. If the target has it and the DB ARN already equals that ARN: status `in place`, nothing read.
3. Else read `source.Binary(w.MPCSecretARN)`:
   - **Target has the name:** read `target.Binary(targetARN)`. Equal bytes → status `pointed` (rewrite the ARN when `--apply`). Different bytes → error `conflict`; never overwrite.
   - **Target lacks the name:** without `--apply`, status `would migrate`. With `--apply`:
     - `target.Create(name, bytes)` (sealed with the master key);
     - read the result back and compare byte for byte;
     - `SetMPCSecretARN`;
     - status `migrated`.
4. Errors do not stop the run. They are collected, every wallet is reported, and the command exits non-zero when any wallet failed. Re-running is safe: a share created before a failed ARN update is found as `pointed`.

The source is never written. A revert is the same command with the endpoints swapped: target LocalStack, `--from-endpoint` empty.

- [ ] **Step 1: Failing service tests**, with fakes for `Wallets`, `Source`, `Target`:
  - `TestShare_Migration_Run_DryRunWritesNothing`
  - `TestShare_Migration_Run_CopiesVerifiesAndRewritesTheARN`
  - `TestShare_Migration_Run_IsIdempotent` (second run: `in place`)
  - `TestShare_Migration_Run_PointsAtAnEqualExistingSecret`
  - `TestShare_Migration_Run_RefusesAConflictingSecret` (no write, error reported)
  - `TestShare_Migration_Run_ReportsAReadBackMismatch` (no ARN rewrite)
  - `TestShare_Migration_Run_SkipsWalletsWithoutAShare`
  - `TestShare_Migration_Run_ContinuesPastAFailedWallet`

- [ ] **Step 2: Run, expect FAIL**: `go test ./app/services/sharemigration/ -count=1`
- [ ] **Step 3: Implement** `service.go`

```go
package sharemigration

// Wallets lists the wallets and rewrites where their share B lives.
type Wallets interface {
	FindAll(ctx context.Context) ([]models.Wallet, error)
	SetMPCSecretARN(ctx context.Context, id uuid.UUID, arn string) error
}

// Source reads a share by the ARN the wallet row holds.
type Source interface {
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// Target is where the shares go: the app's own Secrets Manager.
type Target interface {
	Lookup(ctx context.Context, name string) (arn string, found bool, err error)
	Create(ctx context.Context, name string, secret []byte) (string, error)
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// Row is one wallet's outcome. ARNs are not secret; share bytes never leave the service.
type Row struct {
	WalletID uuid.UUID
	Chain    string
	Status   string // in place | pointed | would migrate | migrated | failed
	From, To string
	Err      error
}

// Run moves every wallet's share B from source to target. Without apply it only
// reports. It returns every row and an error when any wallet failed.
func (s *Service) Run(ctx context.Context, apply bool) ([]Row, error)
```

`bytes.Equal` compares. Collected errors go through `errors.Join`.

Command `secrets_migrate_shares.go`, thin:
- signature `secrets:migrate-shares`, category `secrets`, options `--from-endpoint` (string) and `--apply` (bool);
- builds the source with `deps.SourceFor(endpoint)`, which returns a `sweepsecrets.NewWalletStore(sweepsecrets.NewClient(cfg, endpoint), "")`;
- calls `Run` and prints one line per row: `<status>  <wallet id>  <chain>  <from> -> <to>`;
- ends in `fail(ctx, err)` on error.

Wiring in `bootstrap/commands.go`:
- `Wallets`: `container.MustMake[*repositories.WalletRepository]()`;
- `Target`: the wallet store built as in `wallet_service_provider.go`;
- `SourceFor`: a closure over `*aws.Config`.

- [ ] **Step 4: PASS**: `go test ./app/services/sharemigration/ ./app/console/commands/ -count=1`; `go build ./...`; `go test ./tests/architecture/ -count=1` (thin commands, no query in commands).
- [ ] **Step 5: Commit** `feat(custody): secrets:migrate-shares moves share B between Secrets Manager endpoints`

### Task 5: `webhook_poller` process mode

**Files:**
- Create: `app/adapters/queue/sqs/poller.go`, `poller_test.go`
- Modify: `main.go`, `config/vault.go` (comment on `lambda_mode`)
- Modify: `.ai/guidelines/queues-and-workers.md`

- [ ] **Step 1: Failing tests**, with a fake `pollAPI` and an injected sleep:
  - `TestPoller_Run_DeletesWhatTheHandlerDidNotReportAsFailed`: two messages, one failure; DeleteMessageBatch gets the other's receipt handle.
  - `TestPoller_Run_DeletesNothingWhenTheHandlerErrs`
  - `TestPoller_Run_HandsTheHandlerTheSQSRecords`: MessageId, ReceiptHandle, Body and string message attributes.
  - `TestPoller_Run_BacksOffAfterAReceiveError`: sleep called, then receives again.
  - `TestPoller_Run_StopsWhenTheContextEnds`: returns nil, no further receive.
- [ ] **Step 2: Run, expect FAIL**: `go test ./app/adapters/queue/sqs/ -run TestPoller -count=1`
- [ ] **Step 3: Implement** `poller.go`

```go
// Handler is the deployed SQS handler's shape (main.handleWebhookWorker).
type Handler func(context.Context, events.SQSEvent) (events.SQSEventResponse, error)

// Poller long-polls one queue and hands each batch to the deployed handler, as the
// Lambda event source mapping does: a message the handler does not report as failed
// is deleted; a failed one returns after its visibility timeout and reaches the DLQ
// after the queue's maxReceiveCount.
type Poller struct {
	api      pollAPI
	queueURL string
	sleep    func(context.Context, time.Duration)
}

func NewPoller(client *awssqs.Client, queueURL string) *Poller

func (p *Poller) Run(ctx context.Context, handle Handler) error
```

Receive with `MaxNumberOfMessages: 10`, `WaitTimeSeconds: 20`, `MessageAttributeNames: ["All"]`. After a receive error, back off 5 s; stop when `ctx` ends. Build the records from `out.Messages`. Never range over a `.Records` field here: the architecture test reserves that for deployed handlers.

`main.go`:

```go
	case "webhook_poller":
		runWebhookPoller()
```

```go
// runWebhookPoller is the webhook_worker Lambda's stand-in on a host without Lambda:
// it long-polls vault.queues.webhook and runs handleWebhookWorker on each batch. It
// replaces the Lambda; the two never consume one queue together.
func runWebhookPoller() {
	queueURL := facades.Config().GetString("vault.queues.webhook")
	if queueURL == "" {
		slog.Error("webhook poller needs WEBHOOK_QUEUE_URL")
		os.Exit(exitCodeFailure)
	}
	awsCfg := container.MustMake[*aws.Config]()
	client := queuesqs.NewClient(*awsCfg, facades.Config().GetString("vault.aws.endpoint_url"))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("webhook poller started", "queue", queueURL)
	if err := queuesqs.NewPoller(client, queueURL).Run(ctx, handleWebhookWorker); err != nil {
		slog.Error("webhook poller stopped", "error", err)
		os.Exit(exitCodeFailure)
	}
	slog.Info("webhook poller stopped")
}
```

Guideline: in the table, the SQS row's consumer becomes "the webhook worker (Lambda), or on a host without Lambda the `webhook_poller` process mode; never both". Add a rule line saying the same.

- [ ] **Step 4: PASS**: `go test ./app/adapters/queue/sqs/ -count=1`; `go test ./tests/architecture/ -count=1` (TestOne_Consumer_PerQueue must still pass); `go build ./...`
- [ ] **Step 5: Commit** `feat(queue): webhook_poller mode consumes the webhook queue on a host without Lambda`

### Task 6: Docs and full check

- [ ] `docs/AWS_DEV_STACK.md`: replace "What the service uses today" with the cutover:
  - the `.env` keys;
  - the migration command;
  - the poller daemon command;
  - the rollback.
- [ ] `make test-unit` (unit packages only; no integration suite on the remote machine) and `go vet ./...`.
- [ ] Commit, push, PR against `main`.

### Task 7: Cutover on the dev host (ssh forge@2.31.21.121)

Follow the runbook in `docs/AWS_DEV_STACK.md`, "Cutover of the dev host". It was rewritten after the plan review: freeze, environment audit, the order of the steps, signing proof per curve, the webhook switch and both rollbacks. It is the source of truth for this task.

**Review follow-ups (done in code):**
- an inherited `AWS_ENDPOINT_URL` cannot redirect the clients;
- the migration refuses a source that is the target, `--apply` into AWS without a KMS key, and empty shares;
- "in place" reads the copy;
- the ARN moves by compare-and-swap;
- buffers are zeroed;
- `--from-profile`;
- the poller finishes the batch in flight with a 55 s deadline, in batches of 5.

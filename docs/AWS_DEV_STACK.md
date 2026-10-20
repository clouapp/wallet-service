# AWS resources of the dev environment

The dev environment runs on one host outside AWS (the Forge server), not on Lambda.
It still needs the stateful AWS resources, and `template.dev.yaml` declares them as
the stack `waas-dev`:

| Resource | Name | Notes |
|---|---|---|
| Webhook queue | `vault-webhooks-dev` | visibility 60 s, retention 1 day, 10 receives, then the DLQ |
| Webhook DLQ | `vault-webhooks-dlq-dev` | retention 14 days |
| Webhook DLQ alarm | CloudWatch alarm `vault-webhooks-dlq-not-empty-dev` | in alarm while a message is visible in the webhook DLQ (`ApproximateNumberOfMessagesVisible` > 0, 5-minute maximum; missing data keeps the state, since a DLQ nobody touches stops reporting after six hours); notifies the SNS topic of the `AlarmTopicArn` parameter, none by default |
| Withdrawal queue | `vault-withdrawals-dev` | visibility 120 s, retention 1 day, 3 receives, then the DLQ |
| Withdrawal DLQ | `vault-withdrawals-dlq-dev` | retention 14 days |
| Master key | `alias/waas-dev-master` | symmetric, yearly rotation, **kept** when the stack is deleted |
| Host identity | IAM user `waas-dev-server`, policy `WaasServer-dev` | the queues, the master key, `vault/*` secrets; no console |
| Host access key | secret `waas/dev/server-access-key` | outside `vault/*`, so the host cannot read it back |

The queue names and settings are the ones `template.yaml` uses for staging and prod.

## Deploying

`make deploy ENV=dev` follows the slotkit pattern. `.env.dev` names the account and
the deploy credentials:

```dotenv
AWS_REGION=us-east-1
AWS_ACCOUNT_ID=<12-digit account id>
AWS_DEPLOY_PROFILE=<profile in ~/.aws>   # or AMZ_ACCESS_KEY_ID + AMZ_SECRET_ACCESS_KEY
SERVER_SSH=forge@<host>
```

- Credentials, first match wins:
  - `AMZ_*`: a deploy user's keys. They reach `aws` and `sam` as `AWS_*` in the AWS
    targets only; the app and docker compose never see them.
  - `AWS_DEPLOY_PROFILE`: a named profile in `~/.aws`, so no key sits in the dotenv.
  - Neither: the shell's own credentials.

  The shell's `AWS_PROFILE` and `AWS_SESSION_TOKEN` cannot outrank the first two,
  `AWS_ENDPOINT_URL` is always dropped, and nothing is written to `~/.aws`.
- `check-account` runs first and refuses unless those credentials belong to
  `AWS_ACCOUNT_ID`.

```bash
make deploy ENV=dev          # check-account, cfn-lint, cloudformation deploy, outputs
make server-credentials      # host access key -> AWS profile waas-dev on SERVER_SSH
make verify-aws              # proves everything from the host, as that profile
```

- **`server-credentials`** reads the access key from Secrets Manager and pipes it over
  ssh into `scripts/aws-install-profile.sh`. That script runs `aws configure import`
  on the host. The key never reaches argv, the terminal or a log. Only the `waas-dev`
  profile is written; the host's `default` profile stays as it is.
- **`verify-aws`** runs `scripts/aws-verify.sh` on the host with `AWS_PROFILE=waas-dev`:
  - every queue sends, receives and deletes a tagged probe;
  - the main queues keep the visibility and redrive the stack declares;
  - a message past its receive limit lands in a DLQ (on scratch queues);
  - the master key encrypts, decrypts and issues a data key;
  - Secrets Manager stores and returns a secret sealed with that key.

  Anything else a receive returns goes straight back to its queue. The scratch queues
  and the probe secret are deleted on exit.

Other targets: `make stack-outputs`, `make dlq-check`, `make delete ENV=dev`. Deleting
the stack keeps the master key.

### Deploy credentials

Any identity with enough rights works. Admin access is the simplest, but avoid root
access keys. A dedicated deploy user needs, for this stack only (replace `ACCOUNT`):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow", "Action": "cloudformation:*", "Resource": "arn:aws:cloudformation:us-east-1:ACCOUNT:stack/waas-dev/*" },
    { "Effect": "Allow", "Action": ["cloudformation:ValidateTemplate", "cloudformation:GetTemplateSummary", "sts:GetCallerIdentity"], "Resource": "*" },
    { "Effect": "Allow", "Action": "sqs:*", "Resource": "arn:aws:sqs:us-east-1:ACCOUNT:vault-*-dev" },
    { "Effect": "Allow", "Action": ["cloudwatch:PutMetricAlarm", "cloudwatch:DeleteAlarms", "cloudwatch:DescribeAlarms", "cloudwatch:TagResource", "cloudwatch:UntagResource", "cloudwatch:ListTagsForResource"], "Resource": "arn:aws:cloudwatch:us-east-1:ACCOUNT:alarm:vault-*-dev" },
    { "Effect": "Allow", "Action": ["kms:CreateKey", "kms:ListAliases"], "Resource": "*" },
    { "Effect": "Allow", "Action": ["kms:DescribeKey", "kms:GetKeyPolicy", "kms:PutKeyPolicy", "kms:EnableKeyRotation", "kms:GetKeyRotationStatus", "kms:TagResource", "kms:UntagResource", "kms:ListResourceTags", "kms:ListKeyPolicies", "kms:UpdateKeyDescription", "kms:ScheduleKeyDeletion", "kms:CreateAlias", "kms:DeleteAlias", "kms:UpdateAlias"], "Resource": ["arn:aws:kms:us-east-1:ACCOUNT:key/*", "arn:aws:kms:us-east-1:ACCOUNT:alias/waas-dev-*"] },
    { "Effect": "Allow", "Action": ["iam:CreateUser", "iam:DeleteUser", "iam:GetUser", "iam:TagUser", "iam:UntagUser", "iam:AttachUserPolicy", "iam:DetachUserPolicy", "iam:ListAttachedUserPolicies", "iam:CreateAccessKey", "iam:DeleteAccessKey", "iam:ListAccessKeys", "iam:UpdateAccessKey", "iam:ListUserPolicies", "iam:ListGroupsForUser", "iam:ListUserTags", "iam:GetLoginProfile", "iam:ListMFADevices", "iam:ListSigningCertificates", "iam:ListSSHPublicKeys", "iam:ListServiceSpecificCredentials"], "Resource": "arn:aws:iam::ACCOUNT:user/waas-dev-server" },
    { "Effect": "Allow", "Action": ["iam:CreatePolicy", "iam:DeletePolicy", "iam:GetPolicy", "iam:GetPolicyVersion", "iam:ListPolicyVersions", "iam:CreatePolicyVersion", "iam:DeletePolicyVersion", "iam:ListEntitiesForPolicy"], "Resource": "arn:aws:iam::ACCOUNT:policy/WaasServer-dev" },
    { "Effect": "Allow", "Action": ["secretsmanager:CreateSecret", "secretsmanager:DeleteSecret", "secretsmanager:DescribeSecret", "secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue", "secretsmanager:UpdateSecret", "secretsmanager:TagResource"], "Resource": "arn:aws:secretsmanager:us-east-1:ACCOUNT:secret:waas/dev/*" }
  ]
}
```

## Running the service on these resources

How the service uses each resource:

- **Credentials:** the AWS configuration comes from the app config:
  - `vault.aws.profile` (`AWS_PROFILE`) and `vault.aws.region` (`AWS_REGION`);
  - `vault.aws.endpoint_url` (`AWS_ENDPOINT_URL`, LocalStack when set) for both Secrets Manager and SQS.

  A supervisor process does not inherit `.env`, so this is the only way those values reach the SDK.
- **KMS:** when `MASTER_KEY_REF` names a KMS key (the `MasterKeyArn` output, or `alias/waas-dev-master`), every new share B is sealed with it.
- **Secrets Manager:** `secrets:migrate-shares` moves the existing shares between two Secrets Managers.
- **SQS:** `LAMBDA_MODE=webhook_poller` consumes the webhook queue on a host without Lambda. It runs the same handler as the `webhook_worker` Lambda and never runs beside it. `WITHDRAWAL_QUEUE_URL` still has no producer or consumer.

### Cutover of the dev host (LocalStack → AWS)

Run these on the host, in `~/macro-wallets-backend.on-forge.com/current`, in this order.

1. **Freeze the wallet data.** Nothing may import or create wallets during the cutover.
2. **Audit the environment.** Read `/proc/<pid>/environ` of each daemon and `env` in the shell that runs artisan.
   - The environment outranks `.env`.
   - A stray `AWS_PROFILE`, `AWS_ENDPOINT_URL`, `MASTER_KEY_REF` or `WEBHOOK_QUEUE_URL` there silently wins.
3. **Deploy the new build on the current config.** That is LocalStack, with `MASTER_KEY_REF` still `local:…`. Check `/health` and the worker logs.
4. **Point the app at AWS.** In `.env`: `AWS_PROFILE=waas-dev`, `AWS_ENDPOINT_URL=` (empty), `MASTER_KEY_REF=<MasterKeyArn>`.
5. **Move the shares.**
   - The API still runs on the old config until step 6. Keep the window short, or stop the daemon from Forge for it.
   - Dry run, then apply:

     ```bash
     ./wallet-service artisan secrets:migrate-shares --from-endpoint=http://127.0.0.1:4566
     ./wallet-service artisan secrets:migrate-shares --from-endpoint=http://127.0.0.1:4566 --apply
     ```

   - The first line must read `target: AWS, sealed with arn:aws:kms:…`.
   - The command refuses a source equal to the target, an `--apply` into AWS without a KMS key, and empty shares.
   - Each copy is read back before its row moves, and the row moves only if it still holds the old ARN.
6. **Restart the service.** Run the dry run again: every wallet must be `in place`, which means the copy opens with the app's identity.
7. **Prove it.**
   - Spot-check one ARN: `aws secretsmanager describe-secret --secret-id <arn> --profile waas-dev --query '{arn:ARN,kms:KmsKeyId}'`. Expect account `430776404482` and the master key. (`list-secrets` needs a permission neither user has.)
   - Sign without broadcasting, one wallet per curve: secp256k1 (EVM/BTC) and ed25519 (SOL). Use `./wallet-service artisan withdraw:preflight`.
   - Run `make verify-aws` from the laptop. Do it before step 8: once the poller runs, it would be a second consumer of the webhook queue.
8. **Hand the webhooks to SQS.**
   1. Set `WEBHOOK_QUEUE_URL=<WebhookQueueUrl>` in `.env`. The running API keeps its outbox until it restarts.
   2. Add the poller daemon in Forge, with `stopwaitsecs` at 70 or more (a batch may run 55 s):

      ```bash
      /bin/bash -c 'cd /home/forge/macro-wallets-backend.on-forge.com/current && exec env -u AWS_ENDPOINT_URL -u AWS_PROFILE LAMBDA_MODE=webhook_poller ./wallet-service'
      ```

   3. Wait for the outbox to drain: no `webhook_events` row left with `delivery_status` pending or retrying.
   4. Restart the API. Webhooks now go API → SQS → poller.
   5. `make dlq-check` should stay at 0.

   A Forge deploy must restart both daemons. Otherwise the poller keeps running an old release, which release pruning may later delete.

**Webhook rollback:**
1. Let the queue drain (`ApproximateNumberOfMessages` 0).
2. Stop the poller daemon.
3. Empty `WEBHOOK_QUEUE_URL`.
4. Restart the API.

**Share rollback:**
1. Keep the wallet data frozen and the API stopped, or at least the window short.
2. In `.env`: `AWS_ENDPOINT_URL=http://127.0.0.1:4566`, `MASTER_KEY_REF=local:dev-master-key`. The `AWS_PROFILE` line may go.
3. Dry run, then apply. The profile reads the AWS copies, and the rows point back at the equal LocalStack copies:

   ```bash
   ./wallet-service artisan secrets:migrate-shares --from-profile=waas-dev
   ./wallet-service artisan secrets:migrate-shares --from-profile=waas-dev --apply
   ```

4. Restart, then run the same dry run: every wallet `in place`.

**Known stops:**
- **Conflict:** a copy under the share's name with other bytes is never overwritten.
  - The server user cannot delete `vault/wallet/*`. An admin deletes it, and the name stays reserved during the recovery window.
- **Unreadable source:** a wallet whose source share cannot be read stays `failed` on its source ARN, and nothing is written for it.
  - This covers rows imported without their share.
  - Decide per wallet, before the cutover, whether it is kept.

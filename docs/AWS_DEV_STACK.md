# AWS resources of the dev environment

The dev environment runs on one host outside AWS (the Forge server), not on Lambda.
It still needs the stateful AWS resources, and `template.dev.yaml` declares them as
the stack `waas-dev`:

| Resource | Name | Notes |
|---|---|---|
| Webhook queue | `vault-webhooks-dev` | visibility 60 s, retention 1 day, 10 receives, then the DLQ |
| Webhook DLQ | `vault-webhooks-dlq-dev` | retention 14 days |
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

1. **Point the app at AWS.** In `.env`: `AWS_PROFILE=waas-dev`, `AWS_ENDPOINT_URL=` (empty), `MASTER_KEY_REF=<MasterKeyArn>`.
2. **Move the shares.** Check first, then apply, then check that nothing is left:

   ```bash
   ./wallet-service artisan secrets:migrate-shares --from-endpoint=http://127.0.0.1:4566
   ./wallet-service artisan secrets:migrate-shares --from-endpoint=http://127.0.0.1:4566 --apply
   ./wallet-service artisan secrets:migrate-shares --from-endpoint=http://127.0.0.1:4566
   ```

   - Each copy is sealed with the master key and read back before the wallet row moves to it.
   - The last run must show every wallet `in place`.
   - The LocalStack shares stay as they were.
3. **Restart the service.**
4. **Start the webhook poller as a second daemon:**

   ```bash
   /bin/bash -lc 'cd /home/forge/macro-wallets-backend.on-forge.com/current && LAMBDA_MODE=webhook_poller exec ./wallet-service'
   ```

   The daemon's `LAMBDA_MODE` outranks the one in `.env`.
5. **Hand the webhooks to SQS.** Set `WEBHOOK_QUEUE_URL=<WebhookQueueUrl>` and restart the service. Its local outbox stops, and webhooks go through SQS to the poller. `make dlq-check` should stay at 0.

**Rollback:**

1. Empty `WEBHOOK_QUEUE_URL` and stop the poller.
2. Set `AWS_ENDPOINT_URL=http://127.0.0.1:4566`, but keep `AWS_PROFILE=waas-dev`: the next step reads AWS with it, and LocalStack accepts any credentials.
3. Run `secrets:migrate-shares --apply` with `--from-endpoint` empty. It reads each share in AWS and points the rows back at the equal LocalStack copies.
4. Restore `AWS_PROFILE` and `MASTER_KEY_REF`, then restart.

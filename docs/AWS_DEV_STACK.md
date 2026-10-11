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

## What the service uses today

These resources exist ahead of the code that will use them:

- **Secrets Manager** is the only one in use. It holds MPC share B under
  `vault/wallet/<id>/share-b`, and its ARN is stored in `wallets.mpc_secret_arn`. The
  host's app still points at LocalStack (`AWS_ENDPOINT_URL`, `default` profile).
  Moving it to AWS means copying every share into AWS and rewriting those ARNs;
  without that, existing wallets cannot sign.
- **SQS:** setting `WEBHOOK_QUEUE_URL` turns the local webhook outbox off, and only
  the `webhook_worker` Lambda consumes the queue. Leave it empty on this host until a
  local consumer exists, or webhooks stop being delivered. `WITHDRAWAL_QUEUE_URL` has
  no producer or consumer.
- **KMS:** nothing in the code calls it yet. `MASTER_KEY_REF` (the `MasterKeyArn`
  output) is read and unused.
- **Credentials:** the Forge supervisor process does not inherit `.env`, so the AWS SDK
  reads `~/.aws` on the host.

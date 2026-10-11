#!/usr/bin/env bash
# Proves, with the AWS identity the host runs with, that an environment's stack
# (template.dev.yaml) is usable:
#   - every queue answers send, receive and delete, and the main queues have the
#     visibility and redrive settings the stack declares;
#   - a message moves to a dead-letter queue once its receive limit is exceeded
#     (on scratch queues, created and deleted here);
#   - the master key encrypts, decrypts and issues data keys;
#   - Secrets Manager stores, returns and deletes a secret sealed with that key.
#
# The real queues only see the probe messages: anything else a receive returns is
# made visible again at once. Scratch queues and the probe secret are deleted on
# exit, whatever happened.
#
# Usage: aws-verify.sh <dev|staging|prod>   (AWS_PROFILE / AWS_REGION pick the identity)
# Run by `make verify-aws`, over ssh on the server.
set -euo pipefail

env_name="${1:?usage: aws-verify.sh <dev|staging|prod>}"
region="${AWS_REGION:-us-east-1}"
stamp="$(date -u +%Y%m%d%H%M%S)-$$"
key_alias="alias/waas-${env_name}-master"
probe_attribute="waas-verify"

failures=0
work="$(mktemp -d)"
scratch_queue_url=""
scratch_dlq_url=""
probe_secret=""

awsq() { aws --region "$region" --output text "$@"; }
ok() { printf '  ok    %s\n' "$*"; }
fail() {
	printf '  FAIL  %s\n' "$*" >&2
	failures=$((failures + 1))
}

cleanup() {
	[ -z "$scratch_queue_url" ] || awsq sqs delete-queue --queue-url "$scratch_queue_url" >/dev/null 2>&1 || true
	[ -z "$scratch_dlq_url" ] || awsq sqs delete-queue --queue-url "$scratch_dlq_url" >/dev/null 2>&1 || true
	[ -z "$probe_secret" ] || awsq secretsmanager delete-secret --secret-id "$probe_secret" \
		--force-delete-without-recovery >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

queue_url() { awsq sqs get-queue-url --queue-name "$1" --query QueueUrl; }

# Receives from $1 until the probe tagged $2 shows up (deleting it) or $3 polls pass.
# Messages that are not the probe go straight back to the queue.
receive_probe() {
	local url="$1" tag="$2" polls="$3" handle value
	for _ in $(seq "$polls"); do
		while IFS=$'\t' read -r handle value; do
			[ -n "$handle" ] && [ "$handle" != "None" ] || continue
			if [ "$value" = "$tag" ]; then
				awsq sqs delete-message --queue-url "$url" --receipt-handle "$handle" >/dev/null
				return 0
			fi
			awsq sqs change-message-visibility --queue-url "$url" --receipt-handle "$handle" \
				--visibility-timeout 0 >/dev/null
		done < <(awsq sqs receive-message --queue-url "$url" --max-number-of-messages 10 \
			--wait-time-seconds 2 --message-attribute-names "$probe_attribute" \
			--query "Messages[].[ReceiptHandle, MessageAttributes.\"$probe_attribute\".StringValue]")
	done
	return 1
}

send_probe() {
	awsq sqs send-message --queue-url "$1" --message-body "waas-verify $2" \
		--message-attributes "{\"$probe_attribute\":{\"DataType\":\"String\",\"StringValue\":\"$2\"}}" \
		--query MessageId >/dev/null
}

echo "identity: $(awsq sts get-caller-identity --query Arn) (region $region, environment $env_name)"

echo "SQS"
check_queue() {
	local name="$1" visibility="$2" dlq="$3" max_receives="$4" url attrs
	if ! url="$(queue_url "$name" 2>&1)"; then
		fail "$name: not reachable ($url)"
		return
	fi
	if [ -n "$visibility" ]; then
		attrs="$(awsq sqs get-queue-attributes --queue-url "$url" \
			--attribute-names VisibilityTimeout RedrivePolicy \
			--query "[Attributes.VisibilityTimeout, Attributes.RedrivePolicy]")"
		case "$attrs" in
		"$visibility"*"$dlq"*"\"maxReceiveCount\":$max_receives"* | "$visibility"*"$dlq"*"\"maxReceiveCount\":\"$max_receives\""*) ;;
		*)
			fail "$name: settings differ from the stack (visibility $visibility, $max_receives receives into $dlq): $attrs"
			return
			;;
		esac
	fi
	local tag="$stamp-$name"
	if send_probe "$url" "$tag" && receive_probe "$url" "$tag" 5; then
		ok "$name: send, receive, delete${visibility:+; visibility ${visibility}s, $max_receives receives into $dlq}"
	else
		fail "$name: the probe did not come back"
	fi
}
check_queue "vault-webhooks-$env_name" 60 "vault-webhooks-dlq-$env_name" 10
check_queue "vault-webhooks-dlq-$env_name" "" "" ""
check_queue "vault-withdrawals-$env_name" 120 "vault-withdrawals-dlq-$env_name" 3
check_queue "vault-withdrawals-dlq-$env_name" "" "" ""

redrive_check() {
	local dlq_arn tag="$stamp-redrive"
	scratch_dlq_url="$(awsq sqs create-queue --queue-name "waas-verify-$env_name-$stamp-dlq" --query QueueUrl)"
	dlq_arn="$(awsq sqs get-queue-attributes --queue-url "$scratch_dlq_url" \
		--attribute-names QueueArn --query Attributes.QueueArn)"
	printf '{"VisibilityTimeout":"1","RedrivePolicy":"{\\"deadLetterTargetArn\\":\\"%s\\",\\"maxReceiveCount\\":\\"1\\"}"}' \
		"$dlq_arn" >"$work/redrive.json"
	scratch_queue_url="$(awsq sqs create-queue --queue-name "waas-verify-$env_name-$stamp" \
		--attributes "file://$work/redrive.json" --query QueueUrl)"

	send_probe "$scratch_queue_url" "$tag"
	# First receive spends the only allowed one; the next receive moves it to the DLQ.
	awsq sqs receive-message --queue-url "$scratch_queue_url" --wait-time-seconds 5 \
		--query "Messages[].MessageId" >/dev/null
	sleep 2
	awsq sqs receive-message --queue-url "$scratch_queue_url" --wait-time-seconds 2 \
		--query "Messages[].MessageId" >/dev/null
	receive_probe "$scratch_dlq_url" "$tag" 10
}
if redrive_check; then
	ok "dead-letter redrive: a message past its receive limit lands in the DLQ"
else
	fail "dead-letter redrive: the message did not reach the DLQ"
fi

echo "KMS"
kms_check() {
	local state probe_b64 data_key cipher_b64
	state="$(awsq kms describe-key --key-id "$key_alias" --query KeyMetadata.KeyState)" || return 1
	[ "$state" = "Enabled" ] || {
		echo "key state $state" >&2
		return 1
	}

	printf 'waas-verify %s' "$stamp" >"$work/plain"
	probe_b64="$(base64 <"$work/plain" | tr -d '\n')"
	awsq kms encrypt --key-id "$key_alias" --plaintext "fileb://$work/plain" \
		--query CiphertextBlob | base64 -d >"$work/cipher"
	[ "$(awsq kms decrypt --ciphertext-blob "fileb://$work/cipher" --query Plaintext)" = "$probe_b64" ] || return 1

	read -r data_key cipher_b64 < <(awsq kms generate-data-key --key-id "$key_alias" --key-spec AES_256 \
		--query "[Plaintext, CiphertextBlob]")
	printf '%s' "$cipher_b64" | base64 -d >"$work/data-key"
	[ "$(awsq kms decrypt --ciphertext-blob "fileb://$work/data-key" --query Plaintext)" = "$data_key" ]
}
if kms_check; then
	ok "$key_alias: enabled; encrypt/decrypt and data key (envelope) round trips"
else
	fail "$key_alias: not usable"
fi

echo "Secrets Manager"
secrets_check() {
	local value="waas-verify $stamp"
	probe_secret="vault/verify/$env_name-$stamp"
	awsq secretsmanager create-secret --name "$probe_secret" --kms-key-id "$key_alias" \
		--secret-string "$value" --query ARN >/dev/null || return 1
	[ "$(awsq secretsmanager get-secret-value --secret-id "$probe_secret" --query SecretString)" = "$value" ] || return 1
	awsq secretsmanager delete-secret --secret-id "$probe_secret" --force-delete-without-recovery >/dev/null
	probe_secret=""
}
if secrets_check; then
	ok "vault/verify/*: create (sealed with $key_alias), read back, delete"
else
	fail "vault/verify/*: secret round trip failed"
fi

if [ "$failures" -gt 0 ]; then
	echo "aws-verify: $failures check(s) failed" >&2
	exit 1
fi
echo "aws-verify: all checks passed"

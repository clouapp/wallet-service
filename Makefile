.PHONY: build deploy local invoke-scan test clean

# ---------------------------------------------------------------------------
# SAM CLI
# ---------------------------------------------------------------------------

build:
	sam build

deploy: build
	sam deploy --guided

deploy-ci: build
	sam deploy --no-confirm-changeset --no-fail-on-empty-changeset

# Local dev: emulate API Gateway + Lambda
local: build
	sam local start-api --warm-containers EAGER

# Invoke scanner manually
invoke-scan:
	sam local invoke DepositScannerFunction -e '{"chain": "eth"}'

invoke-scan-remote:
	aws lambda invoke --function-name vault-deposit-scanner-dev \
		--payload '{"chain": "eth"}' /dev/stdout

# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

migrate:
	psql $(DATABASE_URL) < database/migrations/001_init.sql

migrate-prod:
	psql $(PROD_DATABASE_URL) < database/migrations/001_init.sql

# ---------------------------------------------------------------------------
# Testing
# ---------------------------------------------------------------------------

test:
	go test ./... -v -race -count=1

lint:
	golangci-lint run ./...

# ---------------------------------------------------------------------------
# Monitoring
# ---------------------------------------------------------------------------

logs-api:
	sam logs -n VaultApiFunction --stack-name vault --tail

logs-scanner:
	sam logs -n DepositScannerFunction --stack-name vault --tail

logs-webhook:
	sam logs -n WebhookWorkerFunction --stack-name vault --tail

logs-withdrawal:
	sam logs -n WithdrawalWorkerFunction --stack-name vault --tail

# Check DLQ depth
dlq-check:
	@echo "=== Webhook DLQ ==="
	@aws sqs get-queue-attributes \
		--queue-url $$(aws sqs get-queue-url --queue-name vault-webhooks-dlq-dev --query QueueUrl --output text) \
		--attribute-names ApproximateNumberOfMessages \
		--query 'Attributes.ApproximateNumberOfMessages' --output text
	@echo "=== Withdrawal DLQ ==="
	@aws sqs get-queue-attributes \
		--queue-url $$(aws sqs get-queue-url --queue-name vault-withdrawals-dlq-dev --query QueueUrl --output text) \
		--attribute-names ApproximateNumberOfMessages \
		--query 'Attributes.ApproximateNumberOfMessages' --output text

# Replay DLQ messages back to main queue
dlq-replay-webhooks:
	aws sqs start-message-move-task \
		--source-arn $$(aws sqs get-queue-attributes --queue-url vault-webhooks-dlq-dev --attribute-names QueueArn --query Attributes.QueueArn --output text) \
		--destination-arn $$(aws sqs get-queue-attributes --queue-url vault-webhooks-dev --attribute-names QueueArn --query Attributes.QueueArn --output text)

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

clean:
	rm -rf .aws-sam/
	sam delete --stack-name vault --no-prompts

ping:
	@curl -s $$(aws cloudformation describe-stacks --stack-name vault \
		--query 'Stacks[0].Outputs[?OutputKey==`ApiEndpoint`].OutputValue' --output text)health | jq .

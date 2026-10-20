.PHONY: help build localstack-hooks e2e-tools clean run dev dev-back dev-front stop deploy deploy-guided delete validate check-account require-sam-env require-server-ssh stack-outputs server-credentials verify-aws local test test-integration test-unit arch arch-baseline contract contract-update test-coverage test-race test-verbose lint fmt vet security mocks migrate migrate-rollback migrate-status migrate-fresh migrate-fresh-seed migrate-fresh-hard db-reset db-seed key-generate jwt-secret docker-up docker-down docker-logs docker-build docker-test docker-status ecr-login ecr-push logs-api logs-scanner logs-webhook dlq-check dlq-replay-webhooks dlq-replay-withdrawals ping env-info swagger-install swagger-generate swagger-fmt deps-install deps-update

# =============================================================================
# Configuration
# =============================================================================

# Default environment
ENV ?= dev

# Load environment-specific .env file
ifeq ($(ENV),prod)
  -include .env.prod
  STACK_NAME = waas-prod
else ifeq ($(ENV),staging)
  -include .env.staging
  STACK_NAME = waas-staging
else
  -include .env.dev
  STACK_NAME = waas-dev
endif

# dev runs on one host outside AWS and only needs the stateful AWS resources:
# template.dev.yaml (queues, master KMS key, the host's IAM user). staging and prod
# deploy the Lambda application in template.yaml. See docs/AWS_DEV_STACK.md.
ifeq ($(ENV),dev)
  TEMPLATE_FILE = template.dev.yaml
else
  TEMPLATE_FILE = template.yaml
endif

# Project paths
FRONT_DIR = ../front

# Destructive test database; must start with vault_unit_test. vault (dev) and
# vault_test (local e2e stack) are refused by tests/feature/support/testenv.
# make test-integration migrates it fresh once as a TEMPLATE and clones it into one
# database per worker (<name>_p1.._pN, dropped at the end), under TEST_DB_LOCK.
TEST_DB_DATABASE ?= vault_unit_test
export TEST_DB_DATABASE
TEST_DB_PREFIX := vault_unit_test
TEST_DB_LOCK ?= $(HOME)/.local/state/macro-e2e/locks/vault_unit_test.lock
# Integration test binaries running at once (1..6, tests/feature/support/testenv.MaxWorkers).
TEST_PARALLEL ?= 4
# Safety net per go test invocation, not a target: the full suite takes a few minutes.
TEST_TIMEOUT ?= 30m
TEST_FLAGS ?=

# A package is an integration package when its tests import the test environment
# (tests/feature/support/testenv, tests/feature/support/testutil, tests) or boot the app (bootstrap): those reach
# PostgreSQL and Redis. Every other package is a unit package.
TEST_PACKAGE_IMPORTS = go list -f '{{.ImportPath}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}' ./...
INTEGRATION_TEST_IMPORT = / github\.com\/macrowallets\/waas\/(tests\/feature\/support\/testenv|tests\/feature\/support\/testutil|tests\/testenv|tests\/testutil|tests|bootstrap)( |$$)/
INTEGRATION_TEST_PACKAGES = $(shell $(TEST_PACKAGE_IMPORTS) | awk '$(INTEGRATION_TEST_IMPORT) {print $$1}')
UNIT_TEST_PACKAGES = $(shell $(TEST_PACKAGE_IMPORTS) | awk '!$(INTEGRATION_TEST_IMPORT) {print $$1}')

# golangci-lint v2 reads .golangci.yml; v1 cannot
GOLANGCI_LINT_VERSION ?= v2.5.0

# mockery v2 reads .mockery.yaml. Pin the binary and the Go toolchain so
# `make mocks` regenerates tests/mocks the same way on every machine.
# Only _test.go files may import tests/mocks.
MOCKERY_VERSION := v2.53.7
GO_TOOLCHAIN := go1.25.0

# Docker configuration
# The running containers were created from .env.dev (ports 4567/5433/6380); composing without it
# renders other ports and recreates postgres, redis and waas-localstack.
DOCKER_COMPOSE_ENV_FILE ?= .env.dev
DOCKER_COMPOSE = docker compose $(if $(wildcard $(DOCKER_COMPOSE_ENV_FILE)),--env-file $(DOCKER_COMPOSE_ENV_FILE))
DOCKER_IMAGE_NAME = waas-service
DOCKER_TAG ?= latest

# AWS configuration
AWS_REGION ?= us-east-1
# The account .env.$(ENV) names. check-account compares the credentials against it,
# so it is read before the fallback below fills AWS_ACCOUNT_ID from those credentials.
AWS_DECLARED_ACCOUNT_ID := $(strip $(AWS_ACCOUNT_ID))
ifeq ($(AWS_DECLARED_ACCOUNT_ID),)
  AWS_ACCOUNT_ID := $(shell aws sts get-caller-identity --query Account --output text 2>/dev/null)
endif

# Deploy credentials for the AWS targets below, from .env.$(ENV), first match wins:
# - AMZ_ACCESS_KEY_ID/AMZ_SECRET_ACCESS_KEY: a deploy user's keys (never AWS_*, which
#   the app and docker compose read). They reach aws/sam as AWS_* in these targets only.
# - AWS_DEPLOY_PROFILE: a named profile in ~/.aws, so no key sits in the dotenv.
# - neither: the shell's own credentials.
# The shell's AWS_PROFILE/AWS_SESSION_TOKEN cannot outrank the first two, nothing is
# written to ~/.aws, and AWS_ENDPOINT_URL (LocalStack) never reaches these commands.
# check-account is the backstop either way.
AWS_TARGETS = check-account deploy delete stack-outputs server-credentials dlq-check dlq-replay-webhooks dlq-replay-withdrawals
ifneq ($(strip $(AMZ_ACCESS_KEY_ID)),)
$(AWS_TARGETS): export AWS_ACCESS_KEY_ID := $(AMZ_ACCESS_KEY_ID)
$(AWS_TARGETS): export AWS_SECRET_ACCESS_KEY := $(AMZ_SECRET_ACCESS_KEY)
AWS_ENV = env -u AWS_PROFILE -u AWS_SESSION_TOKEN -u AWS_ENDPOINT_URL AWS_DEFAULT_REGION=$(AWS_REGION)
else ifneq ($(strip $(AWS_DEPLOY_PROFILE)),)
AWS_ENV = env -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN -u AWS_ENDPOINT_URL AWS_PROFILE=$(AWS_DEPLOY_PROFILE) AWS_DEFAULT_REGION=$(AWS_REGION)
else
AWS_ENV = env -u AWS_ENDPOINT_URL AWS_DEFAULT_REGION=$(AWS_REGION)
endif

# The host that runs the service outside AWS (dev), and the AWS profile
# server-credentials installs there for the stack's server user.
SERVER_SSH ?=
AWS_RUNTIME_PROFILE ?= waas-$(ENV)

ECR_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
ECR_IMAGE := $(ECR_REGISTRY)/$(DOCKER_IMAGE_NAME):$(DOCKER_TAG)

# SAM configuration
S3_FLAG := $(if $(S3_BUCKET),--s3-bucket $(S3_BUCKET),--resolve-s3)

# Parameter overrides for SAM deployment
PARAMETER_OVERRIDES = \
	Environment=$(ENVIRONMENT) \
	DatabaseURL=$(DATABASE_URL) \
	RedisHost=$(REDIS_HOST) \
	EthRpcURL=$(ETH_RPC_URL) \
	PolygonRpcURL=$(POLYGON_RPC_URL) \
	SolanaRpcURL=$(SOLANA_RPC_URL) \
	BtcRpcURL=$(BTC_RPC_URL) \
	ApiKeySecret=$(API_KEY_SECRET) \
	MasterKeyRef=$(MASTER_KEY_REF)

# =============================================================================
# Docker health check — auto-starts postgres + redis when needed
# =============================================================================

define ensure_docker
	@if ! docker ps --format '{{.Names}}' | grep -q waas-postgres; then \
		echo "⚠️  PostgreSQL not running — starting Docker services..."; \
		$(DOCKER_COMPOSE) up -d postgres redis; \
		echo "⏳ Waiting for PostgreSQL to accept connections..."; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
			docker exec waas-postgres pg_isready -U vault -d vault >/dev/null 2>&1 && break; \
			sleep 1; \
		done; \
		if ! docker exec waas-postgres pg_isready -U vault -d vault >/dev/null 2>&1; then \
			echo "❌ PostgreSQL failed to start within 15 s"; \
			exit 1; \
		fi; \
		echo "✅ PostgreSQL ready"; \
	fi
endef

define ensure_test_database
	@case "$(TEST_DB_DATABASE)" in \
		vault|vault_test) echo "❌ $(TEST_DB_DATABASE) holds live data; refusing it as a test database"; exit 1 ;; \
		$(TEST_DB_PREFIX)_p[0-9]*) echo "❌ $(TEST_DB_DATABASE) is a worker clone name, not a template"; exit 1 ;; \
		$(TEST_DB_PREFIX)|$(TEST_DB_PREFIX)_*) ;; \
		*) echo "❌ TEST_DB_DATABASE must start with $(TEST_DB_PREFIX), got '$(TEST_DB_DATABASE)'"; exit 1 ;; \
	esac
	$(call ensure_docker)
	@echo "🧪 Test database: $(TEST_DB_DATABASE)"
	@if [ "$$(docker exec waas-postgres psql -U vault -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$(TEST_DB_DATABASE)'")" != "1" ]; then \
		echo "🧪 Creating isolated PostgreSQL database $(TEST_DB_DATABASE)..."; \
		docker exec waas-postgres createdb -U vault $(TEST_DB_DATABASE); \
	fi
endef

# run_with_test_databases runs go test $(1) under TEST_DB_LOCK: migrates the template
# fresh once, lets each test binary clone it into its own <template>_pN database and
# Redis index (tests/feature/support/testenv), and drops the clones on any exit.
define run_with_test_databases
	$(call ensure_test_database)
	@mkdir -p "$(dir $(TEST_DB_LOCK))"
	@echo "🔒 Taking $(TEST_DB_LOCK)..."
	@flock "$(TEST_DB_LOCK)" sh -c 'set -a; [ ! -f .env.dev ] || . ./.env.dev; . ./.env.testing; set +a; \
		export DB_DATABASE="$(TEST_DB_DATABASE)" TEST_DB_REQUIRED=1; \
		trap "go run ./tests/feature/support/testdb drop-clones" EXIT; trap "exit 130" INT TERM; \
		go run ./tests/feature/support/testdb drop-clones && go run ./tests/feature/support/testdb prepare && \
		TEST_DB_TEMPLATE="$(TEST_DB_DATABASE)" TEST_DB_WORKERS="$(TEST_PARALLEL)" \
			go test -p "$(TEST_PARALLEL)" -count=1 -timeout $(TEST_TIMEOUT) $(1)'
endef

define ensure_env_dev
	@if [ ! -f .env.dev ]; then \
		echo "📋 Creating .env.dev from .env.dev.example..."; \
		cp .env.dev.example .env.dev; \
	fi
endef

# If APP_KEY is missing or empty, run Goravel's key:generate (writes to .env.dev via --env)
define ensure_app_key_dev
	@if ! grep -q '^APP_KEY=' .env.dev 2>/dev/null; then \
		echo "APP_KEY=" >> .env.dev; \
	fi
	@KEY_VAL=$$(grep '^APP_KEY=' .env.dev 2>/dev/null | head -1 | cut -d= -f2- | tr -d '\r'); \
	if [ -z "$$KEY_VAL" ]; then \
		echo "🔑 Generating APP_KEY (go run . --env=.env.dev artisan key:generate)..."; \
		go run . --env=.env.dev artisan key:generate || exit 1; \
	fi
endef

# If JWT_SECRET is missing or empty, run artisan jwt:secret (required for /v1/auth/login JWT)
define ensure_jwt_secret_dev
	@if ! grep -q '^JWT_SECRET=' .env.dev 2>/dev/null; then \
		echo "JWT_SECRET=" >> .env.dev; \
	fi
	@JWT_VAL=$$(grep '^JWT_SECRET=' .env.dev 2>/dev/null | head -1 | cut -d= -f2- | tr -d '\r'); \
	if [ -z "$$JWT_VAL" ]; then \
		echo "🔐 Generating JWT_SECRET (go run . --env=.env.dev artisan jwt:secret)..."; \
		go run . --env=.env.dev artisan jwt:secret || exit 1; \
	fi
endef

# =============================================================================
# Help
# =============================================================================

help: ## Show this help message
	@echo "╔══════════════════════════════════════════════════════════════╗"
	@echo "║              Macro Wallets (WaaS) - Makefile                ║"
	@echo "╚══════════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "📦 Build Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^build/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🚀 Deployment Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^deploy|^delete|^validate|^check-account|^stack-outputs|^server-credentials|^verify-aws/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🧪 Testing Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^test/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🐳 Docker Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^docker/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "💾 Database Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^migrate|^db-/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "📊 Monitoring Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^logs|^dlq|^ping/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🔧 Utility Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^lint|^fmt|^vet|^security|^mocks|^env-info|^clean/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🖥️  Local Dev Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^run|^dev|^stop|^air-install|^key-generate|^jwt-secret/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "💡 Examples:"
	@echo "  make dev                    # Start full stack (back + front)"
	@echo "  make dev-back               # Start backend only with Air"
	@echo "  make dev-front              # Start frontend only (vinext)"
	@echo "  make migrate-fresh-seed     # artisan migrate:fresh --seed"
	@echo "  make run                    # Start API without live reload"
	@echo "  make stop                   # Stop all dev processes"
	@echo "  make docker-up              # Start local development environment"
	@echo "  make test-coverage          # Run tests with coverage report"
	@echo "  make deploy ENV=dev         # AWS resources of dev (template.dev.yaml)"
	@echo "  make server-credentials     # Install the dev host's AWS profile on SERVER_SSH"
	@echo "  make verify-aws             # Prove queues, KMS key and Secrets Manager from the host"
	@echo "  make deploy ENV=prod        # Deploy to production"
	@echo ""

# =============================================================================
# Build Commands
# =============================================================================

build: ## Build Lambda function for deployment
	@echo "🔨 Building Lambda function..."
	sam build
	@echo "✅ Build complete"

build-lambda: ## Build Lambda binary for arm64
	@echo "🔨 Building Lambda binary for arm64..."
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/bootstrap main.go
	cd dist && zip -q ../function.zip bootstrap
	@echo "✅ Lambda binary built: function.zip"

# The waas-localstack image has no Go: its Secrets Manager snapshot hooks run this
# static binary (gitignored, rebuilt before every docker-up / dev). The bin directory is
# bind-mounted, so a rebuild takes effect on the next container restart, without recreation.
LOCALSTACK_HOOK_BIN = docker/localstack/bin/secrets-snapshot
LOCALSTACK_HOOK_ARCH ?= $(shell go env GOARCH)

localstack-hooks: ## Build the static LocalStack secrets-snapshot binary (linux, CGO off)
	@mkdir -p $(dir $(LOCALSTACK_HOOK_BIN))
	@CGO_ENABLED=0 GOOS=linux GOARCH=$(LOCALSTACK_HOOK_ARCH) go build -trimpath -ldflags="-s -w" -o $(LOCALSTACK_HOOK_BIN) ./tools/localstack-secrets-snapshot
	@echo "✅ $(LOCALSTACK_HOOK_BIN) built for linux/$(LOCALSTACK_HOOK_ARCH)"

# e2e helpers (capture-env, api, preflight, send-from-base, consolidate). Recordings run this
# prebuilt binary so they never compile the working tree mid-run.
MACRO_E2E_STATE_DIR ?= $(HOME)/.local/state/macro-e2e
MACRO_E2E_BIN = $(MACRO_E2E_STATE_DIR)/bin/macro-e2e

e2e-tools: ## Build the macro-e2e helper into <e2e state dir>/bin (0700)
	@mkdir -p -m 700 $(dir $(MACRO_E2E_BIN))
	@go build -trimpath -o $(MACRO_E2E_BIN).new ./tools/macro-e2e
	@chmod 700 $(MACRO_E2E_BIN).new && mv -f $(MACRO_E2E_BIN).new $(MACRO_E2E_BIN)
	@echo "✅ $(MACRO_E2E_BIN) built"

clean: ## Clean build artifacts
	@echo "🧹 Cleaning build artifacts..."
	rm -rf .aws-sam/ tmp/ dist/
	rm -f function.zip
	go clean -testcache
	@echo "✅ Clean complete"

# =============================================================================
# Deployment Commands
# =============================================================================

# The guard that makes a wrong ~/.aws profile harmless: whatever credentials aws
# resolved must belong to the account .env.$(ENV) names in AWS_ACCOUNT_ID.
check-account: ## Refuse unless the AWS credentials belong to AWS_ACCOUNT_ID of .env.$(ENV)
	@test -n "$(AWS_DECLARED_ACCOUNT_ID)" || { echo "check-account: AWS_ACCOUNT_ID is empty in .env.$(ENV)" >&2; exit 1; }
	@actual="$$($(AWS_ENV) aws sts get-caller-identity --query Account --output text 2>&1)" || \
		{ echo "check-account: aws sts get-caller-identity failed: $$actual" >&2; exit 1; }; \
	if [ "$$actual" != "$(AWS_DECLARED_ACCOUNT_ID)" ]; then \
		echo "check-account: credentials resolve to account $$actual, but .env.$(ENV) targets $(AWS_DECLARED_ACCOUNT_ID) — refusing to touch it" >&2; \
		exit 1; \
	fi; \
	echo "check-account: OK — account $$actual, region $(AWS_REGION), stack $(STACK_NAME)"

ifeq ($(ENV),dev)
DEPLOY_PREREQUISITES = check-account validate
else
DEPLOY_PREREQUISITES = validate build check-account
endif

deploy: $(DEPLOY_PREREQUISITES) ## Deploy ENV: dev applies template.dev.yaml (queues, KMS key, server user); staging/prod deploy template.yaml with SAM
	@echo "🚀 Deploying $(TEMPLATE_FILE) to $(ENV) (stack $(STACK_NAME))..."
ifeq ($(ENV),dev)
	$(AWS_ENV) aws cloudformation deploy \
		--stack-name $(STACK_NAME) \
		--template-file $(TEMPLATE_FILE) \
		--capabilities CAPABILITY_NAMED_IAM \
		--parameter-overrides Environment=$(ENV) \
		--tags Project=macro-wallets Environment=$(ENV) \
		--no-fail-on-empty-changeset
	@$(AWS_ENV) aws cloudformation describe-stacks --stack-name $(STACK_NAME) \
		--query "Stacks[0].Outputs[].[OutputKey,OutputValue]" --output table
else
	$(AWS_ENV) sam deploy \
		--stack-name $(STACK_NAME) \
		--parameter-overrides "$(PARAMETER_OVERRIDES)" \
		--capabilities CAPABILITY_IAM \
		$(S3_FLAG) \
		--no-confirm-changeset \
		--no-fail-on-empty-changeset
endif
	@echo "✅ Deployment complete"

require-sam-env:
	@test "$(ENV)" != dev || { echo "this target deploys the Lambda application (template.yaml); dev deploys template.dev.yaml with make deploy ENV=dev" >&2; exit 1; }

deploy-guided: require-sam-env build ## Deploy with guided prompts (first time setup, staging/prod)
	@echo "🚀 Running guided deployment..."
	sam deploy --guided \
		--stack-name $(STACK_NAME) \
		--parameter-overrides "$(PARAMETER_OVERRIDES)" \
		--capabilities CAPABILITY_IAM \
		$(S3_FLAG)

delete: check-account ## Delete ENV's CloudFormation stack (dev keeps its KMS key: DeletionPolicy Retain)
	@echo "⚠️  Deleting stack: $(STACK_NAME)"
	$(AWS_ENV) sam delete --stack-name $(STACK_NAME) --region $(AWS_REGION) --no-prompts
	@echo "✅ Stack deleted"

# --lint runs cfn-lint locally: plain sam validate accepted the sub-minute rates
# EventBridge refuses at deploy time.
validate: ## Validate ENV's template with SAM and cfn-lint (template.dev.yaml for dev, template.yaml otherwise)
	sam validate --lint -t $(TEMPLATE_FILE)

stack-outputs: ## Print ENV's stack outputs (queue URLs, MasterKeyArn for MASTER_KEY_REF, ...)
	@$(AWS_ENV) aws cloudformation describe-stacks --stack-name $(STACK_NAME) \
		--query "Stacks[0].Outputs[].[OutputKey,OutputValue]" --output table

require-server-ssh:
	@test -n "$(SERVER_SSH)" || { echo "SERVER_SSH is empty in .env.$(ENV): the host that runs the service, e.g. forge@203.0.113.10" >&2; exit 1; }

# Copies a repo script to SERVER_SSH and runs it there with the given arguments; stdin
# is passed through, and the copy is removed whatever the outcome.
# $(1) local script, $(2) environment assignments, $(3) arguments.
run-on-server = remote="$$(ssh $(SERVER_SSH) 'f=$$(mktemp); cat > "$$f"; echo "$$f"' < $(1))" && ssh $(SERVER_SSH) "$(2) bash $$remote $(3); rc=\$$?; rm -f $$remote; exit \$$rc"

# The access key never shows: Secrets Manager -> pipe -> ssh -> aws configure import.
server-credentials: require-server-ssh check-account ## Install the stack's server user on SERVER_SSH as AWS profile $(AWS_RUNTIME_PROFILE)
	@secret="$$($(AWS_ENV) aws cloudformation describe-stacks --stack-name $(STACK_NAME) \
		--query "Stacks[0].Outputs[?OutputKey=='ServerAccessKeySecretArn'].OutputValue" --output text)"; \
	if [ -z "$$secret" ] || [ "$$secret" = "None" ]; then \
		echo "server-credentials: $(STACK_NAME) has no server user (CreateServerUser=false?)" >&2; exit 1; \
	fi; \
	$(AWS_ENV) aws secretsmanager get-secret-value --secret-id "$$secret" --query SecretString --output text | \
		{ $(call run-on-server,scripts/aws-install-profile.sh,,$(AWS_RUNTIME_PROFILE) $(AWS_REGION)); }

verify-aws: require-server-ssh ## On SERVER_SSH, as profile $(AWS_RUNTIME_PROFILE): queues, DLQ redrive, KMS key and Secrets Manager round trips
	@$(call run-on-server,scripts/aws-verify.sh,AWS_PROFILE=$(AWS_RUNTIME_PROFILE) AWS_REGION=$(AWS_REGION),$(ENV))

# =============================================================================
# Local Development Commands
# =============================================================================

run: ## Run API server locally (go run)
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	$(call ensure_jwt_secret_dev)
	@echo "🚀 Starting local API server..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run .

dev: localstack-hooks ## Start full stack: Docker + backend (Air) + frontend (vinext)
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	$(call ensure_jwt_secret_dev)
	@echo "══════════════════════════════════════════════════════════"
	@echo "  Starting Macro Wallets — full stack"
	@echo "══════════════════════════════════════════════════════════"
	@if [ ! -d "$(FRONT_DIR)/node_modules" ]; then \
		echo "📦 Installing frontend dependencies..."; \
		cd $(FRONT_DIR) && npm install; \
	fi
	@API_PORT=$$(grep -E '^PORT=' .env.dev 2>/dev/null | cut -d= -f2 | tr -d '\r' | tr -d ' '); \
	API_PORT=$${API_PORT:-2002}; \
	echo "🔥 Starting backend API (port $$API_PORT)..."; \
	if [ -f "$$(go env GOPATH)/bin/air" ]; then \
		export $$(grep -v '^#' .env.dev | xargs) && $$(go env GOPATH)/bin/air & \
	else \
		echo "ℹ️  Air not found — falling back to go run (no live reload)"; \
		echo "   Install Air with: make air-install"; \
		export $$(grep -v '^#' .env.dev | xargs) && go run . & \
	fi; \
	echo "⏳ Waiting for http://127.0.0.1:$$API_PORT/health ..."; \
	for i in $$(seq 1 60); do \
		curl -sf "http://127.0.0.1:$$API_PORT/health" >/dev/null 2>&1 && break; \
		sleep 1; \
	done; \
	if ! curl -sf "http://127.0.0.1:$$API_PORT/health" >/dev/null 2>&1; then \
		echo "❌ API did not become ready in time. Is port $$API_PORT free? Try: make stop"; \
		exit 1; \
	fi; \
	echo "✅ API ready"; \
	echo "🌐 Starting frontend (vinext on port 2001) — API proxies to $$API_PORT"; \
	cd $(FRONT_DIR) && npm run dev

dev-back: ## Start backend only with live reload (Air)
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	$(call ensure_jwt_secret_dev)
	@echo "🔥 Starting backend API with live reload (Air)..."
	@if [ ! -f "$$(go env GOPATH)/bin/air" ]; then \
		echo "❌ Air not found. Install with: make air-install"; \
		exit 1; \
	fi
	@export $$(grep -v '^#' .env.dev | xargs) && $$(go env GOPATH)/bin/air

dev-front: ## Start frontend only (vinext on port 2001)
	@echo "🌐 Starting frontend (vinext on port 2001)..."
	@if [ ! -d "$(FRONT_DIR)/node_modules" ]; then \
		echo "📦 Installing frontend dependencies..."; \
		cd $(FRONT_DIR) && npm install; \
	fi
	@cd $(FRONT_DIR) && npm run dev

stop: ## Stop all running dev processes (backend + frontend)
	@echo "🛑 Stopping all dev processes..."
	@pkill -x air 2>/dev/null && echo "  Stopped Air process" || true
	@API_PORT=$$(grep -E '^PORT=' .env.dev 2>/dev/null | cut -d= -f2); \
	lsof -ti:$${API_PORT:-2002} | xargs kill -9 2>/dev/null && echo "  Killed process on port $${API_PORT:-2002}" || true
	@lsof -ti:2001 | xargs kill -9 2>/dev/null && echo "  Killed process on port 2001" || true
	@echo "✅ All dev processes stopped"

air-install: ## Install Air live-reload tool
	@echo "📦 Installing Air..."
	go install github.com/air-verse/air@latest
	@echo "✅ Air installed"

local: build ## Run API locally with SAM (uses warm containers)
	@echo "🔥 Starting local API with warm containers..."
	sam local start-api --warm-containers EAGER --env-vars env.json

invoke-scanner: ## Invoke deposit scanner locally
	@echo "🔍 Invoking deposit scanner (ETH)..."
	sam local invoke DepositScannerFunction -e '{"chain": "eth"}'

invoke-scanner-remote: ## Invoke deposit scanner on AWS
	@echo "🔍 Invoking remote deposit scanner (ETH)..."
	aws lambda invoke --function-name waas-deposit-scanner-$(ENV) \
		--payload '{"chain": "eth"}' /dev/stdout

# =============================================================================
# Testing Commands
# =============================================================================

test: ## Run all tests: test-unit, then test-integration (both always run)
	@echo "🧪 Running tests..."
	@$(MAKE) --no-print-directory test-unit; unit=$$?; \
		$(MAKE) --no-print-directory test-integration; integration=$$?; \
		echo "🧪 unit: exit $$unit, integration: exit $$integration"; \
		[ $$unit -eq 0 ] && [ $$integration -eq 0 ]

test-unit: ## Unit tests: packages without PostgreSQL/Redis, all in parallel, no lock; architecture runs in ratchet mode
	@echo "🧪 Running unit tests..."
	@set -a; [ ! -f .env.dev ] || . ./.env.dev; . ./.env.testing; set +a; \
		ARCH_MODE=ratchet go test -count=1 -timeout $(TEST_TIMEOUT) $(TEST_FLAGS) $(UNIT_TEST_PACKAGES)

test-integration: ## Integration tests (PostgreSQL/Redis): one cloned database per worker, -p TEST_PARALLEL
	@echo "🔗 Running integration tests ($(TEST_PARALLEL) workers)..."
	$(call run_with_test_databases,$(TEST_FLAGS) $(INTEGRATION_TEST_PACKAGES))

arch: ## Architecture checks, every finding listed (ARCH_MODE=ratchet|enforce to fail)
	$(call ensure_test_database)
	@set -a; [ ! -f .env.dev ] || . ./.env.dev; . ./.env.testing; set +a; \
		DB_DATABASE=$(TEST_DB_DATABASE) ARCH_VERBOSE=1 go test ./tests/architecture/... -v -count=1

arch-baseline: ## Rewrite tests/architecture/testdata/baseline from the current findings
	$(call ensure_test_database)
	@set -a; [ ! -f .env.dev ] || . ./.env.dev; . ./.env.testing; set +a; \
		DB_DATABASE=$(TEST_DB_DATABASE) go test ./tests/architecture/... -count=1 -args -update-baseline

contract: ## Compare the HTTP contract snapshot (tests/contract/testdata/http_contract.txt)
	$(call ensure_test_database)
	@set -a; [ ! -f .env.dev ] || . ./.env.dev; . ./.env.testing; set +a; \
		DB_DATABASE=$(TEST_DB_DATABASE) TEST_DB_REQUIRED=1 go test ./tests/contract/ -run TestContract_HTTP_Contract -v -count=1

contract-update: ## Rewrite the HTTP contract snapshot (only for a decided contract change)
	$(call ensure_test_database)
	@set -a; [ ! -f .env.dev ] || . ./.env.dev; . ./.env.testing; set +a; \
		DB_DATABASE=$(TEST_DB_DATABASE) TEST_DB_REQUIRED=1 go test ./tests/contract/ -run TestContract_HTTP_Contract -count=1 -args -update-contract

test-coverage: ## Run tests with coverage report
	@echo "📊 Running tests with coverage..."
	$(call run_with_test_databases,-coverprofile=coverage.out ./...)
	go tool cover -html=coverage.out
	@echo "✅ Coverage report generated: coverage.out"

test-race: ## Run tests with race detector
	@echo "🏁 Running tests with race detector..."
	@$(MAKE) --no-print-directory test TEST_FLAGS="$(TEST_FLAGS) -race"

test-verbose: ## Run tests with verbose output
	@echo "🔍 Running tests (verbose)..."
	@$(MAKE) --no-print-directory test TEST_FLAGS="$(TEST_FLAGS) -v"

# =============================================================================
# Code Quality Commands
# =============================================================================

mocks: ## Regenerate tests/mocks with mockery v2 (.mockery.yaml)
	@echo "🧪 Generating mocks with mockery $(MOCKERY_VERSION) (GOTOOLCHAIN=$(GO_TOOLCHAIN))..."
	GOTOOLCHAIN=$(GO_TOOLCHAIN) go run github.com/vektra/mockery/v2@$(MOCKERY_VERSION) --config .mockery.yaml
	@echo "✅ Mocks written to tests/mocks (do not edit generated files by hand)"

lint: ## Run golangci-lint (.golangci.yml, report mode)
	@echo "🔍 Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "⚠️  golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
	fi

fmt: ## Format Go code
	@echo "✨ Formatting code..."
	go fmt ./...
	@echo "✅ Code formatted"

vet: ## Run go vet
	@echo "🔍 Running go vet..."
	go vet ./...
	@echo "✅ Vet complete"

security: ## Run security scan with gosec
	@echo "🔒 Running security scan..."
	@if command -v gosec >/dev/null 2>&1; then \
		gosec ./...; \
	else \
		echo "⚠️  gosec not installed. Install with: go install github.com/securego/gosec/v2/cmd/gosec@latest"; \
	fi

# =============================================================================
# Docker Commands (Local Development)
# =============================================================================

docker-up: localstack-hooks ## Start local development environment (PostgreSQL, Redis, Mailpit, LocalStack)
	@echo "🐳 Starting local development environment..."
	$(DOCKER_COMPOSE) up -d
	@echo "⏳ Waiting for PostgreSQL to accept connections..."
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
		docker exec waas-postgres pg_isready -U vault -d vault >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	@echo "✅ Development environment ready!"
	@echo "   PostgreSQL: localhost:$(or $(POSTGRES_PORT),5432)"
	@echo "   Redis:      localhost:$(or $(REDIS_PORT),6379)"

docker-down: ## Stop local development environment
	@echo "🛑 Stopping local development environment..."
	$(DOCKER_COMPOSE) down
	@echo "✅ Environment stopped"

docker-down-volumes: ## Stop and remove volumes (WARNING: deletes data)
	@echo "⚠️  Stopping environment and removing volumes..."
	$(DOCKER_COMPOSE) down -v
	@echo "✅ Environment stopped and volumes removed"

docker-logs: ## View logs from all containers
	$(DOCKER_COMPOSE) logs -f

docker-logs-postgres: ## View PostgreSQL logs
	$(DOCKER_COMPOSE) logs -f postgres

docker-logs-redis: ## View Redis logs
	$(DOCKER_COMPOSE) logs -f redis

docker-build: ## Build Docker image for waas service
	@echo "🐳 Building Docker image: $(DOCKER_IMAGE_NAME):$(DOCKER_TAG)..."
	docker build -t $(DOCKER_IMAGE_NAME):$(DOCKER_TAG) .
	@echo "✅ Image built: $(DOCKER_IMAGE_NAME):$(DOCKER_TAG)"

docker-test: docker-build ## Build and run Docker container for testing
	@echo "🧪 Running Docker container for testing..."
	docker run --rm \
		-p 8080:8080 \
		--env-file .env.dev \
		-e DATABASE_URL=postgres://vault:vault@host.docker.internal:5432/vault?sslmode=disable \
		-e REDIS_HOST=host.docker.internal \
		$(DOCKER_IMAGE_NAME):$(DOCKER_TAG)

docker-shell: ## Open shell in PostgreSQL container
	@echo "🐚 Opening PostgreSQL shell..."
	$(DOCKER_COMPOSE) exec postgres psql -U vault -d vault

docker-redis-cli: ## Open Redis CLI
	@echo "🐚 Opening Redis CLI..."
	$(DOCKER_COMPOSE) exec redis redis-cli

docker-status: ## Show status of all waas containers
	@docker ps -a --filter name=waas --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# =============================================================================
# Database Commands
# =============================================================================

db-reset: docker-down-volumes docker-up migrate-fresh-seed ## Reset DB, LocalStack secrets, and seed data (WARNING: deletes all data)
	@echo "✅ Database and Secrets Manager reset together"

key-generate: ## Generate APP_KEY in .env.dev (go run . --env=.env.dev artisan key:generate)
	$(call ensure_env_dev)
	@echo "🔑 Running artisan key:generate on .env.dev..."
	@go run . --env=.env.dev artisan key:generate

jwt-secret: ## Generate JWT_SECRET in .env.dev (go run . --env=.env.dev artisan jwt:secret)
	$(call ensure_env_dev)
	@if ! grep -q '^JWT_SECRET=' .env.dev 2>/dev/null; then echo "JWT_SECRET=" >> .env.dev; fi
	@echo "🔐 Running artisan jwt:secret on .env.dev..."
	@go run . --env=.env.dev artisan jwt:secret

db-seed: ## Seed database (artisan db:seed — seeded users use admin@macro.markets / secret)
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "🌱 Seeding database (go run . artisan db:seed)..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan db:seed

# =============================================================================
# ECR Commands
# =============================================================================

ecr-login: ## Login to AWS ECR
	@echo "🔐 Logging into AWS ECR..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_REGISTRY)
	@echo "✅ Logged in to ECR"

ecr-create-repo: ## Create ECR repository
	@echo "📦 Creating ECR repository: $(DOCKER_IMAGE_NAME)..."
	@aws ecr describe-repositories --repository-names $(DOCKER_IMAGE_NAME) 2>/dev/null || \
	aws ecr create-repository \
		--repository-name $(DOCKER_IMAGE_NAME) \
		--image-scanning-configuration scanOnPush=true \
		--encryption-configuration encryptionType=AES256
	@echo "✅ ECR repository ready"

ecr-push: docker-build ecr-login ## Build and push Docker image to ECR
	@echo "📤 Pushing image to ECR..."
	docker tag $(DOCKER_IMAGE_NAME):$(DOCKER_TAG) $(ECR_IMAGE)
	docker push $(ECR_IMAGE)
	@echo "✅ Image pushed: $(ECR_IMAGE)"

# =============================================================================
# Monitoring Commands
# =============================================================================

logs-api: ## Tail API Lambda logs
	sam logs -n ApiFunction --stack-name $(STACK_NAME) --tail

logs-scanner: ## Tail deposit scanner logs
	sam logs -n DepositScannerFunction --stack-name $(STACK_NAME) --tail

logs-webhook: ## Tail webhook worker logs
	sam logs -n WebhookWorkerFunction --stack-name $(STACK_NAME) --tail

dlq-check: ## Check DLQ message counts
	@echo "═══════════════════════════════════════"
	@echo "📊 Checking Dead Letter Queues"
	@echo "═══════════════════════════════════════"
	@echo "Webhook DLQ:"
	@$(AWS_ENV) aws sqs get-queue-attributes \
		--queue-url $$($(AWS_ENV) aws sqs get-queue-url --queue-name vault-webhooks-dlq-$(ENV) --query QueueUrl --output text) \
		--attribute-names ApproximateNumberOfMessages \
		--query 'Attributes.ApproximateNumberOfMessages' --output text || echo "0"
	@echo ""
	@echo "Withdrawal DLQ:"
	@$(AWS_ENV) aws sqs get-queue-attributes \
		--queue-url $$($(AWS_ENV) aws sqs get-queue-url --queue-name vault-withdrawals-dlq-$(ENV) --query QueueUrl --output text) \
		--attribute-names ApproximateNumberOfMessages \
		--query 'Attributes.ApproximateNumberOfMessages' --output text || echo "0"
	@echo "═══════════════════════════════════════"

dlq-replay-webhooks: ## Replay webhook DLQ messages back to main queue
	@echo "♻️  Replaying webhook DLQ messages..."
	$(AWS_ENV) aws sqs start-message-move-task \
		--source-arn $$($(AWS_ENV) aws sqs get-queue-attributes --queue-url $$($(AWS_ENV) aws sqs get-queue-url --queue-name vault-webhooks-dlq-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text) \
		--destination-arn $$($(AWS_ENV) aws sqs get-queue-attributes --queue-url $$($(AWS_ENV) aws sqs get-queue-url --queue-name vault-webhooks-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text)
	@echo "✅ Replay initiated"

dlq-replay-withdrawals: ## Replay withdrawal DLQ messages back to main queue
	@echo "♻️  Replaying withdrawal DLQ messages..."
	$(AWS_ENV) aws sqs start-message-move-task \
		--source-arn $$($(AWS_ENV) aws sqs get-queue-attributes --queue-url $$($(AWS_ENV) aws sqs get-queue-url --queue-name vault-withdrawals-dlq-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text) \
		--destination-arn $$($(AWS_ENV) aws sqs get-queue-attributes --queue-url $$($(AWS_ENV) aws sqs get-queue-url --queue-name vault-withdrawals-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text)
	@echo "✅ Replay initiated"

ping: ## Check API health endpoint
	@echo "🏓 Pinging API health endpoint..."
	@curl -s $$(aws cloudformation describe-stacks --stack-name $(STACK_NAME) \
		--query 'Stacks[0].Outputs[?OutputKey==`ApiEndpoint`].OutputValue' --output text)health | jq .

# =============================================================================
# Utility Commands
# =============================================================================

env-info: ## Display current environment configuration
	@echo "╔══════════════════════════════════════════════════════════════╗"
	@echo "║              Environment Configuration                       ║"
	@echo "╚══════════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Environment:        $(ENV)"
	@echo "Stack Name:         $(STACK_NAME)"
	@echo "AWS Region:         $(AWS_REGION)"
	@echo "AWS Account:        $(AWS_ACCOUNT_ID)"
	@echo ""
	@echo "Environment Variables:"
	@echo "  ENVIRONMENT       = $(ENVIRONMENT)"
	@echo "  DATABASE_URL      = $(DATABASE_URL)"
	@echo "  REDIS_HOST        = $(REDIS_HOST)"
	@echo "  ETH_RPC_URL       = $(ETH_RPC_URL)"
	@echo "  POLYGON_RPC_URL   = $(POLYGON_RPC_URL)"
	@echo "  SOLANA_RPC_URL    = $(SOLANA_RPC_URL)"
	@echo "  BTC_RPC_URL       = $(BTC_RPC_URL)"
	@echo ""

deps-install: ## Install development dependencies
	@echo "📦 Installing development dependencies..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	@echo "✅ Dependencies installed"

deps-update: ## Update Go dependencies
	@echo "🔄 Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✅ Dependencies updated"

swagger-install: ## Install Swagger CLI tool
	@echo "📦 Installing Swagger CLI..."
	go install github.com/swaggo/swag/cmd/swag@v1.8.12
	@echo "✅ Swagger CLI installed"

swagger-generate: ## Generate Swagger documentation
	@echo "📝 Generating Swagger documentation..."
	swag init -g main.go --output ./docs --parseDependency
	@echo "✅ Swagger docs generated in ./docs"
	@echo "   View at: http://localhost:$(or $(PORT),2002)/swagger/index.html"

swagger-fmt: ## Format Swagger comments
	@echo "✨ Formatting Swagger comments..."
	swag fmt
	@echo "✅ Swagger comments formatted"

# =============================================================================
# Database Migrations (Goravel)
# =============================================================================

migrate: ## Run pending database migrations
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "🔄 Running database migrations..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate

migrate-status: ## Show migration status
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "📊 Checking migration status..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:status

migrate-rollback: ## Rollback last migration batch
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "⏪ Rolling back migrations..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:rollback

migrate-fresh: ## Drop all tables and re-run migrations
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "🆕 Dropping tables and running fresh migrations..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:fresh

migrate-fresh-seed: ## artisan migrate:fresh --seed (drop, migrate, db:seed)
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "🆕 migrate:fresh --seed..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:fresh --seed
	@echo "✅ migrate:fresh --seed complete"

migrate-fresh-hard: ## DEV ONLY: drop schema public (tables + enums + domains), migrate, seed
	$(call ensure_docker)
	$(call ensure_env_dev)
	$(call ensure_app_key_dev)
	@echo "💣 Dropping schema public (nuclear reset — tables + custom types)..."
	@docker exec waas-postgres psql -U vault -d vault -v ON_ERROR_STOP=1 -c "\
		DROP SCHEMA public CASCADE; \
		CREATE SCHEMA public; \
		GRANT ALL ON SCHEMA public TO vault; \
		GRANT ALL ON SCHEMA public TO public;"
	@echo "🔄 Running migrations on clean schema..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate
	@echo "🌱 Seeding database..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan db:seed
	@echo "✅ migrate-fresh-hard complete"

.DEFAULT_GOAL := help

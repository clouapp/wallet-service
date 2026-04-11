package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/bootstrap"
	_ "github.com/macrowallets/waas/docs" // Import generated swagger docs
	"github.com/macrowallets/waas/pkg/types"
)

// @title           Vault Custody Service API
// @version         1.0
// @description     Multi-chain cryptocurrency custody service with deposit scanning, withdrawals, and webhooks
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.email  support@vault.dev

// @license.name  MIT
// @license.url   https://opensource.org/licenses/MIT

// @host      localhost:8080
// @BasePath  /

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key

// @securityDefinitions.apikey SignatureAuth
// @in header
// @name X-API-Signature

// @tag.name Chains
// @tag.description Operations about blockchain networks

// @tag.name Wallets
// @tag.description Wallet management operations

// @tag.name Addresses
// @tag.description Address generation and lookup

// @tag.name Withdrawals
// @tag.description Withdrawal request operations

// @tag.name Transactions
// @tag.description Transaction history and details

// @tag.name Webhooks
// @tag.description Webhook configuration for event notifications

var (
	c *container.Container
)

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	bootstrap.Boot()
	c = container.Get()

	mode := facades.Config().GetString("vault.lambda_mode")
	envName := facades.Config().GetString("app.env")
	slog.Info("vault booted", "mode", mode, "env", envName)
}

func main() {
	if len(os.Args) > 1 {
		if err := facades.Artisan().Run(os.Args, true); err != nil {
			slog.Error("artisan command failed", "error", err)
			os.Exit(1)
		}
		return
	}

	mode := facades.Config().GetString("vault.lambda_mode")

	switch mode {
	case "deposit_scanner":
		lambda.Start(handleDepositScan)
	case "confirmation_tracker":
		lambda.Start(handleConfirmationTracker)
	case "webhook_reconciler":
		lambda.Start(handleWebhookReconciler)
	case "webhook_worker":
		lambda.Start(handleWebhookWorker)
	case "api":
		lambda.Start(handleAPIGateway)
	default:
		runLocal()
	}
}

func handleAPIGateway(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return httpadapter.NewV2(facades.Route()).ProxyWithContext(ctx, req)
}

func handleDepositScan(ctx context.Context, event types.DepositScanEvent) error {
	slog.Info("deposit scan triggered", "chain", event.Chain)
	return c.DepositService.ScanLatestBlocks(ctx, event.Chain)
}

func handleConfirmationTracker(ctx context.Context) error {
	slog.Info("confirmation tracker triggered")
	return c.DepositService.RunConfirmationCheck(ctx)
}

func handleWebhookReconciler(ctx context.Context) error {
	slog.Info("webhook reconciler triggered")
	return c.WebhookSyncService.RunReconciliation(ctx)
}

func handleWebhookWorker(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure

	for _, record := range sqsEvent.Records {
		var msg types.WebhookMessage
		if err := json.Unmarshal([]byte(record.Body), &msg); err != nil {
			slog.Error("unmarshal webhook message", "error", err, "message_id", record.MessageId)
			failures = append(failures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
			continue
		}

		if err := c.WebhookService.Deliver(ctx, msg); err != nil {
			slog.Error("webhook delivery failed", "error", err, "event_id", msg.EventID)
			failures = append(failures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
		}
	}

	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

func runLocal() {
	port := facades.Config().GetString("vault.port")
	if port == "" {
		port = "8080"
	}

	slog.Info("starting Goravel HTTP server", "port", port)

	if err := facades.Route().Run(":" + port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

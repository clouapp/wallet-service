package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/macromarkets/vault/app/http/controllers"
	"github.com/macromarkets/vault/app/http/middleware"
	"github.com/macromarkets/vault/app/providers"
	_ "github.com/macromarkets/vault/docs" // Import generated swagger docs
	"github.com/macromarkets/vault/pkg/types"
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
// @BasePath  /v1

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
	ginLambda *ginadapter.GinLambdaV2
	container *providers.Container
)

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Boot application — shared across all Lambda modes
	container = providers.Boot()
	slog.Info("vault booted", "mode", os.Getenv("LAMBDA_MODE"), "env", os.Getenv("ENV"))
}

func main() {
	mode := os.Getenv("LAMBDA_MODE")

	switch mode {
	case "deposit_scanner":
		lambda.Start(handleDepositScan)
	case "webhook_worker":
		lambda.Start(handleWebhookWorker)
	case "withdrawal_worker":
		lambda.Start(handleWithdrawalWorker)
	default:
		// API mode — Goravel/Gin HTTP kernel behind API Gateway
		ginLambda = ginadapter.NewV2(setupRouter())
		lambda.Start(handleAPIGateway)
	}
}

// ---------------------------------------------------------------------------
// API Gateway Handler
// ---------------------------------------------------------------------------

func handleAPIGateway(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

func setupRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogger())

	// Health — no auth
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "0.1.0"})
	})

	// Swagger documentation — no auth
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// API v1 — authenticated
	v1 := r.Group("/v1")
	v1.Use(middleware.HMACAuth(os.Getenv("API_KEY_SECRET")))

	ctrl := controllers.New(container)
	ctrl.RegisterRoutes(v1)

	return r
}

// ---------------------------------------------------------------------------
// Deposit Scanner — triggered by EventBridge schedule
// ---------------------------------------------------------------------------

func handleDepositScan(ctx context.Context, event types.DepositScanEvent) error {
	slog.Info("deposit scan triggered", "chain", event.Chain)
	return container.DepositService.ScanLatestBlocks(ctx, event.Chain)
}

// ---------------------------------------------------------------------------
// Webhook Worker — triggered by SQS
// ---------------------------------------------------------------------------

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

		if err := container.WebhookService.Deliver(ctx, msg); err != nil {
			slog.Error("webhook delivery failed", "error", err, "event_id", msg.EventID)
			failures = append(failures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
		}
	}

	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

// ---------------------------------------------------------------------------
// Withdrawal Worker — triggered by SQS
// ---------------------------------------------------------------------------

func handleWithdrawalWorker(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure

	for _, record := range sqsEvent.Records {
		var msg types.WithdrawalMessage
		if err := json.Unmarshal([]byte(record.Body), &msg); err != nil {
			slog.Error("unmarshal withdrawal message", "error", err, "message_id", record.MessageId)
			failures = append(failures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
			continue
		}

		if err := container.WithdrawalService.Execute(ctx, msg); err != nil {
			slog.Error("withdrawal execution failed", "error", err, "tx_id", msg.TransactionID)
			failures = append(failures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
		}
	}

	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

// ---------------------------------------------------------------------------
// Local dev: run as plain HTTP server
// ---------------------------------------------------------------------------

func runLocal() {
	r := setupRouter()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	slog.Info("starting local server", "port", port)
	if err := r.Run(":" + port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

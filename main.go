package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/localworkers"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/bootstrap"
	_ "github.com/macrowallets/waas/docs" // Import generated swagger docs
	"github.com/macrowallets/waas/pkg/lifecycle"
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
	deposits    *deposit.Service
	webhooks    *webhook.Service
	webhookSync *webhooksync.Service
	registry    *chainpkg.Registry
)

func init() {
	bootstrap.Boot()
	deposits = container.MustMake[*deposit.Service]()
	webhooks = container.MustMake[*webhook.Service]()
	webhookSync = container.MustMake[*webhooksync.Service]()
	registry = container.MustMake[*chainpkg.Registry]()

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

// handleDepositScan scans new blocks, then retries the chain's pending blocks whose
// backoff elapsed, in the same invocation, as the local scan loop does.
func handleDepositScan(ctx context.Context, event types.DepositScanEvent) error {
	slog.Info("deposit scan triggered", "chain", event.Chain)
	scanErr := deposits.ScanLatestBlocks(ctx, event.Chain)
	resolved, pendingErr := deposits.ReprocessDuePending(ctx, event.Chain)
	if resolved > 0 {
		slog.Info("pending deposit blocks recovered", "chain", event.Chain, "count", resolved)
	}
	return errors.Join(scanErr, pendingErr)
}

func handleConfirmationTracker(ctx context.Context) error {
	slog.Info("confirmation tracker triggered")
	return deposits.RunConfirmationCheck(ctx)
}

func handleWebhookReconciler(ctx context.Context) error {
	slog.Info("webhook reconciler triggered")
	return webhookSync.RunReconciliation(ctx)
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

		if err := webhooks.Deliver(ctx, msg); err != nil {
			slog.Error("webhook delivery failed", "error", err, "event_id", msg.EventID)
			failures = append(failures, events.SQSBatchItemFailure{
				ItemIdentifier: record.MessageId,
			})
		}
	}

	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

// startLocalWorkers stands in for the confirmation_tracker and webhook_worker
// Lambdas, which never run beside the local HTTP server, and keeps the wallet
// balance read model refreshed. It returns nil when no worker was started.
func startLocalWorkers(ctx context.Context) lifecycle.Workers {
	if !facades.Config().GetBool("vault.local_workers.enabled") {
		slog.Info("local workers disabled")
		return nil
	}
	cfg := localworkers.Config{
		ConfirmationInterval:   time.Duration(facades.Config().GetInt("vault.local_workers.confirmation_interval_seconds")) * time.Second,
		DeliveryInterval:       time.Duration(facades.Config().GetInt("vault.local_workers.delivery_interval_seconds")) * time.Second,
		DeliverOutbox:          facades.Config().GetString("vault.queues.webhook") == "",
		DepositScanChains:      localworkers.ParseChainList(facades.Config().GetString("vault.local_workers.deposit_scan_chains")),
		DepositScanInterval:    time.Duration(facades.Config().GetInt("vault.local_workers.deposit_scan_interval_seconds")) * time.Second,
		BalanceRefreshInterval: time.Duration(facades.Config().GetInt("vault.local_workers.balance_refresh_interval_seconds")) * time.Second,
	}
	for _, chainID := range cfg.DepositScanChains {
		if _, err := registry.Chain(chainID); err != nil {
			slog.Error("local workers not started: unknown deposit scan chain", "chain", chainID, "error", err)
			return nil
		}
	}
	loops, err := localworkers.Start(ctx, cfg, localworkers.Workers{
		Checker:   deposits,
		Deliverer: webhooks,
		Scanner:   deposits,
		Balances:  container.MustMake[*refresh.WalletRefresher](),
	})
	if err != nil {
		slog.Error("local workers not started", "error", err)
		return nil
	}
	return loops
}

const (
	defaultLocalPort        = "8080"
	exitCodeFailure         = 1
	exitCodeShutdownTimeout = 2
	// maxHeaderBytes is the gin driver's default header_limit (4096 KiB), which
	// its Listen applied before the server moved into pkg/lifecycle.
	maxHeaderBytes = 4096 << 10
)

// runLocal serves HTTP and runs the local workers until SIGINT or SIGTERM, then
// drains both within vault.shutdown_timeout_seconds.
func runLocal() {
	port := facades.Config().GetString("vault.port")
	if port == "" {
		port = defaultLocalPort
	}
	shutdownTimeout := time.Duration(facades.Config().GetInt("vault.shutdown_timeout_seconds")) * time.Second

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		slog.Error("server error", "error", err)
		os.Exit(exitCodeFailure)
	}
	// The hard request cut lives here, at the net/http level. In Lambda mode only
	// the cooperative middleware.RequestTimeout deadline applies.
	server, err := lifecycle.NewListenerServer(lifecycle.ListenerServerDeps{
		Router:         facades.Route(),
		Listener:       listener,
		Wrap:           bootstrap.RequestTimeoutHandler(),
		MaxHeaderBytes: maxHeaderBytes,
	})
	if err != nil {
		slog.Error("server error", "error", err)
		os.Exit(exitCodeFailure)
	}

	slog.Info("starting Goravel HTTP server", "port", port, "shutdown_timeout", shutdownTimeout.String())

	err = lifecycle.Run(context.Background(), lifecycle.Config{
		Server:          server,
		StartWorkers:    startLocalWorkers,
		ShutdownTimeout: shutdownTimeout,
	})
	switch {
	case errors.Is(err, lifecycle.ErrShutdownTimeout):
		slog.Error("local server exiting before a clean shutdown", "error", err)
		os.Exit(exitCodeShutdownTimeout)
	case err != nil:
		slog.Error("local server exiting with error", "error", err)
		os.Exit(exitCodeFailure)
	}
	slog.Info("local server exited cleanly")
}

package providers

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/goravel/framework/contracts/foundation"

	sweepsecrets "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/facades"
	mpc "github.com/macrowallets/waas/app/services/mpc"
)

// AppServiceProvider binds the process-wide clients the domain providers
// share: the AWS configuration, the Secrets Manager client built from it, and
// the MPC service. Boot loads the token and chain catalogs.
type AppServiceProvider struct{}

func (receiver *AppServiceProvider) Register(app foundation.Application) {
	app.Singleton((*aws.Config)(nil), func(foundation.Application) (any, error) {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("vault: aws config: %w", err)
		}
		return &cfg, nil
	})
	app.Singleton((*sweepsecrets.SDKClient)(nil), func(app foundation.Application) (any, error) {
		cfg, err := resolve[*aws.Config](app)
		if err != nil {
			return nil, err
		}
		return sweepsecrets.NewClient(*cfg, facades.Config().GetString("vault.aws.endpoint_url")), nil
	})
	app.Singleton((*mpc.TSSService)(nil), func(foundation.Application) (any, error) {
		return mpc.NewTSSService(), nil
	})
}

func (receiver *AppServiceProvider) Boot(app foundation.Application) {
	loadActiveTokens(app)
	refreshChainRegistry(app)
}

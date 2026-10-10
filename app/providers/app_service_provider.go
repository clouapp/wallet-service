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
// the MPC service with its keygen pre-parameters pool. Boot loads the token and chain catalogs.
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
	app.Singleton((*mpc.PreParamsPool)(nil), func(foundation.Application) (any, error) {
		return mpc.NewPreParamsPool(facades.Config().GetInt("vault.mpc.preparams_pool_size"), mpc.GeneratePreParams), nil
	})
	app.Singleton((*mpc.TSSService)(nil), func(app foundation.Application) (any, error) {
		pool, err := resolve[*mpc.PreParamsPool](app)
		if err != nil {
			return nil, err
		}
		return mpc.NewTSSServiceWithPreParams(pool), nil
	})
}

func (receiver *AppServiceProvider) Boot(app foundation.Application) {
	loadActiveTokens(app)
	refreshChainRegistry(app)
}

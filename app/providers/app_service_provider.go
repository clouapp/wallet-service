package providers

import (
	"github.com/goravel/framework/contracts/foundation"
)

type AppServiceProvider struct{}

func (receiver *AppServiceProvider) Register(app foundation.Application) {
	registerVaultContainer(app)
}

func (receiver *AppServiceProvider) Boot(app foundation.Application) {}

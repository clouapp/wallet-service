package requestctx

import (
	"context"
	"github.com/macrowallets/waas/app/models"
)

type RampCustomerKey struct{}
type RampActorKey struct{}

func RampCustomer(ctx context.Context) *models.RampCustomer {
	row, _ := ctx.Value(RampCustomerKey{}).(*models.RampCustomer)
	return row
}
func RampActor(ctx context.Context) *models.User {
	row, _ := ctx.Value(RampActorKey{}).(*models.User)
	return row
}

package ramps

import (
	"context"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
	"time"
)

type CustomerStore interface {
	Find(context.Context, uuid.UUID, uuid.UUID) (*models.RampCustomer, error)
	List(context.Context, uuid.UUID, int, int, string) ([]models.RampCustomer, int64, error)
	AccountHolder(context.Context, uuid.UUID) (*models.RampCustomer, error)
	FindByEngine(context.Context, uint) (*models.RampCustomer, error)
	ContactAndHolder(context.Context, *models.RampCustomer) (*em.User, *em.Customer, *em.CustomerMember, error)
	Create(context.Context, *models.Account, models.RampCustomerDraft) (*models.RampCustomer, bool, error)
	AuditActor(context.Context, uuid.UUID) (*em.Admin, error)
	FeatureSubject(context.Context, uint) (uuid.UUID, uuid.UUID, error)
	FeatureOverrides(context.Context, uuid.UUID) ([]models.RampFeature, error)
	SetFeature(context.Context, uuid.UUID, string, bool) error
}
type WalletStore interface {
	FindByID(context.Context, uuid.UUID) (*models.Wallet, error)
}
type AddressStore interface {
	FindByWalletID(context.Context, uuid.UUID) ([]models.Address, error)
}
type ChainStore interface {
	FindByID(context.Context, string) (*models.Chain, error)
}
type TaskStore interface {
	Enqueue(context.Context, uuid.UUID, string, string, time.Duration) error
	Claim(context.Context, int) ([]models.RampTask, error)
	Finish(context.Context, uuid.UUID, error) error
}
type StateStore interface {
	FundingIntentAbsent(context.Context, uuid.UUID) (bool, error)
	AppendOutbox(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string) error
	ListOutbox(context.Context, int) ([]models.RampOutboxEvent, error)
	DeliverOutbox(context.Context, uuid.UUID) error
	ReconciliationCustomers(context.Context, int) ([]models.RampReconciliationCustomer, error)
	TouchCustomer(context.Context, uuid.UUID) error
}

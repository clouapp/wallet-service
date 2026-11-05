package ramps

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	aud "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	ad "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/ramps"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
	audit "github.com/macrowallets/waas/packages/rampengine/app/services/admin/audit"
	admin "github.com/macrowallets/waas/packages/rampengine/app/services/admin/ramps"
)

func (s *Service) Attention(ctx context.Context, account *models.Account, customer *models.RampCustomer) ([]ad.AttentionItemDTO, error) {
	mode, err := ModeForAccount(account)
	if err != nil {
		return nil, err
	}
	queue := admin.NewAttentionService(er.NewTransactionRepositoryForHolder(account.ID.String(), mode, customer.EngineCustomerID), er.NewOfframpFundingAuthorizationRepositoryForHolder(account.ID.String(), mode, customer.EngineCustomerID), er.NewProviderKycSubmissionIntentRepositoryForHolder(account.ID.String(), mode, customer.EngineCustomerID), nil)
	view, err := queue.Queue(ad.AdminListAttentionDTO{Limit: 100})
	if err != nil {
		return nil, err
	}
	out := []ad.AttentionItemDTO{}
	for _, item := range view.Items {
		if item.CustomerID == customer.EngineCustomerID {
			out = append(out, item)
		}
	}
	return out, nil
}

type Resolution struct {
	Kind, ID, ProviderID, Code, Reference, Note string
	Abandon, NotCreated                         bool
}

func (s *Service) Resolve(ctx context.Context, account *models.Account, actor *models.User, customer *models.RampCustomer, in Resolution, ip string) error {
	if actor == nil || !actor.TotpEnabled {
		return ErrFundingForbidden
	}
	// Resolve the child in this customer's namespace before spending any MFA code.
	switch in.Kind {
	case "kyc_intent":
		row, _, err := er.NewProviderKycSubmissionIntentRepository().FindByExternalID(in.ID)
		if err != nil && !errors.Is(err, er.ErrKycIntentIntegrity) {
			return err
		}
		if row == nil || row.CustomerID != customer.EngineCustomerID {
			return ErrCustomerNotFound
		}
	case "finalization":
		row, err := er.NewOfframpFundingAuthorizationRepository().FindByExternalID(in.ID)
		if err != nil {
			return err
		}
		if row == nil {
			return ErrCustomerNotFound
		}
		tx, err := er.NewTransactionRepository().FindByID(row.TransactionID)
		if err != nil {
			return err
		}
		if tx == nil || tx.CustomerID != customer.EngineCustomerID {
			return ErrCustomerNotFound
		}
	case "support_ticket":
		tx, err := er.NewTransactionRepository().FindByCustomerAndExternalID(customer.EngineCustomerID, in.ID)
		if err != nil {
			return err
		}
		if tx == nil {
			return ErrCustomerNotFound
		}
	default:
		return fmt.Errorf("unknown recovery action")
	}
	if err := s.d.Verifier.Verify(actor, in.Code, ""); err != nil {
		return err
	}
	runtime, err := s.Runtime(account, actor)
	if err != nil {
		return err
	}
	engineActor, err := s.d.Customers.AuditActor(ctx, actor.ID)
	if err != nil {
		return err
	}
	actions := admin.NewAttentionActions(admin.AttentionActionsDeps{Transactions: er.NewTransactionRepository(), Authorizations: er.NewOfframpFundingAuthorizationRepository(), Intents: er.NewProviderKycSubmissionIntentRepository(), Kyc: runtime.Kyc, Finalizations: runtime.Finalizations, Audit: audit.NewService(er.NewAdminAuditLogRepository(), er.NewUserRepository(), er.NewCustomerRepository())})
	who := aud.ActorDTO{Admin: engineActor, IP: ip}
	switch in.Kind {
	case "kyc_intent":
		_, err = actions.ResolveKycIntent(ctx, who, in.ID, ad.ResolveKycIntentDTO{CustomerID: in.ProviderID, Abandon: in.Abandon})
	case "finalization":
		_, err = actions.ResolveFinalization(ctx, who, in.ID, ad.ResolveFinalizationDTO{PayoutID: in.ProviderID, NotCreated: in.NotCreated})
	case "support_ticket":
		_, err = actions.RecordSupportTicket(who, in.ID, ad.SupportTicketDTO{Reference: in.Reference, Note: in.Note})
	}
	return err
}
func (s *Service) AuditPrivateRead(ctx context.Context, actorID uuid.UUID, customer *models.RampCustomer, ip string) error {
	actor, err := s.d.Customers.AuditActor(ctx, actorID)
	if err != nil {
		return err
	}
	return audit.NewService(er.NewAdminAuditLogRepository(), er.NewUserRepository(), er.NewCustomerRepository()).Record(aud.ActorDTO{Admin: actor, IP: ip}, aud.EntryDTO{Action: "customer.compliance_profile.viewed", SubjectType: audit.SubjectCustomers, SubjectID: customer.EngineCustomerID})
}

package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	svc "github.com/macrowallets/waas/app/services/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/controllers/api/client/bank"
	"github.com/macrowallets/waas/packages/rampengine/app/http/controllers/api/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/http/controllers/api/client/ramps"
)

type FlowHandler struct{ service *svc.Service }

func NewFlowHandler(s *svc.Service) *FlowHandler { return &FlowHandler{s} }
func (h *FlowHandler) Action(operation string) http.HandlerFunc {
	return func(ctx http.Context) http.Response {
		runtime, err := h.service.Runtime(requestctx.MustAccount(ctx), requestctx.RampActor(ctx))
		if err != nil {
			return responses.InternalError(ctx, err)
		}
		switch operation {
		case "kyc.Index":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Index(ctx)
		case "kyc.Overview":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Overview(ctx)
		case "kyc.RefreshOverview":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).RefreshOverview(ctx)
		case "kyc.Capabilities":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Capabilities(ctx)
		case "kyc.Profile":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Profile(ctx)
		case "kyc.Countries":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Countries(ctx)
		case "kyc.Show":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Show(ctx)
		case "kyc.Requirements":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Requirements(ctx)
		case "kyc.StartTos":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).StartTos(ctx)
		case "kyc.SubmitApplication":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).SubmitApplication(ctx)
		case "kyc.SubmitApplicationFanOut":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).SubmitApplicationFanOut(ctx)
		case "kyc.SubmitDocuments":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).SubmitDocuments(ctx)
		case "kyc.SubmitDocumentsFanOut":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).SubmitDocumentsFanOut(ctx)
		case "kyc.Refresh":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).Refresh(ctx)
		case "kyc.RequestEndorsements":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).RequestEndorsements(ctx)
		case "kyc.HostedLink":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).HostedLink(ctx)
		case "kyc.StartTosFanOut":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).StartTosFanOut(ctx)
		case "kyc.RecordTosAcceptances":
			return ramps.NewKycController(runtime.Kyc, runtime.Capabilities).RecordTosAcceptances(ctx)
		case "integrations.Index":
			return ramps.NewIntegrationController(runtime.Integration).Index(ctx)
		case "integrations.Show":
			return ramps.NewIntegrationController(runtime.Integration).Show(ctx)
		case "integrations.Start":
			return ramps.NewIntegrationController(runtime.Integration).Start(ctx)
		case "integrations.Refresh":
			return ramps.NewIntegrationController(runtime.Integration).Refresh(ctx)
		case "onramps.Estimate":
			return ramps.NewOnrampController(runtime.Onramp).Estimate(ctx)
		case "onramps.Create":
			return ramps.NewOnrampController(runtime.Onramp).Create(ctx)
		case "onramps.Show":
			return ramps.NewOnrampController(runtime.Onramp).Show(ctx)
		case "onramps.Index":
			return ramps.NewOnrampController(runtime.Onramp).Index(ctx)
		case "offramps.Estimate":
			return ramps.NewOfframpController(runtime.Offramp).Estimate(ctx)
		case "offramps.BankOptions":
			return ramps.NewOfframpController(runtime.Offramp).BankOptions(ctx)
		case "offramps.Create":
			return ramps.NewOfframpController(runtime.Offramp).Create(ctx)
		case "offramps.Show":
			return ramps.NewOfframpController(runtime.Offramp).Show(ctx)
		case "offramps.SimulateDeposit":
			return ramps.NewOfframpController(runtime.Offramp).SimulateDeposit(ctx)
		case "offramps.Cancel":
			return ramps.NewOfframpController(runtime.Offramp).Cancel(ctx)
		case "offramps.RefreshFundingAction":
			return ramps.NewOfframpController(runtime.Offramp).RefreshFundingAction(ctx)
		case "offramps.Finalize":
			return ramps.NewOfframpController(runtime.Offramp).Finalize(ctx)
		case "offramps.Index":
			return ramps.NewOfframpController(runtime.Offramp).Index(ctx)
		case "virtual_accounts.Provision":
			return ramps.NewVirtualAccountController(runtime.VirtualAccounts).Provision(ctx)
		case "virtual_accounts.Show":
			return ramps.NewVirtualAccountController(runtime.VirtualAccounts).Show(ctx)
		case "virtual_accounts.UpdateDestination":
			return ramps.NewVirtualAccountController(runtime.VirtualAccounts).UpdateDestination(ctx)
		case "payout_addresses.Provision":
			return ramps.NewPayoutAddressController(runtime.LiquidationAddresses).Provision(ctx)
		case "payout_addresses.SimulateDeposit":
			return ramps.NewPayoutAddressController(runtime.LiquidationAddresses).SimulateDeposit(ctx)
		case "bank_accounts.Index":
			return ramps.NewBankAccountController(runtime.BankAccounts).Index(ctx)
		case "bank_accounts.Store":
			return ramps.NewBankAccountController(runtime.BankAccounts).Store(ctx)
		case "bank_accounts.Update":
			return ramps.NewBankAccountController(runtime.BankAccounts).Update(ctx)
		case "bank_accounts.Destroy":
			return ramps.NewBankAccountController(runtime.BankAccounts).Destroy(ctx)
		case "wallets.Store":
			return ramps.NewWalletController(runtime.Wallets).Store(ctx)
		case "pix_keys.Index":
			return pixkeys.NewPixKeysController(runtime.PixKeys).Index(ctx)
		case "pix_keys.Store":
			return pixkeys.NewPixKeysController(runtime.PixKeys).Store(ctx)
		case "pix_keys.Update":
			return pixkeys.NewPixKeysController(runtime.PixKeys).Update(ctx)
		case "pix_keys.Destroy":
			return pixkeys.NewPixKeysController(runtime.PixKeys).Destroy(ctx)
		case "beneficiaries.Index":
			return bank.NewBeneficiariesController(runtime.BankBeneficiaries).Index(ctx)
		case "beneficiaries.Show":
			return bank.NewBeneficiariesController(runtime.BankBeneficiaries).Show(ctx)
		case "beneficiaries.Store":
			return bank.NewBeneficiariesController(runtime.BankBeneficiaries).Store(ctx)
		case "beneficiaries.Update":
			return bank.NewBeneficiariesController(runtime.BankBeneficiaries).Update(ctx)
		case "beneficiaries.Destroy":
			return bank.NewBeneficiariesController(runtime.BankBeneficiaries).Destroy(ctx)
		default:
			return responses.Fail(ctx, 404, "not_found", "not found")
		}
	}
}

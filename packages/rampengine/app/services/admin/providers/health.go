package providers

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/settings"
)

const (
	RailStatusOpen   = "open"
	RailStatusClosed = "closed"

	ReasonEmptyOrder       = "empty_order"
	ReasonProviderDisabled = "provider_disabled"
	ReasonModeMismatch     = "mode_mismatch"
	ReasonAdapterStub      = "adapter_stub"
	ReasonRailUnsupported  = "rail_unsupported"
	ReasonFlagClosed       = "flag_closed"
)

// Snapshot is the operator view of the payment stack: every registered
// provider's admin state, and why each PIX rail is open or closed. It is not
// an HTTP type — the resource projects it.
type Snapshot struct {
	Providers []ProviderHealth
	Rails     []RailHealth
}

// ProviderHealth is one registered adapter's place in the stack.
type ProviderHealth struct {
	Code        string
	Enabled     bool
	Mode        string
	Implemented bool
	Credentials map[string]bool
	// BlindPay is the BlindPay-only detail; nil for every other provider.
	BlindPay *BlindPayHealth
}

// BlindPayHealth is what an operator checks before money moves through
// BlindPay. Everything here is public configuration: no credential.
type BlindPayHealth struct {
	InstanceConfigured        bool
	ModeMatchesEnv            bool
	USDCMint                  string
	USDTMint                  string
	DelegateAddress           string
	ProductionPayoutConfirmed bool
	LastWebhookAt             *time.Time
}

// RailHealth is one rail's routing policy plus the stack-only eligibility
// answer. Via is the first surviving provider; Reason is why the head of the
// order is ineligible when nobody survives.
type RailHealth struct {
	Rail          string
	ProviderOrder []string
	// FeePercent is the fee of the provider serving the rail (Via), or of the
	// first provider in the order that has one when the rail is closed. It is
	// kept for admin builds that predate Fees.
	FeePercent string
	// Fees is each provider's fee on this rail.
	Fees   map[string]string
	MinBRL *string
	MaxBRL *string
	// MinUSD and MaxUSD are a bank rail's limits; nil when unlimited or on Pix.
	MinUSD *string
	MaxUSD *string
	Status string
	Via    *string
	Reason *string
}

// Health answers "why can nobody transact?" by crossing the operator's
// settings with whether each adapter is a real implementation. User KYC and
// integration are skipped: those are per-person, not a stack question.
func (s *Service) Health(ctx context.Context) (Snapshot, error) {
	providers, err := s.providerRows()
	if err != nil {
		return Snapshot{}, err
	}
	rails, err := s.railRows(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Providers: providers, Rails: rails}, nil
}

func (s *Service) providerRows() ([]ProviderHealth, error) {
	codes := s.factory.Codes()
	out := make([]ProviderHealth, 0, len(codes))
	for _, code := range codes {
		state, err := s.config.Provider(code)
		if err != nil {
			return nil, err
		}
		creds, err := s.credentialFlags(code)
		if err != nil {
			return nil, err
		}
		row := ProviderHealth{
			Code:        code,
			Enabled:     state.Enabled,
			Mode:        state.Mode,
			Implemented: s.factory.IsImplemented(code),
			Credentials: creds,
		}
		if code == models.ProviderBlindPay {
			detail, err := s.blindPayDetail(code, state)
			if err != nil {
				return nil, err
			}
			row.BlindPay = detail
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) credentialFlags(code ramp.ProviderCode) (map[string]bool, error) {
	flags := map[string]bool{}
	group, ok := settings.FindGroup(ramp.ProviderGroup(code))
	if !ok {
		return flags, nil
	}
	for _, def := range group.Settings {
		if !def.Secret {
			continue
		}
		value, err := s.config.Secret(code, def.Key)
		if err != nil {
			return nil, err
		}
		// Presence only. The plaintext never leaves Config.Secret.
		flags[def.Key] = value != ""
	}
	return flags, nil
}

func (s *Service) railRows(ctx context.Context) ([]RailHealth, error) {
	rails := slices.Concat(ramp.AllRails(), ramp.BankRails())
	out := make([]RailHealth, 0, len(rails))
	for _, rail := range rails {
		row, err := s.oneRail(ctx, rail)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) oneRail(ctx context.Context, rail ramp.Rail) (RailHealth, error) {
	policy, err := s.config.Rail(rail)
	if err != nil {
		return RailHealth{}, err
	}
	row, err := s.route(rail, policy)
	if err != nil {
		return RailHealth{}, err
	}
	row.FeePercent = servingFee(policy, row.Via)
	if rail.IsBank() && row.Status == RailStatusOpen {
		on, err := s.railFlagOn(ctx, rail)
		if err != nil {
			return RailHealth{}, err
		}
		if !on {
			reason := ReasonFlagClosed
			row.Status, row.Via, row.Reason = RailStatusClosed, nil, &reason
			row.FeePercent = servingFee(policy, nil)
		}
	}
	return row, nil
}

// route answers whether the rail is open, through which provider, or why not.
func (s *Service) route(rail ramp.Rail, policy ramp.RailPolicy) (RailHealth, error) {
	row := RailHealth{
		Rail:          rail.String(),
		ProviderOrder: slices.Clone(policy.Providers),
		Fees:          maps.Clone(policy.Fees),
		MinBRL:        policy.MinBRL,
		MaxBRL:        policy.MaxBRL,
		MinUSD:        policy.MinUSD,
		MaxUSD:        policy.MaxUSD,
		Status:        RailStatusClosed,
	}
	if len(policy.Providers) == 0 {
		reason := ReasonEmptyOrder
		row.Reason = &reason
		return row, nil
	}
	var headReason string
	for i, code := range policy.Providers {
		skip, reason, err := s.skipReason(code, rail)
		if err != nil {
			return RailHealth{}, err
		}
		if i == 0 {
			headReason = reason
		}
		if skip {
			continue
		}
		via := code
		row.Status = RailStatusOpen
		row.Via = &via
		row.Reason = nil
		return row, nil
	}
	row.Reason = &headReason
	return row, nil
}

// servingFee is the single fee an older admin build shows for the rail: the
// serving provider's, else the first provider in the order that has one.
func servingFee(policy ramp.RailPolicy, via *string) string {
	if via != nil {
		if fee, err := policy.FeeFor(*via); err == nil {
			return fee
		}
	}
	for _, code := range policy.Providers {
		if fee, err := policy.FeeFor(code); err == nil {
			return fee
		}
	}
	return ""
}

func (s *Service) skipReason(code ramp.ProviderCode, rail ramp.Rail) (bool, string, error) {
	state, err := s.config.Provider(code)
	if err != nil {
		return false, "", err
	}
	if !state.IsUsable() {
		return true, ReasonProviderDisabled, nil
	}
	if err := ramp.ValidateMode(state.Mode, s.appEnv); err != nil {
		return true, ReasonModeMismatch, nil
	}
	if !s.factory.IsImplemented(code) {
		return true, ReasonAdapterStub, nil
	}
	adapter, err := s.factory.Get(code)
	if err != nil {
		return false, "", err
	}
	// The rail alone: this row answers "is this rail open", and narrowing it to
	// one token would answer a different question than the one it renders.
	if !adapter.Supports(ramp.Capability{Rail: rail}) {
		return true, ReasonRailUnsupported, nil
	}
	return false, "", nil
}

// railFlagOn reads the rail's switch globally. A rail built without a flag
// store (a unit wiring) is not gated here.
func (s *Service) railFlagOn(ctx context.Context, rail ramp.Rail) (bool, error) {
	if s.features == nil {
		return true, nil
	}
	return s.features.IsActive(ctx, rail.Flag(), nil)
}

func (s *Service) blindPayDetail(code ramp.ProviderCode, state ramp.ProviderState) (*BlindPayHealth, error) {
	read := func(key string) (string, error) { return s.config.ProviderString(code, key) }
	instance, err := read(ramp.SettingInstanceID)
	if err != nil {
		return nil, err
	}
	usdc, err := read(ramp.SettingSolanaUSDCMint)
	if err != nil {
		return nil, err
	}
	usdt, err := read(ramp.SettingSolanaUSDTMint)
	if err != nil {
		return nil, err
	}
	delegate, err := read(ramp.SettingSolanaDelegateAddress)
	if err != nil {
		return nil, err
	}
	confirmed, err := read(ramp.SettingProductionPayoutConfirmation)
	if err != nil {
		return nil, err
	}
	detail := &BlindPayHealth{
		InstanceConfigured:        instance != "",
		ModeMatchesEnv:            ramp.ValidateMode(state.Mode, s.appEnv) == nil,
		USDCMint:                  usdc,
		USDTMint:                  usdt,
		DelegateAddress:           delegate,
		ProductionPayoutConfirmed: confirmed == "true",
	}
	if s.webhooks != nil {
		if detail.LastWebhookAt, err = s.webhooks.LastReceivedAt(code); err != nil {
			return nil, err
		}
	}
	return detail, nil
}

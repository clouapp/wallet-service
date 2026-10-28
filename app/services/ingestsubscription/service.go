// Package ingestsubscription creates the provider push subscriptions (Alchemy,
// Helius, QuickNode) that call the ingest route, and lists them. A subscription is
// the provider's webhook and its webhook_subscriptions row, whose signing secret is
// stored sealed; the address sync (webhooksync) keeps its address list in step.
package ingestsubscription

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/pgerr"
)

var (
	// ErrUnknownProvider is a provider name without a push adapter.
	ErrUnknownProvider = errors.New("ingestsubscription: unknown provider")
	// ErrUnsupportedChain is a chain on a network the provider does not push.
	ErrUnsupportedChain = errors.New("ingestsubscription: the provider does not push this chain's network")
	// ErrMissingConfiguration is a setting the subscription needs that is not set;
	// Plan.Missing names each one.
	ErrMissingConfiguration = errors.New("ingestsubscription: configuration missing")
	// ErrInvalidBaseURL is a base URL a provider cannot post to; the error names the
	// rule (egress.ParsePublicBase) and where the base URL was read, never its value.
	ErrInvalidBaseURL = errors.New("ingestsubscription: the ingest base URL is not a public https URL a provider can post to")
	// ErrBaseURLUnread is a run with --apply whose ingest_push platform setting could
	// not be read: the environment value may be stale, so no provider webhook is
	// created on it. A dry run falls back to the environment value and says so.
	ErrBaseURLUnread = errors.New("ingestsubscription: --apply needs the ingest_push.base_url platform setting to be readable; nothing was created")
	// ErrBaseURLSettingInvalid is a run with --apply whose stored ingest_push.base_url
	// platform setting the base URL rule refuses (an edit made outside the service, or
	// a rule tightened since it was saved): the operator meant that value, so no
	// provider webhook is created on the environment value instead. A dry run falls
	// back to the environment value and says so. It never carries the stored value.
	ErrBaseURLSettingInvalid = errors.New("ingestsubscription: --apply refuses to run while the stored ingest_push.base_url platform setting is not a public https URL a provider can post to; fix or clear the setting; nothing was created")
	// ErrChainSubscribed is a chain another provider already pushes.
	ErrChainSubscribed = errors.New("ingestsubscription: the chain already has an active subscription with another provider")
	// ErrNoSigningSecret is a provider webhook created without a secret to verify its calls.
	ErrNoSigningSecret = errors.New("ingestsubscription: the provider returned no signing secret")
	// ErrAddressSyncFailed is a stored subscription whose address sync failed.
	ErrAddressSyncFailed = errors.New("ingestsubscription: subscription stored, address sync failed")
	// ErrActivationFailed is a stored subscription whose webhook, created paused, was
	// not activated; the next ingest:subscribe --apply activates it.
	ErrActivationFailed = errors.New("ingestsubscription: subscription stored, webhook not activated")
)

// undoTimeout bounds the delete of a webhook whose row could not be stored; it runs
// with a context detached from the request, which may be what ended.
const undoTimeout = 30 * time.Second

// ingestRoute is the path of the inbound webhook route under the base URL.
const ingestRoute = "/v1/webhooks/ingest/"

// providerKeySettings names, per provider, where its API key is configured.
// The platform setting comes first: it wins when it holds a usable value.
var providerKeySettings = map[string]string{
	providers.ProviderAlchemy:   "the provider_alchemy platform setting (enabled, with auth_token) or ALCHEMY_AUTH_TOKEN (vault.webhooks.alchemy_auth_token)",
	providers.ProviderHelius:    "the provider_helius platform setting (enabled, with api_key) or HELIUS_API_KEY (vault.webhooks.helius_api_key)",
	providers.ProviderQuickNode: "the provider_quicknode platform setting (enabled, with api_key) or QUICKNODE_API_KEY (vault.webhooks.quicknode_api_key)",
}

// baseURLSetting names where the base URL is configured, the setting first since it wins.
const baseURLSetting = "the ingest_push.base_url platform setting or WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url): the public https origin the providers post to"

// The notes a plan carries when the platform setting was not used.
const (
	baseURLUnreadNote  = "the ingest_push.base_url platform setting could not be read: this dry run uses WEBHOOK_INGEST_BASE_URL, and --apply refuses to run until the setting can be read"
	baseURLInvalidNote = "the stored ingest_push.base_url platform setting is not a public https URL and is ignored: this dry run uses WEBHOOK_INGEST_BASE_URL, and --apply refuses to run until the setting is fixed or cleared"
)

const (
	// BaseURLFromSetting is a base URL read from the ingest_push.base_url platform setting.
	BaseURLFromSetting = "setting"
	// BaseURLFromEnv is a base URL read from WEBHOOK_INGEST_BASE_URL.
	BaseURLFromEnv = "env"
)

// baseURLSources names, per source, where the base URL of a run was read.
var baseURLSources = map[string]string{
	BaseURLFromSetting: "the ingest_push.base_url platform setting",
	BaseURLFromEnv:     "WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)",
}

// BaseURL is the origin the providers post to for one run, and where it was read
// (BaseURLFromSetting or BaseURLFromEnv). An empty URL is no base URL configured.
// SettingInvalid is a stored platform setting the rule refuses, left for the
// environment value.
type BaseURL struct {
	URL            string
	Source         string
	SettingInvalid bool
}

// baseURLGuard checks the base URL by the base URL rule and what its host name
// resolves to now; *egress.PublicBaseGuard is the one implementation, so no other
// guard (a webhook receiver's, an RPC node's) can be wired in its place.
type baseURLGuard interface {
	CheckPublicBase(ctx context.Context, rawURL string) error
}

// Creator creates and deletes one provider's push webhook.
type Creator interface {
	CreateWebhook(ctx context.Context, cfg providers.ProviderConfig) (*providers.ProviderWebhook, error)
	DeleteWebhook(ctx context.Context, webhookID string) error
}

// activator is a provider whose webhook is created paused (QuickNode) and activated
// once its subscription is stored, so it never pushes what nothing verifies.
type activator interface {
	ActivateWebhook(ctx context.Context, webhookID string) error
}

// subscriptionStore is the webhook_subscriptions rows this service reads and writes.
type subscriptionStore interface {
	FindByProviderAndChain(ctx context.Context, provider, chainID string) (*models.WebhookSubscription, error)
	FindByChainID(ctx context.Context, chainID string) (*models.WebhookSubscription, error)
	FindAll(ctx context.Context) ([]models.WebhookSubscription, error)
	Create(ctx context.Context, sub *models.WebhookSubscription) error
}

// addressLister is the chain's watched addresses, sent with the new webhook.
type addressLister interface {
	PluckActiveAddresses(ctx context.Context, chainID string) ([]string, error)
}

// addressSync pushes the chain's watched addresses to its subscription.
type addressSync interface {
	SyncChainAddresses(ctx context.Context, chainID string) error
}

// Deps is everything the subscription service needs.
type Deps struct {
	Subscriptions subscriptionStore
	Addresses     addressLister
	Sync          addressSync
	// Creators are the push adapters by provider name.
	Creators map[string]Creator
	// ProviderKey returns the provider's API key; empty when none is configured.
	// The service only checks that there is one: the adapter reads it at its call.
	ProviderKey func(ctx context.Context, provider string) string
	// ChainNetwork names the network the chain record serves (a models.Network*
	// name); a chain that does not exist is models.ErrRepositoryNotFound.
	ChainNetwork func(ctx context.Context, chainID string) (string, error)
	// BaseURLAtUse reads the base URL at each run, so a changed setting applies to
	// the next run without a restart: the ingest_push.base_url platform setting,
	// else WEBHOOK_INGEST_BASE_URL. An error is a setting that could not be read;
	// the BaseURL is then the environment value, which only a dry run uses.
	BaseURLAtUse func(ctx context.Context) (BaseURL, error)
	// Seal seals the signing secret as every stored secret is (settings.Seal).
	Seal func(plaintext string) (string, error)
	// Destinations checks what the base URL's host resolves to, by the base URL rule
	// (egress.PublicBaseGuard); nil is that guard over the system resolver.
	Destinations baseURLGuard
}

// Service creates and lists provider push subscriptions.
type Service struct {
	deps Deps
}

// NewService wires the subscription service from Deps.
func NewService(deps Deps) *Service {
	if deps.Destinations == nil {
		deps.Destinations = egress.NewPublicBaseGuard(nil)
	}
	return &Service{deps: deps}
}

// Request is one ingest:subscribe run; without Apply nothing is created.
type Request struct {
	Provider string
	ChainID  string
	Apply    bool
}

// Plan is what a run found and did. Missing names the settings to set; BaseURLSource
// names where the base URL of WebhookURL was read, and BaseURLNote why the platform
// setting was not used when it was not read or is invalid; BaseURLWarning is a dry
// run's base URL whose host resolves to an address the rule refuses, which --apply
// refuses; Existing is a subscription already there (nothing is done); Applied is a
// subscription created.
type Plan struct {
	Provider          string
	ChainID           string
	Network           string
	ProviderNetwork   string
	WebhookURL        string
	BaseURLSource     string
	BaseURLNote       string
	BaseURLWarning    string
	Addresses         int
	Missing           []string
	Existing          *Listed
	Applied           bool
	SubscriptionID    uuid.UUID
	ProviderWebhookID string
}

// Listed is a subscription as an operator may see it: never its signing secret,
// and its webhook URL by host only.
type Listed struct {
	ID                uuid.UUID
	Provider          string
	ChainID           string
	ProviderWebhookID string
	Host              string
	Status            string
	SyncStatus        string
	LastSyncedAt      *time.Time
}

// Subscribe plans, and with Apply creates, the provider subscription of one chain:
// the provider's webhook pointed at the ingest route, the row with its sealed
// signing secret, then an address sync. It fails when a setting is missing
// (ErrMissingConfiguration), when the provider or the chain is not served, and
// when the provider's webhook cannot be stored, after deleting it. Before any
// provider call, Apply also refuses a platform setting that could not be read
// (ErrBaseURLUnread) or that the base URL rule refuses (ErrBaseURLSettingInvalid),
// and a base URL whose host resolves now to an address the rule refuses
// (ErrInvalidBaseURL, egress.ReasonResolved); a dry run notes each instead.
func (s *Service) Subscribe(ctx context.Context, req Request) (Plan, error) {
	plan := Plan{Provider: strings.ToLower(strings.TrimSpace(req.Provider)), ChainID: strings.TrimSpace(req.ChainID)}
	creator, known := s.deps.Creators[plan.Provider]
	if !known {
		return plan, fmt.Errorf("%w %q", ErrUnknownProvider, plan.Provider)
	}
	network, err := s.deps.ChainNetwork(ctx, plan.ChainID)
	if err != nil {
		return plan, fmt.Errorf("chain %s: %w", plan.ChainID, err)
	}
	plan.Network = network
	providerNetwork, served := providers.SubscriptionNetwork(plan.Provider, network)
	if !served {
		return plan, fmt.Errorf("%s on %s (%s): %w", plan.Provider, plan.ChainID, network, ErrUnsupportedChain)
	}
	plan.ProviderNetwork = providerNetwork

	base, unread := s.baseURL(ctx)
	switch {
	case unread != nil && req.Apply:
		return plan, fmt.Errorf("%w: %w", ErrBaseURLUnread, unread)
	case base.SettingInvalid && req.Apply:
		return plan, ErrBaseURLSettingInvalid
	case unread != nil:
		plan.BaseURLNote = baseURLUnreadNote
	case base.SettingInvalid:
		plan.BaseURLNote = baseURLInvalidNote
	}
	if strings.TrimSpace(base.URL) == "" {
		plan.Missing = append(plan.Missing, baseURLSetting)
	} else {
		plan.BaseURLSource = baseURLSourceName(base.Source)
		if plan.WebhookURL, err = webhookURL(base.URL, plan.Provider, plan.ChainID); err != nil {
			return plan, fmt.Errorf("%w; read from %s", err, plan.BaseURLSource)
		}
		if resolveErr := s.deps.Destinations.CheckPublicBase(ctx, base.URL); resolveErr != nil {
			if req.Apply {
				return plan, fmt.Errorf("%w: %w; read from %s", ErrInvalidBaseURL, resolveErr, plan.BaseURLSource)
			}
			plan.BaseURLWarning = fmt.Sprintf("%s (base URL read from %s): --apply refuses it", refusalReason(resolveErr), plan.BaseURLSource)
		}
	}
	if strings.TrimSpace(s.deps.ProviderKey(ctx, plan.Provider)) == "" {
		plan.Missing = append(plan.Missing, providerKeySettings[plan.Provider])
	}
	if len(plan.Missing) > 0 {
		return plan, ErrMissingConfiguration
	}

	existing, err := s.existing(ctx, plan.Provider, plan.ChainID)
	if err != nil {
		return plan, err
	}
	if existing != nil {
		plan.Existing = existing
		if req.Apply {
			return plan, s.activate(ctx, creator, existing.ProviderWebhookID)
		}
		return plan, nil
	}
	addresses, err := s.deps.Addresses.PluckActiveAddresses(ctx, plan.ChainID)
	if err != nil {
		return plan, fmt.Errorf("load the %s addresses: %w", plan.ChainID, err)
	}
	if addresses == nil {
		addresses = []string{}
	}
	plan.Addresses = len(addresses)
	if !req.Apply {
		return plan, nil
	}
	return s.create(ctx, creator, plan, addresses)
}

// baseURL is the base URL in effect for this run; none when no source is wired.
func (s *Service) baseURL(ctx context.Context) (BaseURL, error) {
	if s.deps.BaseURLAtUse == nil {
		return BaseURL{}, nil
	}
	return s.deps.BaseURLAtUse(ctx)
}

// baseURLSourceName is where a base URL was read, as an operator names it.
func baseURLSourceName(source string) string {
	if name, ok := baseURLSources[source]; ok {
		return name
	}
	return "an unknown source"
}

// activate starts the webhook of a provider that creates its webhooks paused; for
// the others there is nothing to do.
func (s *Service) activate(ctx context.Context, creator Creator, webhookID string) error {
	paused, ok := creator.(activator)
	if !ok {
		return nil
	}
	if err := paused.ActivateWebhook(ctx, webhookID); err != nil {
		return fmt.Errorf("%w: provider webhook %s: %w", ErrActivationFailed, webhookID, err)
	}
	return nil
}

// existing is the subscription of the provider on the chain, nil when there is none;
// another provider's active subscription on the chain is ErrChainSubscribed.
func (s *Service) existing(ctx context.Context, provider, chainID string) (*Listed, error) {
	sub, err := s.deps.Subscriptions.FindByProviderAndChain(ctx, provider, chainID)
	if err == nil && sub.Status == models.WebhookSubscriptionActive {
		listed := listed(*sub)
		return &listed, nil
	}
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, fmt.Errorf("find the %s subscription of %s: %w", provider, chainID, err)
	}
	active, err := s.deps.Subscriptions.FindByChainID(ctx, chainID)
	switch {
	case errors.Is(err, models.ErrRepositoryNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("find the active subscription of %s: %w", chainID, err)
	default:
		return nil, fmt.Errorf("%s is pushed by %s (subscription %s): %w", chainID, active.Provider, active.ID, ErrChainSubscribed)
	}
}

// create makes the provider's webhook, stores its row and syncs the addresses. A
// webhook whose row cannot be written is deleted, so none is left that nothing verifies.
func (s *Service) create(ctx context.Context, creator Creator, plan Plan, addresses []string) (Plan, error) {
	webhook, err := creator.CreateWebhook(ctx, providers.ProviderConfig{
		ChainID: plan.ChainID, Network: plan.ProviderNetwork, WebhookURL: plan.WebhookURL, Addresses: addresses,
	})
	if err != nil {
		// The provider's own answer (its HTTP status and message) is what an operator
		// acts on: a sentinel such as "provider error" alone says nothing about a plan
		// limit, a refused URL or a key without the webhook scope.
		if cause := chain.CauseText(err); cause != "" {
			return plan, fmt.Errorf("create the %s webhook: %w (%s)", plan.Provider, err, cause)
		}
		return plan, fmt.Errorf("create the %s webhook: %w", plan.Provider, err)
	}
	row, err := s.row(plan, webhook)
	if err == nil {
		err = s.deps.Subscriptions.Create(ctx, row)
	}
	if pgerr.IsUniqueViolation(err) {
		// Another run stored the subscription first (the unique index of active
		// subscriptions): this run's webhook goes, the stored one stays.
		undoErr := s.undo(ctx, creator, webhook.ProviderWebhookID, err)
		if existing, findErr := s.existing(ctx, plan.Provider, plan.ChainID); findErr == nil && existing != nil {
			plan.Existing = existing
			return plan, nil
		}
		return plan, undoErr
	}
	if err != nil {
		return plan, s.undo(ctx, creator, webhook.ProviderWebhookID, err)
	}
	plan.Applied, plan.SubscriptionID, plan.ProviderWebhookID = true, row.ID, row.ProviderWebhookID
	if err := s.activate(ctx, creator, row.ProviderWebhookID); err != nil {
		return plan, err
	}
	if err := s.deps.Sync.SyncChainAddresses(ctx, plan.ChainID); err != nil {
		return plan, fmt.Errorf("%w: %w", ErrAddressSyncFailed, err)
	}
	return plan, nil
}

func (s *Service) row(plan Plan, webhook *providers.ProviderWebhook) (*models.WebhookSubscription, error) {
	if webhook == nil || strings.TrimSpace(webhook.ProviderWebhookID) == "" {
		return nil, fmt.Errorf("the %s webhook has no id", plan.Provider)
	}
	if strings.TrimSpace(webhook.SigningSecret) == "" {
		return nil, ErrNoSigningSecret
	}
	sealed, err := s.deps.Seal(webhook.SigningSecret)
	if err != nil {
		return nil, errors.New("seal the signing secret: the cipher refused it")
	}
	return &models.WebhookSubscription{
		ID:                uuid.New(),
		ChainID:           plan.ChainID,
		Provider:          plan.Provider,
		ProviderWebhookID: webhook.ProviderWebhookID,
		WebhookURL:        plan.WebhookURL,
		SigningSecret:     sealed,
		Status:            models.WebhookSubscriptionActive,
	}, nil
}

// undo deletes the provider webhook of a subscription that could not be stored and
// names it when the delete fails too, so an operator can remove it.
func (s *Service) undo(ctx context.Context, creator Creator, webhookID string, cause error) error {
	if webhookID == "" {
		return fmt.Errorf("store the subscription: %w", cause)
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), undoTimeout)
	defer cancel()
	if err := creator.DeleteWebhook(detached, webhookID); err != nil {
		return fmt.Errorf("store the subscription: %w; the provider webhook %s was not deleted, delete it in the provider's dashboard: %w", cause, webhookID, err)
	}
	return fmt.Errorf("store the subscription: %w; the provider webhook %s was deleted", cause, webhookID)
}

// List returns every subscription, without its signing secret.
func (s *Service) List(ctx context.Context) ([]Listed, error) {
	subs, err := s.deps.Subscriptions.FindAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list webhook subscriptions: %w", err)
	}
	out := make([]Listed, 0, len(subs))
	for _, sub := range subs {
		out = append(out, listed(sub))
	}
	return out, nil
}

func listed(sub models.WebhookSubscription) Listed {
	host := ""
	if parsed, err := url.Parse(sub.WebhookURL); err == nil {
		host = parsed.Host
	}
	return Listed{
		ID: sub.ID, Provider: sub.Provider, ChainID: sub.ChainID, ProviderWebhookID: sub.ProviderWebhookID,
		Host: host, Status: sub.Status, SyncStatus: sub.SyncStatus, LastSyncedAt: sub.LastSyncedAt,
	}
}

// refusalReason is the rule a refused base URL broke, a sentence that never carries
// the URL.
func refusalReason(err error) string {
	var refusal *egress.Refusal
	if errors.As(err, &refusal) && refusal.Reason != "" {
		return refusal.Reason
	}
	return ErrInvalidBaseURL.Error()
}

// webhookURL is the ingest route of provider and chain under base, which must be a
// public https origin with an optional path prefix (egress.ParsePublicBase). A
// refusal carries the rule's sentence, never the URL.
func webhookURL(base, provider, chainID string) (string, error) {
	parsed, err := egress.ParsePublicBase(base)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidBaseURL, err)
	}
	return strings.TrimRight(parsed.String(), "/") + ingestRoute + url.PathEscape(provider) + "/" + url.PathEscape(chainID), nil
}

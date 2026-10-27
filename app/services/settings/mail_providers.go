package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/policies"
)

const (
	keyMailProviderKey    = "key"
	keyMailProviderSecret = "secret"
	keyMailRegion         = "region"
	keyMailDomain         = "domain"
	keyMailEndpoint       = "endpoint"
	keyMailAPIKey         = "api_key"
	keyMailToken          = "token"
	keyMailMessageStream  = "message_stream_id"

	blockMailProviders = "Providers"

	credentialPairMessage = "must be set together"
)

// S1.4.4 registers mail_ses, mail_mailgun, mail_resend, and mail_postmark
// with the xip keys. mail_delivery.driver selects the transport at send time:
// mailgun, resend and postmark send over their HTTP APIs with these groups
// (EffectiveMailProvider); ses is refused until it ships.

func mailSESGroup() Group {
	return Group{
		Name:    groupMailSES,
		Scope:   ScopePlatform,
		Section: sectionMail,
		Block:   blockMailProviders,
		// key and secret are the SES key pair. Both are secrets. region,
		// from_address, and from_name are returned. S1.4.7 names mail.view
		// and mail.update on this credential so settings.update is not the
		// grant. No account role holds that pair. There is no platform
		// permission catalog, so a platform_admins row stands in.
		ViewPermission:   policies.PermMailView,
		UpdatePermission: policies.PermMailUpdate,
		CredentialGroups: [][]string{{keyMailProviderKey, keyMailProviderSecret}},
		Settings: append([]Definition{
			{
				Key:     keyMailProviderKey,
				Label:   "Access key id",
				Help:    "SES access key id. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
			{
				Key:     keyMailProviderSecret,
				Label:   "Secret access key",
				Help:    "SES secret access key. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
			{
				Key:     keyMailRegion,
				Label:   "Region",
				Help:    "AWS region for SES.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
		}, mailProviderSenderOverrides()...),
	}
}

func mailMailgunGroup() Group {
	return Group{
		Name:             groupMailMailgun,
		Scope:            ScopePlatform,
		Section:          sectionMail,
		Block:            blockMailProviders,
		ViewPermission:   policies.PermMailView,
		UpdatePermission: policies.PermMailUpdate,
		// secret is the API key. domain and endpoint are returned.
		// S1.4.7 names mail.view and mail.update so settings.update is not
		// the grant. No account role holds that pair. A platform_admins row
		// stands in: there is no platform permission catalog.
		Settings: append([]Definition{
			{
				Key:     keyMailDomain,
				Label:   "Domain",
				Help:    "Mailgun sending domain.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
			{
				Key:     keyMailProviderSecret,
				Label:   "API key",
				Help:    "Mailgun API key. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
			{
				Key:     keyMailEndpoint,
				Label:   "API host",
				Help:    "api.mailgun.net, or api.eu.mailgun.net for an EU domain.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
		}, mailProviderSenderOverrides()...),
	}
}

func mailResendGroup() Group {
	return Group{
		Name:             groupMailResend,
		Scope:            ScopePlatform,
		Section:          sectionMail,
		Block:            blockMailProviders,
		ViewPermission:   policies.PermMailView,
		UpdatePermission: policies.PermMailUpdate,
		Settings: append([]Definition{
			{
				Key:     keyMailAPIKey,
				Label:   "API key",
				Help:    "Resend API key. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
		}, mailProviderSenderOverrides()...),
	}
}

func mailPostmarkGroup() Group {
	return Group{
		Name:             groupMailPostmark,
		Scope:            ScopePlatform,
		Section:          sectionMail,
		Block:            blockMailProviders,
		ViewPermission:   policies.PermMailView,
		UpdatePermission: policies.PermMailUpdate,
		Settings: append([]Definition{
			{
				Key:     keyMailToken,
				Label:   "Server token",
				Help:    "Postmark server token. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
			{
				Key:     keyMailMessageStream,
				Label:   "Message stream",
				Help:    "Empty uses the server's default stream.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
		}, mailProviderSenderOverrides()...),
	}
}

func mailProviderSenderOverrides() []Definition {
	return []Definition{
		{
			Key:     keyMailFromAddress,
			Label:   "From address",
			Help:    "Sender override for this provider. Empty is allowed.",
			Type:    TypeString,
			Default: func() any { return "" },
		},
		{
			Key:     keyMailFromName,
			Label:   "From name",
			Help:    "Name override for this provider. Empty is allowed.",
			Type:    TypeString,
			Default: func() any { return "" },
		},
	}
}

// incompleteCredentialGroups rejects a write that would leave one key of a
// pair set and another unset. An empty result means the write may proceed.
// A blank secret is already omitted from writes, so a stored secret still counts.
func incompleteCredentialGroups(group Group, stored, writes map[string]string) *ValidationError {
	invalid := &ValidationError{}
	for _, pair := range group.CredentialGroups {
		if len(pair) < 2 {
			continue
		}
		missing := make([]string, 0, len(pair))
		present := 0
		for _, key := range pair {
			if credentialKeyPresent(stored, writes, key) {
				present++
				continue
			}
			missing = append(missing, key)
		}
		if present == 0 || len(missing) == 0 {
			continue
		}
		for _, key := range missing {
			invalid.add(key, credentialPairMessage)
		}
	}
	return invalid
}

func credentialKeyPresent(stored, writes map[string]string, key string) bool {
	if value, ok := writes[key]; ok {
		return strings.TrimSpace(value) != ""
	}
	value, ok := stored[key]
	return ok && strings.TrimSpace(value) != ""
}

// MailProvider is one HTTP mail provider's stored credentials, opened, and its
// sender override. A blank field is not stored.
type MailProvider struct {
	Driver        string
	Domain        string
	APIKey        string
	Endpoint      string
	MessageStream string
	FromAddress   string
	FromName      string
}

// EffectiveMailProvider reads the group of driver (mailgun, resend, postmark) at
// send time and opens its secret. A database failure is returned; a secret that
// does not open is left blank (the send then fails for a missing credential).
func (s *Service) EffectiveMailProvider(ctx context.Context, driver string) (MailProvider, error) {
	if s == nil {
		return MailProvider{}, errServiceRequired
	}
	if ctx == nil {
		return MailProvider{}, fmt.Errorf("mail provider settings: context is required")
	}
	group, secretKey := "", ""
	switch driver {
	case mailDriverMailgun:
		group, secretKey = groupMailMailgun, keyMailProviderSecret
	case mailDriverResend:
		group, secretKey = groupMailResend, keyMailAPIKey
	case mailDriverPostmark:
		group, secretKey = groupMailPostmark, keyMailToken
	default:
		return MailProvider{}, ErrGroupNotFound
	}
	stored, err := s.platformValues(ctx, group)
	if err != nil {
		return MailProvider{}, err
	}
	out := MailProvider{
		Driver:        driver,
		Domain:        trimmedValue(stored, keyMailDomain),
		Endpoint:      trimmedValue(stored, keyMailEndpoint),
		MessageStream: trimmedValue(stored, keyMailMessageStream),
		FromAddress:   trimmedValue(stored, keyMailFromAddress),
		FromName:      trimmedValue(stored, keyMailFromName),
	}
	if secret, ok := s.openSealed(stored[secretKey]); ok {
		out.APIKey = secret
	}
	return out, nil
}

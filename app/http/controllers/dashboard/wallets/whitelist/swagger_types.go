package whitelist

import "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/whitelist"

// Swagger request and response types of the whitelist routes (doc-only).

type AddWhitelistEntrySwagger struct {
	Address string `json:"address" example:"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"`
	Label   string `json:"label,omitempty" example:"Cold Storage"`
}

type WhitelistEntryListResponse struct {
	Data []whitelist.WhitelistEntry `json:"data"`
}

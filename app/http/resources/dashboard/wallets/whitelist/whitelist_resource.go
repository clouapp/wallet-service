package whitelist

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// WhitelistEntry is the whitelist row the dashboard reads. Field order and
// tags match the model wire, including the embedded timestamps and the
// omission of an empty label. A nil page stays nil; an empty page stays empty.
type WhitelistEntry struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
	ID        uuid.UUID        `json:"id"`
	WalletID  uuid.UUID        `json:"wallet_id"`
	Label     string           `json:"label,omitempty"`
	Address   string           `json:"address"`
}

// WhitelistEntryFrom projects one whitelist row.
func WhitelistEntryFrom(entry models.WhitelistEntry) WhitelistEntry {
	return WhitelistEntry{
		CreatedAt: entry.CreatedAt,
		UpdatedAt: entry.UpdatedAt,
		ID:        entry.ID,
		WalletID:  entry.WalletID,
		Label:     entry.Label,
		Address:   entry.Address,
	}
}

// WhitelistEntriesFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func WhitelistEntriesFrom(entries []models.WhitelistEntry) []WhitelistEntry {
	if entries == nil {
		return nil
	}
	views := make([]WhitelistEntry, len(entries))
	for i := range entries {
		views[i] = WhitelistEntryFrom(entries[i])
	}
	return views
}

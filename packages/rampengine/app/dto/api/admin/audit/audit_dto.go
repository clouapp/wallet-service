// Package audit contains the DTOs of the admin audit trail.
package audit

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ActorDTO is the admin an act is recorded against and the address the request
// came from. A controller fills it with middleware.CurrentActor and passes it
// down with the act, the way it passes the client user.
type ActorDTO struct {
	Admin *models.Admin
	IP    string
} //	@name	AdminAuditActorDTO

// EntryDTO is one act to record. SubjectType and Provider are empty when the act
// has none. Metadata holds field names, ids, booleans and the operator's own
// suspension reason — never a CPF, phone, name, e-mail or Pix key.
type EntryDTO struct {
	Action      string
	SubjectType string
	SubjectID   uint
	Provider    string
	Metadata    map[string]any
} //	@name	AdminAuditEntryDTO

// AdminListAuditLogsDTO carries the validated filters of an audit listing.
// Zero values do not filter; To is exclusive.
type AdminListAuditLogsDTO struct {
	Page        int
	PerPage     int
	AdminID     uint
	SubjectType string
	SubjectID   uint
	Action      string
	From        *time.Time
	To          *time.Time
} //	@name	AdminListAuditLogsDTO

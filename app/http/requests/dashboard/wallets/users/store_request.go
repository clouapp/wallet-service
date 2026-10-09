package users

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
)

// StoreRequest is the body of a wallet membership: the user and the wallet roles
// (a comma-separated set) to give them.
type StoreRequest struct {
	UserID string `form:"user_id" json:"user_id"`
	Roles  string `form:"roles"   json:"roles"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"user_id": "required|uuid",
		"roles":   "required",
	}
}

// UserUUID is the user the rule has already checked is a UUID.
func (r *StoreRequest) UserUUID() uuid.UUID {
	id, _ := uuid.Parse(r.UserID)
	return id
}

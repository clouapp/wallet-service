package repositories

import (
	"context"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	ef "github.com/macrowallets/waas/packages/rampengine/app/facades"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
)

func (*RampCustomers) AuditActor(ctx context.Context, id uuid.UUID) (*em.Admin, error) {
	var result em.Admin
	err := ef.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if _, err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "macro:ramp-actor:"+id.String()); err != nil {
			return err
		}
		var mapped struct{ EngineAdminID uint }
		if err := tx.Table("public.ramp_customer_actors").Where("id", id).First(&mapped); err != nil {
			return err
		}
		if mapped.EngineAdminID != 0 {
			return tx.Where("id", mapped.EngineAdminID).First(&result)
		}
		result = em.Admin{Name: id.String(), Email: id.String() + "@macro.invalid", Password: "!macro-actor-no-login"}
		if err := tx.Create(&result); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO public.ramp_customer_actors(id,engine_admin_id) VALUES(?,?)", id, result.ID)
		return err
	})
	return &result, err
}

package chainregistry

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// FullStore is everything a complete alignment reads and writes.
type FullStore interface {
	Store
	AddressStore
}

// Alignment is the full set of changes that puts the registry, the addresses on
// it and optionally one account on a profile.
type Alignment struct {
	Plan *Plan
	// Reissues by chain id.
	Reissues      map[string][]AddressReissue
	AccountChange *AccountChange
}

// IsEmpty reports whether applying the alignment would write nothing.
func (a *Alignment) IsEmpty() bool {
	return a == nil || (len(a.Plan.Changes) == 0 && len(a.Reissues) == 0 && a.AccountChange == nil)
}

// PlanAlignment builds the alignment to profile. accountID may be uuid.Nil to leave
// accounts alone; probe may be nil to skip the RPC check.
func PlanAlignment(ctx context.Context, profile string, store FullStore, decrypt RPCDecrypter, probe NetworkProbe, accountID uuid.UUID) (*Alignment, error) {
	if store == nil {
		return nil, errors.New("chainregistry: store is required")
	}
	plan, err := BuildPlan(ctx, profile, store, decrypt, probe)
	if err != nil {
		return nil, err
	}
	alignment := &Alignment{Plan: plan, Reissues: make(map[string][]AddressReissue)}
	targets, err := reissueTargets(profile, store)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		planned, err := PlanAddressReissue(store, target)
		if err != nil {
			return nil, err
		}
		if len(planned) > 0 {
			alignment.Reissues[target.ChainID] = planned
		}
	}
	if accountID != uuid.Nil {
		if alignment.AccountChange, err = PlanAccountEnvironment(store, accountID, profile); err != nil {
			return nil, err
		}
	}
	return alignment, nil
}

// ApplyAlignment writes the chain changes first, then the address reissues, then
// the account. Each step is idempotent, so a failed run can be repeated.
func ApplyAlignment(store FullStore, alignment *Alignment) error {
	if store == nil || alignment == nil || alignment.Plan == nil {
		return errors.New("chainregistry: store and alignment are required")
	}
	if err := ApplyPlan(store, alignment.Plan); err != nil {
		return err
	}
	for _, planned := range alignment.Reissues {
		for _, reissue := range planned {
			if err := ApplyAddressReissue(store, reissue); err != nil {
				return fmt.Errorf("reissue addresses of wallet %s: %w", reissue.WalletID, err)
			}
		}
	}
	return ApplyAccountChange(store, alignment.AccountChange)
}

// reissueTargets are the primary records as the profile wants them. Built from the
// profile, not from the chain changes, so a run that stopped after flipping a chain
// still reissues its addresses when repeated.
func reissueTargets(profile string, store Store) ([]ReissueTarget, error) {
	chains, err := store.Chains()
	if err != nil {
		return nil, fmt.Errorf("load chains: %w", err)
	}
	var targets []ReissueTarget
	for _, record := range chains {
		spec, decided, err := models.PrimaryChainNetwork(profile, record.ID)
		if err != nil {
			return nil, err
		}
		if decided {
			targets = append(targets, ReissueTarget{ChainID: record.ID, AdapterType: record.AdapterType, Testnet: spec.IsTestnet})
		}
	}
	return targets, nil
}

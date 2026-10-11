// Package sharemigration moves every wallet's share B from one Secrets Manager to
// another (LocalStack to AWS, or back) and points the wallet row at the copy. The
// source is never written, and an ARN only moves after the bytes in the target are
// proven equal to the source's: a wallet whose share is lost cannot sign.
package sharemigration

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/wallet"
)

// Outcome of one wallet.
const (
	StatusInPlace      = "in place"      // the row already points at the target's copy
	StatusWouldPoint   = "would point"   // the target has an equal copy; the row would move to it
	StatusWouldMigrate = "would migrate" // the target has no copy; one would be created
	StatusPointed      = "pointed"       // the row moved to the target's equal copy
	StatusMigrated     = "migrated"      // a copy was created, read back, and the row moved to it
	StatusFailed       = "failed"
)

// ErrConflict is a target secret under the share's name holding other bytes. It is
// never overwritten.
var ErrConflict = errors.New("the target holds a different secret under the share name")

// ErrReadBack is a created copy that did not read back as the bytes written.
var ErrReadBack = errors.New("the target returned different bytes than were written")

// Wallets lists the wallets and moves the ARN of their share B.
type Wallets interface {
	FindAll(ctx context.Context) ([]models.Wallet, error)
	SetMPCSecretARN(ctx context.Context, id uuid.UUID, arn string) error
}

// Source reads a share by the ARN the wallet row holds.
type Source interface {
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// Target is where the shares go: the Secrets Manager the app itself uses.
type Target interface {
	Lookup(ctx context.Context, name string) (arn string, found bool, err error)
	Create(ctx context.Context, name string, secret []byte) (string, error)
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// Deps is everything the migration needs besides the source, which a run names.
type Deps struct {
	Wallets Wallets
	Target  Target
}

// Service moves shares into Target.
type Service struct {
	wallets Wallets
	target  Target
}

// NewService builds the migration from deps.
func NewService(deps Deps) *Service {
	return &Service{wallets: deps.Wallets, target: deps.Target}
}

// Row is one wallet's outcome. ARNs are not secret; share bytes never leave the service.
type Row struct {
	WalletID uuid.UUID
	Chain    string
	Status   string
	From     string
	To       string
	Err      error
}

// Run moves every wallet's share B from source into the target. Without apply it
// only reports what it would do. Every wallet is tried; the error joins the failed
// ones. Running again is safe: a copy created before a failed ARN update is found
// and pointed at.
func (s *Service) Run(ctx context.Context, source Source, apply bool) ([]Row, error) {
	wallets, err := s.wallets.FindAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	rows := make([]Row, 0, len(wallets))
	var failures []error
	for _, w := range wallets {
		if w.MPCSecretARN == "" {
			continue
		}
		row := s.migrate(ctx, source, w, apply)
		if row.Err != nil {
			failures = append(failures, fmt.Errorf("wallet %s: %w", w.ID, row.Err))
		}
		rows = append(rows, row)
	}
	return rows, errors.Join(failures...)
}

func (s *Service) migrate(ctx context.Context, source Source, w models.Wallet, apply bool) Row {
	row := Row{WalletID: w.ID, Chain: w.Chain, From: w.MPCSecretARN}
	name := wallet.ShareBSecretName(w.ID)

	targetARN, found, err := s.target.Lookup(ctx, name)
	if err != nil {
		return failed(row, fmt.Errorf("look up %s in the target: %w", name, err))
	}
	if found && targetARN == w.MPCSecretARN {
		row.Status, row.To = StatusInPlace, targetARN
		return row
	}

	share, err := source.Binary(ctx, w.MPCSecretARN)
	if err != nil {
		return failed(row, fmt.Errorf("read the source share: %w", err))
	}

	if found {
		return s.pointAtExisting(ctx, row, w.ID, targetARN, share, apply)
	}
	if !apply {
		row.Status = StatusWouldMigrate
		return row
	}

	createdARN, err := s.target.Create(ctx, name, share)
	if err != nil {
		return failed(row, fmt.Errorf("create %s in the target: %w", name, err))
	}
	row.To = createdARN
	back, err := s.target.Binary(ctx, createdARN)
	if err != nil {
		return failed(row, fmt.Errorf("read back %s: %w", name, err))
	}
	if !bytes.Equal(back, share) {
		return failed(row, ErrReadBack)
	}
	if err := s.wallets.SetMPCSecretARN(ctx, w.ID, createdARN); err != nil {
		return failed(row, fmt.Errorf("point the wallet at the copy: %w", err))
	}
	row.Status = StatusMigrated
	return row
}

func (s *Service) pointAtExisting(ctx context.Context, row Row, walletID uuid.UUID, targetARN string, share []byte, apply bool) Row {
	row.To = targetARN
	existing, err := s.target.Binary(ctx, targetARN)
	if err != nil {
		return failed(row, fmt.Errorf("read the target copy: %w", err))
	}
	if !bytes.Equal(existing, share) {
		return failed(row, ErrConflict)
	}
	if !apply {
		row.Status = StatusWouldPoint
		return row
	}
	if err := s.wallets.SetMPCSecretARN(ctx, walletID, targetARN); err != nil {
		return failed(row, fmt.Errorf("point the wallet at the copy: %w", err))
	}
	row.Status = StatusPointed
	return row
}

func failed(row Row, err error) Row {
	row.Status, row.Err = StatusFailed, err
	return row
}

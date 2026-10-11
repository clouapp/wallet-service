// Package sharemigration moves every wallet's share B from one Secrets Manager to
// another (LocalStack to AWS, or back) and points the wallet row at the copy. It
// is a custody service: it holds share bytes, only in memory, and zeroes them after
// use. The source is never written, and an ARN only moves after the bytes in the
// target are proven equal to the source's: a wallet whose share is lost cannot sign.
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
	StatusInPlace      = "in place"      // the row points at the target's copy, which reads back non-empty
	StatusWouldPoint   = "would point"   // the target has an equal copy; the row would move to it
	StatusWouldMigrate = "would migrate" // the target has no copy; one would be created
	StatusPointed      = "pointed"       // the row moved to the target's equal copy
	StatusMigrated     = "migrated"      // a copy was created, read back, and the row moved to it
	StatusFailed       = "failed"
)

var (
	// ErrSameStore is a run whose source is the target: every row would read
	// "in place" while nothing moved.
	ErrSameStore = errors.New("the source and the target are the same Secrets Manager")
	// ErrUnsealedTarget is --apply into AWS without a master key: the copies would be
	// sealed with Secrets Manager's default key, which the server user cannot change.
	ErrUnsealedTarget = errors.New("MASTER_KEY_REF names no KMS key; refusing to write shares into AWS unsealed")
	// ErrEmptyShare is a share with no bytes, in the source or in the target.
	ErrEmptyShare = errors.New("the share is empty")
	// ErrConflict is a target secret under the share's name holding other bytes. It is
	// never overwritten.
	ErrConflict = errors.New("the target holds a different secret under the share name")
	// ErrReadBack is a created copy that did not read back as the bytes written.
	ErrReadBack = errors.New("the target returned different bytes than were written")
)

// Wallets lists the wallets and moves the ARN of their share B. SetMPCSecretARN
// only moves a row that still holds from.
type Wallets interface {
	FindAll(ctx context.Context) ([]models.Wallet, error)
	SetMPCSecretARN(ctx context.Context, id uuid.UUID, from, to string) error
}

// Source reads a share by the ARN the wallet row holds.
type Source interface {
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// Sources opens the Secrets Manager at endpoint (empty: AWS) with the credentials
// of profile (empty: the app's own).
type Sources func(endpoint, profile string) (Source, error)

// Target is where the shares go: the Secrets Manager the app itself uses.
type Target interface {
	Lookup(ctx context.Context, name string) (arn string, found bool, err error)
	Create(ctx context.Context, name string, secret []byte) (string, error)
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// Deps wires the migration. TargetEndpoint and TargetKMSKeyID describe Target
// (vault.aws.endpoint_url, empty for AWS, and the key MASTER_KEY_REF names).
type Deps struct {
	Wallets        Wallets
	Sources        Sources
	Target         Target
	TargetEndpoint string
	TargetKMSKeyID string
}

// Service moves shares into Target.
type Service struct {
	deps Deps
}

// NewService builds the migration from deps.
func NewService(deps Deps) *Service {
	return &Service{deps: deps}
}

// Request is one run: where the shares are read from, and whether to write.
type Request struct {
	SourceEndpoint string
	SourceProfile  string
	Apply          bool
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

// Report is a run's outcome: where the shares went, and one row per wallet.
type Report struct {
	TargetEndpoint string
	TargetKMSKeyID string
	Rows           []Row
}

// Run moves every wallet's share B from the request's source into the target.
// Without Apply it only reports. Every wallet is tried; the error joins the failed
// ones. Running again is safe: a copy created before a failed ARN update is found
// and pointed at.
func (s *Service) Run(ctx context.Context, req Request) (Report, error) {
	report := Report{TargetEndpoint: s.deps.TargetEndpoint, TargetKMSKeyID: s.deps.TargetKMSKeyID}
	if req.SourceEndpoint == s.deps.TargetEndpoint {
		return report, ErrSameStore
	}
	if req.Apply && s.deps.TargetEndpoint == "" && s.deps.TargetKMSKeyID == "" {
		return report, ErrUnsealedTarget
	}
	source, err := s.deps.Sources(req.SourceEndpoint, req.SourceProfile)
	if err != nil {
		return report, fmt.Errorf("open the source: %w", err)
	}
	wallets, err := s.deps.Wallets.FindAll(ctx)
	if err != nil {
		return report, fmt.Errorf("list wallets: %w", err)
	}
	var failures []error
	for _, w := range wallets {
		if w.MPCSecretARN == "" {
			continue
		}
		row := s.migrate(ctx, source, w, req.Apply)
		if row.Err != nil {
			failures = append(failures, fmt.Errorf("wallet %s: %w", w.ID, row.Err))
		}
		report.Rows = append(report.Rows, row)
	}
	return report, errors.Join(failures...)
}

func (s *Service) migrate(ctx context.Context, source Source, w models.Wallet, apply bool) Row {
	row := Row{WalletID: w.ID, Chain: w.Chain, From: w.MPCSecretARN}
	name := wallet.ShareBSecretName(w.ID)

	targetARN, found, err := s.deps.Target.Lookup(ctx, name)
	if err != nil {
		return failed(row, fmt.Errorf("look up %s in the target: %w", name, err))
	}
	if found && targetARN == w.MPCSecretARN {
		return s.confirmInPlace(ctx, row, targetARN)
	}

	share, err := source.Binary(ctx, w.MPCSecretARN)
	defer clear(share)
	if err != nil {
		return failed(row, fmt.Errorf("read the source share: %w", err))
	}
	if len(share) == 0 {
		return failed(row, ErrEmptyShare)
	}

	if found {
		return s.pointAtExisting(ctx, row, w.ID, targetARN, share, apply)
	}
	if !apply {
		row.Status = StatusWouldMigrate
		return row
	}

	createdARN, err := s.deps.Target.Create(ctx, name, share)
	if err != nil {
		return failed(row, fmt.Errorf("create %s in the target: %w", name, err))
	}
	row.To = createdARN
	back, err := s.deps.Target.Binary(ctx, createdARN)
	defer clear(back)
	if err != nil {
		return failed(row, fmt.Errorf("read back %s: %w", name, err))
	}
	if !bytes.Equal(back, share) {
		return failed(row, ErrReadBack)
	}
	if err := s.deps.Wallets.SetMPCSecretARN(ctx, w.ID, w.MPCSecretARN, createdARN); err != nil {
		return failed(row, fmt.Errorf("point the wallet at the copy: %w", err))
	}
	row.Status = StatusMigrated
	return row
}

// confirmInPlace reads the copy the row already points at: "in place" means the
// target can open it, not only that the ARN matches.
func (s *Service) confirmInPlace(ctx context.Context, row Row, targetARN string) Row {
	row.To = targetARN
	share, err := s.deps.Target.Binary(ctx, targetARN)
	defer clear(share)
	if err != nil {
		return failed(row, fmt.Errorf("read the target copy: %w", err))
	}
	if len(share) == 0 {
		return failed(row, ErrEmptyShare)
	}
	row.Status = StatusInPlace
	return row
}

func (s *Service) pointAtExisting(ctx context.Context, row Row, walletID uuid.UUID, targetARN string, share []byte, apply bool) Row {
	row.To = targetARN
	existing, err := s.deps.Target.Binary(ctx, targetARN)
	defer clear(existing)
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
	if err := s.deps.Wallets.SetMPCSecretARN(ctx, walletID, row.From, targetARN); err != nil {
		return failed(row, fmt.Errorf("point the wallet at the copy: %w", err))
	}
	row.Status = StatusPointed
	return row
}

func failed(row Row, err error) Row {
	row.Status, row.Err = StatusFailed, err
	return row
}

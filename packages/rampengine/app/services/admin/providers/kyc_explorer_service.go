package providers

import (
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	customersdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/customers"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/providers"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// ErrKycExplorerNotFound is returned when no integration matches the explorer id.
var ErrKycExplorerNotFound = errors.New("kyc explorer row not found")

// HolderMembers is who acts for each holder — its owner and how many members
// it has. The admin customers service implements it, the one reader of a
// customer's members and their roles.
type HolderMembers interface {
	MembersSummary(ids []uint) (map[uint]customersdto.MembersSummaryDTO, error)
}

// KycExplorerService drives the cross-holder compliance explorer.
type KycExplorerService struct {
	integRepo repositories.CustomerProviderIntegrationRepository
	// profiles names a company holding a row by its legal name.
	profiles repositories.CustomerBusinessProfileRepository
	// members says who acts for each holder listed.
	members HolderMembers
	// factory declares the documents each provider asks with an application,
	// part of what kycsvc.Terminal reads off a marked row.
	factory *ramp.Factory
}

// NewKycExplorerService wires the explorer service.
func NewKycExplorerService(integRepo repositories.CustomerProviderIntegrationRepository,
	profiles repositories.CustomerBusinessProfileRepository, members HolderMembers, factory *ramp.Factory) *KycExplorerService {
	return &KycExplorerService{integRepo: integRepo, profiles: profiles, members: members, factory: factory}
}

// List returns paginated explorer rows plus total count, normalizing page
// (>=1) and per-page (1..100, default 20).
func (s *KycExplorerService) List(d dto.AdminListKycDTO) ([]dto.KycExplorerRowDTO, int64, error) {
	filters := repositories.AdminKycListFilters{
		Provider:          d.Provider,
		KycStatus:         d.KycStatus,
		CustomerType:      d.CustomerType,
		CustomerStatus:    d.CustomerStatus,
		TosStatus:         d.TosStatus,
		CollectionMode:    d.CollectionMode,
		IntegrationStatus: d.IntegrationStatus,
		Rail:              d.Rail,
		Search:            d.Search,
		SyncedBefore:      d.SyncedBefore,
		RejectionCategory: d.RejectionCategory,
		Terminal:          optionalBool(d.Terminal),
		HasError:          optionalBool(d.HasError),
		Page:              d.Page,
		PerPage:           d.PerPage,
		Sort:              d.Sort,
	}
	if filters.Terminal != nil {
		ids, err := s.terminalUnclassified()
		if err != nil {
			return nil, 0, err
		}
		filters.TerminalKycIDs = ids
	}
	rows, total, err := s.integRepo.AdminPaginate(filters)
	if err != nil {
		return nil, 0, err
	}
	s.mark(rows)
	out, err := s.explorerRowDTOs(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// terminalUnclassified is which of the rejections no adapter classified
// kycsvc.Terminal calls terminal — by what is left to send them, which the
// query cannot read. The rows a terminal category decides the query finds
// itself.
func (s *KycExplorerService) terminalUnclassified() ([]uint, error) {
	candidates, err := s.integRepo.AdminUnclassifiedRejections()
	if err != nil {
		return nil, err
	}
	s.mark(candidates)
	var ids []uint
	for i := range candidates {
		if candidates[i].Kyc != nil && kycsvc.Terminal(candidates[i].Kyc) {
			ids = append(ids, candidates[i].Kyc.ID)
		}
	}
	return ids, nil
}

// mark sets what each row owes from its own link, as the client's listing
// does, so the terminal it renders is the one the app computes.
func (s *KycExplorerService) mark(rows []repositories.AdminKycRow) {
	for i := range rows {
		if rows[i].Kyc == nil || rows[i].Integration == nil {
			continue
		}
		marked := []models.CustomerProviderKyc{*rows[i].Kyc}
		kycsvc.MarkNeedsApplication(marked, []models.CustomerProviderIntegration{*rows[i].Integration}, s.factory)
		*rows[i].Kyc = marked[0]
	}
}

// Get loads one explorer row by integration id.
func (s *KycExplorerService) Get(id uint) (*dto.KycExplorerRowDTO, error) {
	row, err := s.integRepo.FindByIDForAdmin(id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrKycExplorerNotFound
	}
	marked := []repositories.AdminKycRow{*row}
	s.mark(marked)
	out, err := s.explorerRowDTOs(marked)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// optionalBool reads a validated "true"/"false" query value; anything else,
// the empty string included, is no filter.
func optionalBool(raw string) *bool {
	switch raw {
	case "true":
		value := true
		return &value
	case "false":
		value := false
		return &value
	}
	return nil
}

// explorerRowDTOs hands the repository's rows over as the service's own type
// — resources never take a repositories type (controllers-and-services.md) —
// each company holding a row named by its legal name, and each holder with
// who acts for it, read in one go each.
func (s *KycExplorerService) explorerRowDTOs(rows []repositories.AdminKycRow) ([]dto.KycExplorerRowDTO, error) {
	var holders, businesses []uint
	for i := range rows {
		holder := rows[i].Customer
		if holder == nil {
			continue
		}
		holders = append(holders, holder.ID)
		if holder.Type == models.CustomerTypeBusiness {
			businesses = append(businesses, holder.ID)
		}
	}
	profiles := map[uint]models.BusinessProfile{}
	if len(businesses) > 0 {
		var err error
		if profiles, err = s.profiles.FindByCustomers(businesses); err != nil {
			return nil, fmt.Errorf("admin kyc explorer: business names: %w", err)
		}
	}
	members := map[uint]customersdto.MembersSummaryDTO{}
	if len(holders) > 0 {
		var err error
		if members, err = s.members.MembersSummary(holders); err != nil {
			return nil, fmt.Errorf("admin kyc explorer: holders' members: %w", err)
		}
	}
	out := make([]dto.KycExplorerRowDTO, len(rows))
	for i := range rows {
		out[i] = dto.KycExplorerRowDTO{
			Integration: rows[i].Integration, Kyc: rows[i].Kyc, Customer: rows[i].Customer, User: rows[i].User,
		}
		if holder := rows[i].Customer; holder != nil {
			out[i].LegalName = profiles[holder.ID].LegalName
			out[i].Members = members[holder.ID]
		}
	}
	return out, nil
}

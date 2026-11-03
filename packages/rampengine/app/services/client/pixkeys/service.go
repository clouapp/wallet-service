package pixkeys

import (
	"fmt"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// Errors a caller maps to a status. They are the repository's values, so
// errors.Is matches them through the service's wrapping.
var (
	// ErrPixKeyNotFound is returned for an id that does not exist or belongs
	// to another user; the two are indistinguishable on purpose.
	ErrPixKeyNotFound = repositories.ErrPixKeyNotFound
	// ErrDuplicatePixKey is returned when the user already saved the same
	// normalized key.
	ErrDuplicatePixKey = repositories.ErrDuplicatePixKey
)

// Service is the business-logic boundary between PixKeysController and
// UserPixKeyRepository.
type Service struct {
	repo repositories.UserPixKeyRepository
}

// NewService wires the service with its repository dependency.
func NewService(repo repositories.UserPixKeyRepository) *Service {
	return &Service{repo: repo}
}

// List returns every Pix key the given user has saved.
func (s *Service) List(userID uint) ([]dto.PixKeyDTO, error) {
	views, err := s.repo.ListByUser(userID)
	if err != nil {
		return nil, fmt.Errorf("pix keys: list: %w", err)
	}
	keys := make([]dto.PixKeyDTO, 0, len(views))
	for i := range views {
		keys = append(keys, toDTO(&views[i]))
	}
	return keys, nil
}

// Create registers a new Pix key for the user.
func (s *Service) Create(userID uint, d dto.CreatePixKeyDTO) (*dto.PixKeyDTO, error) {
	view, err := s.repo.Create(userID, toInput(d))
	if err != nil {
		return nil, fmt.Errorf("pix keys: create: %w", err)
	}
	key := toDTO(view)
	return &key, nil
}

// Update edits an existing Pix key owned by the user.
func (s *Service) Update(userID, id uint, d dto.UpdatePixKeyDTO) (*dto.PixKeyDTO, error) {
	view, err := s.repo.Update(userID, id, toInput(dto.CreatePixKeyDTO(d)))
	if err != nil {
		return nil, fmt.Errorf("pix keys: update %d: %w", id, err)
	}
	key := toDTO(view)
	return &key, nil
}

// Delete removes a single Pix key owned by the user.
func (s *Service) Delete(userID, id uint) error {
	if err := s.repo.Delete(userID, id); err != nil {
		return fmt.Errorf("pix keys: delete %d: %w", id, err)
	}
	return nil
}

// toInput builds the repository input shared by create and update, deriving
// the hash duplicate detection compares.
func toInput(d dto.CreatePixKeyDTO) repositories.UserPixKeyInput {
	input := repositories.UserPixKeyInput{
		Label:         d.Label,
		PixKey:        d.PixKey,
		PixKeyType:    d.PixKeyType,
		RecipientName: d.RecipientName,
		PixKeyHash:    HashKey(d.PixKeyType, d.PixKey),
	}
	if d.RecipientDocument != "" {
		input.RecipientDocument = &d.RecipientDocument
	}
	return input
}

func toDTO(view *repositories.UserPixKeyView) dto.PixKeyDTO {
	return dto.PixKeyDTO{
		ID:                view.ID,
		Label:             view.Label,
		PixKey:            view.PixKey,
		PixKeyType:        view.PixKeyType,
		RecipientName:     view.RecipientName,
		RecipientDocument: view.RecipientDocument,
	}
}

package pixkeys

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func TestServiceCreateHashesBeforeDelegating(t *testing.T) {
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().Create(uint(7), mock.MatchedBy(func(in repositories.UserPixKeyInput) bool {
		return in.PixKeyHash == HashKey("cpf", "390.533.447-05")
	})).Return(&repositories.UserPixKeyView{ID: 1}, nil).Once()

	_, err := NewService(repo).Create(7, dto.CreatePixKeyDTO{
		PixKeyType: "cpf",
		PixKey:     "390.533.447-05",
	})
	assert.NoError(t, err)
}

func TestServiceUpdateHashesBeforeDelegating(t *testing.T) {
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().Update(uint(7), uint(3), mock.MatchedBy(func(in repositories.UserPixKeyInput) bool {
		return in.PixKeyHash == HashKey("email", "ana.costa@example.com")
	})).Return(&repositories.UserPixKeyView{ID: 3}, nil).Once()

	_, err := NewService(repo).Update(7, 3, dto.UpdatePixKeyDTO{
		PixKeyType: "email",
		PixKey:     "ana.costa@example.com",
	})
	assert.NoError(t, err)
}

func TestServiceCreateMapsInputAndResult(t *testing.T) {
	doc := "39053344705"
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().Create(uint(7), mock.MatchedBy(func(in repositories.UserPixKeyInput) bool {
		return in.Label == "Aluguel" && in.PixKey == "ana.costa@example.com" && in.PixKeyType == "email" &&
			in.RecipientName == "Ana Costa" && in.RecipientDocument != nil && *in.RecipientDocument == doc
	})).Return(&repositories.UserPixKeyView{
		ID: 5, Label: "Aluguel", PixKey: "ana.costa@example.com", PixKeyType: "email",
		RecipientName: "Ana Costa", RecipientDocument: &doc,
	}, nil).Once()

	key, err := NewService(repo).Create(7, dto.CreatePixKeyDTO{
		Label: "Aluguel", PixKey: "ana.costa@example.com", PixKeyType: "email",
		RecipientName: "Ana Costa", RecipientDocument: doc,
	})
	assert.NoError(t, err)
	assert.Equal(t, &dto.PixKeyDTO{
		ID: 5, Label: "Aluguel", PixKey: "ana.costa@example.com", PixKeyType: "email",
		RecipientName: "Ana Costa", RecipientDocument: &doc,
	}, key)
}

func TestServiceUpdateSendsAbsentRecipientDocumentAsNil(t *testing.T) {
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().Update(uint(7), uint(3), mock.MatchedBy(func(in repositories.UserPixKeyInput) bool {
		return in.RecipientDocument == nil
	})).Return(&repositories.UserPixKeyView{ID: 3}, nil).Once()

	key, err := NewService(repo).Update(7, 3, dto.UpdatePixKeyDTO{PixKeyType: "cpf", PixKey: "39053344705"})
	assert.NoError(t, err)
	assert.Nil(t, key.RecipientDocument)
}

func TestServiceListMapsEveryKey(t *testing.T) {
	doc := "39053344705"
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().ListByUser(uint(7)).Return([]repositories.UserPixKeyView{
		{ID: 1, Label: "A", PixKey: "k1", PixKeyType: "cpf", RecipientName: "N1"},
		{ID: 2, Label: "B", PixKey: "k2", PixKeyType: "email", RecipientName: "N2", RecipientDocument: &doc},
	}, nil).Once()

	keys, err := NewService(repo).List(7)
	assert.NoError(t, err)
	assert.Equal(t, []dto.PixKeyDTO{
		{ID: 1, Label: "A", PixKey: "k1", PixKeyType: "cpf", RecipientName: "N1"},
		{ID: 2, Label: "B", PixKey: "k2", PixKeyType: "email", RecipientName: "N2", RecipientDocument: &doc},
	}, keys)
}

func TestServiceSurfacesRepositoryOutcomesAsItsOwnErrors(t *testing.T) {
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().Create(uint(7), mock.Anything).
		Return(nil, fmt.Errorf("unique violation: %w", repositories.ErrDuplicatePixKey)).Once()
	repo.EXPECT().Update(uint(7), uint(3), mock.Anything).Return(nil, repositories.ErrPixKeyNotFound).Once()
	repo.EXPECT().Delete(uint(7), uint(4)).Return(repositories.ErrPixKeyNotFound).Once()
	svc := NewService(repo)

	_, err := svc.Create(7, dto.CreatePixKeyDTO{PixKeyType: "cpf", PixKey: "39053344705"})
	assert.ErrorIs(t, err, ErrDuplicatePixKey)
	_, err = svc.Update(7, 3, dto.UpdatePixKeyDTO{PixKeyType: "cpf", PixKey: "39053344705"})
	assert.ErrorIs(t, err, ErrPixKeyNotFound)
	assert.ErrorIs(t, svc.Delete(7, 4), ErrPixKeyNotFound)
}

func TestServiceDeleteIsScopedToTheOwner(t *testing.T) {
	repo := mocks.NewMockUserPixKeyRepository(t)
	repo.EXPECT().Delete(uint(7), uint(4)).Return(nil).Once()

	assert.NoError(t, NewService(repo).Delete(7, 4))
}

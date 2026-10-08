package wallet

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type memWallets struct {
	mu   sync.Mutex
	rows map[uuid.UUID]models.Wallet
}

func newMemWallets() *memWallets {
	return &memWallets{rows: map[uuid.UUID]models.Wallet{}}
}

func (m *memWallets) Create(_ context.Context, wallet *models.Wallet) error {
	if wallet == nil {
		return fmt.Errorf("create wallet: wallet is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[wallet.ID] = *wallet
	return nil
}

func (m *memWallets) FindByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	return &wallet, nil
}

func (m *memWallets) FindAll(context.Context) ([]models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.Wallet, 0, len(m.rows))
	for _, wallet := range m.rows {
		out = append(out, wallet)
	}
	return out, nil
}

func (m *memWallets) IncrementAddressIndex(_ context.Context, id uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.rows[id]
	if !ok {
		return 0, fmt.Errorf("wallet not found")
	}
	wallet.AddressIndex++
	m.rows[id] = wallet
	return wallet.AddressIndex, nil
}

func (m *memWallets) SetDepositAddressID(_ context.Context, id, addressID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.rows[id]
	if !ok {
		return fmt.Errorf("wallet not found")
	}
	wallet.DepositAddressID = &addressID
	m.rows[id] = wallet
	return nil
}

func (m *memWallets) SetMPCChainCode(_ context.Context, id uuid.UUID, chainCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.rows[id]
	if !ok {
		return fmt.Errorf("wallet not found")
	}
	wallet.MPCChainCode = chainCode
	m.rows[id] = wallet
	return nil
}

func (m *memWallets) Activate(_ context.Context, id uuid.UUID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.rows[id]
	if !ok {
		return fmt.Errorf("wallet not found")
	}
	wallet.Status = status
	wallet.ActivationCode = nil
	m.rows[id] = wallet
	return nil
}

type memAddresses struct {
	mu   sync.Mutex
	rows map[uuid.UUID]models.Address
}

func newMemAddresses() *memAddresses {
	return &memAddresses{rows: map[uuid.UUID]models.Address{}}
}

func (m *memAddresses) Create(_ context.Context, addr *models.Address) error {
	if addr == nil {
		return fmt.Errorf("create address: address is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[addr.ID] = *addr
	return nil
}

func (m *memAddresses) FindByID(_ context.Context, id uuid.UUID) (*models.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	return &addr, nil
}

func (m *memAddresses) SetLabel(_ context.Context, id uuid.UUID, label string) error {
	return m.update(id, func(addr *models.Address) { addr.Label = label })
}

func (m *memAddresses) SetExternalUserID(_ context.Context, id uuid.UUID, externalUserID string) error {
	return m.update(id, func(addr *models.Address) { addr.ExternalUserID = externalUserID })
}

func (m *memAddresses) update(id uuid.UUID, fn func(*models.Address)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr, ok := m.rows[id]
	if !ok {
		return fmt.Errorf("address not found")
	}
	fn(&addr)
	m.rows[id] = addr
	return nil
}

func (m *memAddresses) FindByChainAndAddress(_ context.Context, chainID, address string) (*models.Address, error) {
	return m.find(func(addr models.Address) bool {
		return addr.Chain == chainID && addr.Address == address
	})
}

func (m *memAddresses) FindByChainAndAddressAndAccount(_ context.Context, chainID, address string, _ uuid.UUID) (*models.Address, error) {
	return m.find(func(addr models.Address) bool {
		return addr.Chain == chainID && addr.Address == address
	})
}

func (m *memAddresses) FindByExternalUserID(_ context.Context, externalUserID string) ([]models.Address, error) {
	return m.list(func(addr models.Address) bool { return addr.ExternalUserID == externalUserID })
}

func (m *memAddresses) FindByExternalUserIDAndAccount(_ context.Context, externalUserID string, _ uuid.UUID) ([]models.Address, error) {
	return m.list(func(addr models.Address) bool { return addr.ExternalUserID == externalUserID })
}

func (m *memAddresses) FindByWalletID(_ context.Context, walletID uuid.UUID) ([]models.Address, error) {
	return m.list(func(addr models.Address) bool { return addr.WalletID == walletID })
}

func (m *memAddresses) find(match func(models.Address) bool) (*models.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, addr := range m.rows {
		if match(addr) {
			copied := addr
			return &copied, nil
		}
	}
	return nil, nil
}

func (m *memAddresses) list(match func(models.Address) bool) ([]models.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.Address
	for _, addr := range m.rows {
		if match(addr) {
			out = append(out, addr)
		}
	}
	return out, nil
}

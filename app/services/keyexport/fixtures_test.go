package keyexport

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	walletsvc "github.com/macrowallets/waas/app/services/wallet"
)

const (
	testWalletPassphrase = "fixture-passphrase-not-real-1"
	testArchivePassword  = "Zip-Fixture-Password-9f3c"
	testSecretARN        = "arn:aws:secretsmanager:us-east-1:000000000000:secret:test"
)

// secp256k1TestKeys runs one real 2-of-2 ECDSA keygen for the package: the ceremony
// is slow and the tests only need some real shares.
var secp256k1TestKeys = sync.OnceValues(func() (*mpcpkg.KeygenResult, error) {
	return mpcpkg.NewTSSService().Keygen(context.Background(), mpcpkg.CurveSecp256k1)
})

func mustSecp256k1Keys(t *testing.T) *mpcpkg.KeygenResult {
	t.Helper()
	keys, err := secp256k1TestKeys()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func mustEd25519Keys(t *testing.T) *mpcpkg.KeygenResult {
	t.Helper()
	keys, err := mpcpkg.NewTSSService().Keygen(context.Background(), mpcpkg.CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// walletFixture is a wallet built like production: share A encrypted with the
// passphrase, genesis row at creation, children issued by the real wallet service.
type walletFixture struct {
	wallet    models.Wallet
	addresses []models.Address
	shareA    []byte
	shareB    []byte
	network   Network
}

type fixtureWalletRepo struct {
	repositories.WalletRepository
	wallet *models.Wallet
	next   int
}

func (r *fixtureWalletRepo) FindByID(uuid.UUID) (*models.Wallet, error) { return r.wallet, nil }

func (r *fixtureWalletRepo) IncrementAddressIndex(uuid.UUID) (int, error) {
	r.next++
	return r.next, nil
}

type fixtureAddressRepo struct {
	repositories.AddressRepository
	created []models.Address
}

func (r *fixtureAddressRepo) Create(address *models.Address) error {
	r.created = append(r.created, *address)
	return nil
}

type fixtureSecrets struct{ shareB []byte }

func (f fixtureSecrets) CreateSecret(context.Context, *secretsmanager.CreateSecretInput, ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	return nil, fmt.Errorf("not used")
}

func (f fixtureSecrets) GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return &secretsmanager.GetSecretValueOutput{SecretBinary: bytes.Clone(f.shareB)}, nil
}

func newWalletFixture(t *testing.T, keys *mpcpkg.KeygenResult, curve mpcpkg.Curve, network Network, label string, children int) walletFixture {
	t.Helper()
	encrypted, err := mpcpkg.EncryptShare(keys.ShareA, testWalletPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	walletID := uuid.New()
	genesisAddress, err := addressing.DeriveAddressOnNetwork(network.ChainID, network.Testnet, keys.CombinedPubKey)
	if err != nil {
		t.Fatal(err)
	}
	genesis := models.Address{
		ID: uuid.New(), WalletID: walletID, Chain: network.ChainID, Address: genesisAddress,
		DerivationIndex: 0, ExternalUserID: "system", IsActive: true, Label: "Deposit Address", DerivationType: DerivationGenesis,
	}
	wallet := models.Wallet{
		ID: walletID, Chain: network.ChainID, Label: label,
		MPCCustomerShare: hex.EncodeToString(encrypted.Ciphertext), MPCShareIV: hex.EncodeToString(encrypted.IV),
		MPCShareSalt: hex.EncodeToString(encrypted.Salt), MPCSecretARN: testSecretARN,
		MPCPublicKey: hex.EncodeToString(keys.CombinedPubKey), MPCCurve: string(curve),
		MPCChainCode: hex.EncodeToString(keys.ChainCode), Status: "active",
		DepositAddressID: &genesis.ID, DepositAddress: &genesis,
	}

	walletRepo := &fixtureWalletRepo{wallet: &wallet}
	addressRepo := &fixtureAddressRepo{}
	issuer := walletsvc.NewService(registryFor(network), nil, mpcpkg.NewTSSService(), fixtureSecrets{shareB: keys.ShareB}, walletRepo, addressRepo)
	for i := 0; i < children; i++ {
		if _, err := issuer.GenerateAddress(context.Background(), walletID, fmt.Sprintf("user-%d", i), "", "{}", testWalletPassphrase); err != nil {
			t.Fatal(err)
		}
	}
	return walletFixture{
		wallet: wallet, addresses: append([]models.Address{genesis}, addressRepo.created...),
		shareA: bytes.Clone(keys.ShareA), shareB: bytes.Clone(keys.ShareB), network: network,
	}
}

// registryFor gives Bitcoin the adapter that decides tb1 vs bc1 for child addresses.
func registryFor(network Network) *chain.Registry {
	registry := chain.NewRegistry()
	if network.AdapterType == models.AdapterTypeBitcoin {
		registry.RegisterChain(chain.NewBitcoinLive(chain.BitcoinConfig{
			ChainIDStr: network.ChainID, RPCURL: "http://127.0.0.1:9", IsTestnet: network.Testnet,
		}))
	}
	return registry
}

var (
	baseSepolia  = Network{ChainID: models.ChainBase, AdapterType: models.AdapterTypeEVM, Name: models.NetworkBaseSepolia, Testnet: true}
	bscMainnet   = Network{ChainID: models.ChainBSC, AdapterType: models.AdapterTypeEVM, Name: models.NetworkBSCMainnet, Testnet: false}
	bitcoinTest  = Network{ChainID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin, Name: models.NetworkBitcoinTestnet, Testnet: true}
	bitcoinMain  = Network{ChainID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin, Name: models.NetworkBitcoinMainnet, Testnet: false}
	solanaDevnet = Network{ChainID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, Name: models.NetworkSolanaDevnet, Testnet: true}
	litecoinTest = Network{ChainID: models.ChainLTC, AdapterType: models.AdapterTypeBitcoin, Name: models.NetworkLitecoinTestnet, Testnet: true}
	litecoinMain = Network{ChainID: models.ChainLTC, AdapterType: models.AdapterTypeBitcoin, Name: models.NetworkLitecoinMainnet, Testnet: false}
	tronNile     = Network{ChainID: models.ChainTron, AdapterType: models.AdapterTypeTron, Name: models.NetworkTronNile, Testnet: true}
)

// fakeStore serves fixtures as the database, Secrets Manager and network registry.
type fakeStore struct {
	wallets   []models.Wallet
	addresses map[uuid.UUID][]models.Address
	byChain   map[string]Network
	shareB    map[uuid.UUID][]byte
}

func newFakeStore(fixtures ...walletFixture) *fakeStore {
	store := &fakeStore{
		addresses: map[uuid.UUID][]models.Address{}, byChain: map[string]Network{}, shareB: map[uuid.UUID][]byte{},
	}
	for _, fixture := range fixtures {
		store.wallets = append(store.wallets, fixture.wallet)
		store.addresses[fixture.wallet.ID] = fixture.addresses
		store.byChain[fixture.wallet.Chain] = fixture.network
		store.shareB[fixture.wallet.ID] = fixture.shareB
	}
	return store
}

func (s *fakeStore) FindAll() ([]models.Wallet, error) { return s.wallets, nil }

func (s *fakeStore) FindByID(id uuid.UUID) (*models.Wallet, error) {
	for i := range s.wallets {
		if s.wallets[i].ID == id {
			wallet := s.wallets[i]
			return &wallet, nil
		}
	}
	return nil, nil
}

func (s *fakeStore) FindByWalletID(walletID uuid.UUID) ([]models.Address, error) {
	return append([]models.Address{}, s.addresses[walletID]...), nil
}

func (s *fakeStore) ResolveNetwork(chainID string) (Network, error) {
	network, ok := s.byChain[chainID]
	if !ok {
		return Network{}, fmt.Errorf("chain %s not registered", chainID)
	}
	return network, nil
}

func (s *fakeStore) FetchShareB(_ context.Context, wallet models.Wallet) ([]byte, error) {
	share, ok := s.shareB[wallet.ID]
	if !ok {
		return nil, fmt.Errorf("no secret")
	}
	return bytes.Clone(share), nil
}

func (s *fakeStore) service(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(Dependencies{Wallets: s, Addresses: s, Networks: s, ShareB: s, MPC: mpcpkg.NewTSSService()})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type fixedPassphrase string

func (p fixedPassphrase) MaxAttempts() int { return 1 }

func (p fixedPassphrase) Passphrase(context.Context, models.Wallet, int) (string, error) {
	return string(p), nil
}

// scriptedTerminal answers prompts from a script; an exhausted script aborts.
type scriptedTerminal struct {
	secrets [][]byte
	lines   []string
	notices []string
	prompts []string
}

func (s *scriptedTerminal) ReadSecret(prompt string) ([]byte, error) {
	s.prompts = append(s.prompts, prompt)
	if len(s.secrets) == 0 {
		return nil, ErrAborted
	}
	next := s.secrets[0]
	s.secrets = s.secrets[1:]
	return bytes.Clone(next), nil
}

func (s *scriptedTerminal) ReadLine(prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if len(s.lines) == 0 {
		return "", ErrAborted
	}
	next := s.lines[0]
	s.lines = s.lines[1:]
	return next, nil
}

func (s *scriptedTerminal) Notify(message string) { s.notices = append(s.notices, message) }

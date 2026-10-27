package sweep

import (
	"context"
	"math/big"
	"testing"

	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type recoverySigningTestChain struct{ *evmSigningChain }

func (c *recoverySigningTestChain) BuildCall(call chain.EVMCall) (*types.UnsignedTx, error) {
	return c.live.BuildCall(call)
}

func recoveryExactTestCall() chain.EVMCall {
	return chain.EVMCall{Nonce: 0, To: evmCallTestInbox, Value: big.NewInt(0), Data: []byte{0xa9, 0x05, 0x9c, 0xbb}, GasLimit: 120000, GasPrice: big.NewInt(2000000000)}
}

func TestRecoverySigning_UsesTheExistingChildAndExactNonceZeroWithoutSending(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := &recoverySigningTestChain{newEVMSigningChain(t, &broadcasts)}
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	fixture.child.IsActive = true
	svc, rows := preflightService(t, fixture, adapter)
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{fixture.child}}
	call := recoveryExactTestCall()
	signed, err := svc.SignEVMRecoveryCall(context.Background(), fixture.wallet.ID, fixture.child.ID, sepoliaNetworkID, call, preflightTestPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	assertEVMSentFrom(t, signed, fixture.child.Address)
	var decoded gethtypes.Transaction
	if err := decoded.UnmarshalBinary(signed.RawBytes); err != nil {
		t.Fatal(err)
	}
	if decoded.Nonce() != 0 || len(broadcasts) != 0 || len(rows.created) != 0 {
		t.Fatalf("nonce=%d broadcasts=%d rows=%d", decoded.Nonce(), len(broadcasts), len(rows.created))
	}
	for name, alter := range map[string]func(*chain.EVMCall){
		"nonce":     func(c *chain.EVMCall) { c.Nonce = 1 },
		"recipient": func(c *chain.EVMCall) { c.To = fixture.child.Address },
		"value":     func(c *chain.EVMCall) { c.Value = big.NewInt(1) },
		"data":      func(c *chain.EVMCall) { c.Data = []byte{1} },
		"gas limit": func(c *chain.EVMCall) { c.GasLimit++ },
		"gas price": func(c *chain.EVMCall) { c.GasPrice = big.NewInt(1) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := recoveryExactTestCall()
			alter(&changed)
			if err := verifyRecoveryCall(signed, fixture.child.Address, sepoliaNetworkID, changed); err == nil {
				t.Fatal("accepted a signed call with changed reviewed semantics")
			}
		})
	}
	if err := verifyRecoveryCall(signed, fixture.wallet.DepositAddress.Address, sepoliaNetworkID, call); err == nil {
		t.Fatal("accepted the base as the child sender")
	}
	if err := verifyRecoveryCall(signed, fixture.child.Address, evmCallTestOtherNetworkID, call); err == nil {
		t.Fatal("accepted another network")
	}
}

func TestRecoverySigning_RejectsAddressAndNetworkMismatchBeforeRetrievingKeys(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := &recoverySigningTestChain{newEVMSigningChain(t, &broadcasts)}
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	fixture.child.IsActive = true
	svc, rows := preflightService(t, fixture, adapter)
	keys := 0
	svc.fetchShareBFn = func(context.Context, *models.Wallet) ([]byte, error) {
		keys++
		return append([]byte(nil), fixture.shareB...), nil
	}
	for _, tc := range []struct {
		name    string
		address models.Address
		id      uuid.UUID
		network int64
	}{
		{name: "absent", address: fixture.child, id: uuid.New(), network: sepoliaNetworkID},
		{name: "foreign wallet", address: func() models.Address { a := fixture.child; a.WalletID = uuid.New(); return a }(), id: fixture.child.ID, network: sepoliaNetworkID},
		{name: "foreign chain", address: func() models.Address { a := fixture.child; a.Chain = models.ChainPolygon; return a }(), id: fixture.child.ID, network: sepoliaNetworkID},
		{name: "inactive", address: func() models.Address { a := fixture.child; a.IsActive = false; return a }(), id: fixture.child.ID, network: sepoliaNetworkID},
		{name: "different network", address: fixture.child, id: fixture.child.ID, network: evmCallTestOtherNetworkID},
		{name: "zero network", address: fixture.child, id: fixture.child.ID},
		{name: "untracked derivation", address: func() models.Address { a := fixture.child; a.DerivationIndex = 0; return a }(), id: fixture.child.ID, network: sepoliaNetworkID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc.addressRepo = &fakeAddressRepo{children: []models.Address{tc.address}}
			if _, err := svc.SignEVMRecoveryCall(context.Background(), fixture.wallet.ID, tc.id, tc.network, recoveryExactTestCall(), preflightTestPassphrase); err == nil {
				t.Fatal("accepted an unbound recovery signer")
			}
		})
	}
	if keys != 0 || len(broadcasts) != 0 || len(rows.created) != 0 {
		t.Fatalf("keys=%d broadcasts=%d rows=%d", keys, len(broadcasts), len(rows.created))
	}
}

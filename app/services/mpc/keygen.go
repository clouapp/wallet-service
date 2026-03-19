package mpc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/tss"
)

// TSSService implements the Service interface using tss-lib v2.
type TSSService struct{}

// NewTSSService creates a new TSSService.
func NewTSSService() *TSSService {
	return &TSSService{}
}

// Keygen runs a 2-party MPC key generation ceremony for the given curve.
func (s *TSSService) Keygen(ctx context.Context, curve Curve) (*KeygenResult, error) {
	switch curve {
	case CurveSecp256k1:
		return keygenSecp256k1(ctx)
	case CurveEd25519:
		return keygenEd25519(ctx)
	default:
		return nil, fmt.Errorf("unsupported curve: %s", curve)
	}
}

// keygenSecp256k1 performs a 2-of-2 MPC keygen on secp256k1.
func keygenSecp256k1(ctx context.Context) (*KeygenResult, error) {
	// Create two party IDs with distinct random keys.
	keyA, err := randomBigInt256()
	if err != nil {
		return nil, fmt.Errorf("generate party key A: %w", err)
	}
	keyB, err := randomBigInt256()
	if err != nil {
		return nil, fmt.Errorf("generate party key B: %w", err)
	}

	partyIDA := tss.NewPartyID("A", "customer", keyA)
	partyIDB := tss.NewPartyID("B", "service", keyB)

	// SortPartyIDs assigns contiguous 0-based indices to parties.
	sortedIDs := tss.SortPartyIDs(tss.UnSortedPartyIDs{partyIDA, partyIDB})
	peerCtx := tss.NewPeerContext(sortedIDs)

	// threshold=1 means 2-of-2: both parties must be present to sign.
	partyCount := 2
	threshold := 1

	// Buffered channels to avoid blocking during message routing.
	outCh := make(chan tss.Message, partyCount*partyCount*10)
	endCh := make(chan keygen.LocalPartySaveData, partyCount)
	errCh := make(chan error, partyCount*4)

	// Create per-party parameters.
	paramsA := tss.NewParameters(tss.S256(), peerCtx, sortedIDs[0], partyCount, threshold)
	paramsB := tss.NewParameters(tss.S256(), peerCtx, sortedIDs[1], partyCount, threshold)

	// SetNoProofMod / SetNoProofFac skip expensive ZK proof generation.
	// These are safe for internal/trusted use; remove for adversarial settings.
	paramsA.SetNoProofMod()
	paramsA.SetNoProofFac()
	paramsB.SetNoProofMod()
	paramsB.SetNoProofFac()

	partyA := keygen.NewLocalParty(paramsA, outCh, endCh)
	partyB := keygen.NewLocalParty(paramsB, outCh, endCh)
	parties := []tss.Party{partyA, partyB}

	// Start both parties concurrently.
	for _, p := range parties {
		p := p
		go func() {
			if tssErr := p.Start(); tssErr != nil {
				errCh <- tssErr.Cause()
			}
		}()
	}

	// Collect save data from both parties.
	saves := make([]keygen.LocalPartySaveData, 0, partyCount)

loop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case routeErr := <-errCh:
			return nil, fmt.Errorf("keygen ceremony: %w", routeErr)

		case msg := <-outCh:
			dest := msg.GetTo()
			if dest == nil {
				// broadcast to all other parties
				for _, p := range parties {
					if p.PartyID().Index == msg.GetFrom().Index {
						continue
					}
					go routeMessage(p, msg, errCh)
				}
			} else {
				// point-to-point
				go routeMessage(parties[dest[0].Index], msg, errCh)
			}

		case save := <-endCh:
			saves = append(saves, save)
			if len(saves) == partyCount {
				break loop
			}
		}
	}

	saveA, saveB, err := matchSavesByIndex(saves)
	if err != nil {
		return nil, err
	}

	shareABytes, err := json.Marshal(saveA)
	if err != nil {
		return nil, fmt.Errorf("marshal shareA: %w", err)
	}
	shareBBytes, err := json.Marshal(saveB)
	if err != nil {
		return nil, fmt.Errorf("marshal shareB: %w", err)
	}

	pubKey := saveA.ECDSAPub
	if pubKey == nil {
		return nil, fmt.Errorf("keygen produced nil ECDSAPub")
	}

	return &KeygenResult{
		ShareA:         shareABytes,
		ShareB:         shareBBytes,
		CombinedPubKey: compressSecp256k1(pubKey.X(), pubKey.Y()),
	}, nil
}

// routeMessage converts msg to wire format and delivers it to party.
// This matches the pattern used in tss-lib's test utilities.
func routeMessage(party tss.Party, msg tss.Message, errCh chan<- error) {
	if party.PartyID().Index == msg.GetFrom().Index {
		return
	}
	bz, _, err := msg.WireBytes()
	if err != nil {
		errCh <- err
		return
	}
	pMsg, parseErr := tss.ParseWireMessage(bz, msg.GetFrom(), msg.IsBroadcast())
	if parseErr != nil {
		errCh <- parseErr
		return
	}
	if _, tssErr := party.Update(pMsg); tssErr != nil {
		errCh <- tssErr.Cause()
	}
}

// matchSavesByIndex returns (saveA, saveB) ordered by original party index (0, 1).
func matchSavesByIndex(saves []keygen.LocalPartySaveData) (keygen.LocalPartySaveData, keygen.LocalPartySaveData, error) {
	var zero keygen.LocalPartySaveData
	if len(saves) != 2 {
		return zero, zero, fmt.Errorf("expected 2 save data items, got %d", len(saves))
	}
	idx0, err := saves[0].OriginalIndex()
	if err != nil {
		return zero, zero, fmt.Errorf("OriginalIndex saves[0]: %w", err)
	}
	if idx0 == 0 {
		return saves[0], saves[1], nil
	}
	return saves[1], saves[0], nil
}

// compressSecp256k1 encodes (x, y) as a 33-byte SEC compressed public key.
func compressSecp256k1(x, y *big.Int) []byte {
	b := make([]byte, 33)
	if y.Bit(0) == 0 {
		b[0] = 0x02
	} else {
		b[0] = 0x03
	}
	xBytes := x.Bytes()
	copy(b[33-len(xBytes):], xBytes)
	return b
}

// randomBigInt256 returns a cryptographically random 256-bit positive integer.
func randomBigInt256() (*big.Int, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(buf), nil
}

// keygenEd25519 is a stub; ed25519 support requires additional Solana compatibility work.
func keygenEd25519(_ context.Context) (*KeygenResult, error) {
	return nil, fmt.Errorf("ed25519 keygen: pending Solana compatibility gate verification")
}

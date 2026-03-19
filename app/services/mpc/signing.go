package mpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/common"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	"github.com/bnb-chain/tss-lib/v2/tss"
)

// Sign performs a 2-party MPC signing ceremony on secp256k1.
// shareA and shareB are JSON-marshaled keygen.LocalPartySaveData blobs.
// inputs.TxHashes[0] is the 32-byte message digest to sign.
func (s *TSSService) Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error) {
	if curve != CurveSecp256k1 {
		return nil, fmt.Errorf("Sign: unsupported curve %s", curve)
	}
	if len(inputs.TxHashes) == 0 {
		return nil, fmt.Errorf("Sign: no transaction hashes provided")
	}

	// Unmarshal save data for both parties.
	var saveA, saveB keygen.LocalPartySaveData
	if err := json.Unmarshal(shareA, &saveA); err != nil {
		return nil, fmt.Errorf("Sign: unmarshal shareA: %w", err)
	}
	if err := json.Unmarshal(shareB, &saveB); err != nil {
		return nil, fmt.Errorf("Sign: unmarshal shareB: %w", err)
	}

	// Reconstruct the sorted party IDs from the Ks embedded in the save data.
	// Ks[i] is the key used as party ID during keygen, in sorted order.
	if len(saveA.Ks) != 2 {
		return nil, fmt.Errorf("Sign: expected 2 Ks entries, got %d", len(saveA.Ks))
	}
	ids := make(tss.UnSortedPartyIDs, 2)
	for i, k := range saveA.Ks {
		ids[i] = tss.NewPartyID(fmt.Sprintf("%d", i), fmt.Sprintf("party-%d", i), new(big.Int).Set(k))
	}
	sortedIDs := tss.SortPartyIDs(ids)
	peerCtx := tss.NewPeerContext(sortedIDs)

	partyCount := 2
	threshold := 1

	// The message to sign: interpret the 32-byte hash as a big.Int.
	msgBigInt := new(big.Int).SetBytes(inputs.TxHashes[0])

	// Buffered channels.
	outCh := make(chan tss.Message, partyCount*partyCount*10)
	endCh := make(chan common.SignatureData, partyCount)
	errCh := make(chan error, partyCount*4)

	// Determine which sorted index corresponds to each save data using OriginalIndex.
	idxA, err := saveA.OriginalIndex()
	if err != nil {
		return nil, fmt.Errorf("Sign: OriginalIndex for saveA: %w", err)
	}
	idxB, err := saveB.OriginalIndex()
	if err != nil {
		return nil, fmt.Errorf("Sign: OriginalIndex for saveB: %w", err)
	}

	paramsA := tss.NewParameters(tss.S256(), peerCtx, sortedIDs[idxA], partyCount, threshold)
	paramsB := tss.NewParameters(tss.S256(), peerCtx, sortedIDs[idxB], partyCount, threshold)

	partyA := signing.NewLocalParty(msgBigInt, paramsA, saveA, outCh, endCh)
	partyB := signing.NewLocalParty(msgBigInt, paramsB, saveB, outCh, endCh)
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

	// Collect signatures from both parties; they should agree.
	sigs := make([]common.SignatureData, 0, partyCount)

loop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case routeErr := <-errCh:
			return nil, fmt.Errorf("signing ceremony: %w", routeErr)

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

		case sigData := <-endCh:
			sigs = append(sigs, sigData)
			if len(sigs) == partyCount {
				break loop
			}
		}
	}

	// Use the first completed signature to produce the DER encoding.
	sigData := sigs[0]

	// If the library produced a pre-encoded DER signature, return it.
	if len(sigData.Signature) > 0 {
		return sigData.Signature, nil
	}

	// Otherwise DER-encode from R and S components.
	if len(sigData.R) == 0 || len(sigData.S) == 0 {
		return nil, fmt.Errorf("Sign: signature data missing R or S")
	}
	return derEncode(sigData.R, sigData.S), nil
}

// derEncode produces a DER-encoded ECDSA signature from raw R and S byte slices.
func derEncode(r, s []byte) []byte {
	rb := padTo32(r)
	sb := padTo32(s)

	// Ensure positive: prepend 0x00 if high bit is set (two's complement).
	if rb[0]&0x80 != 0 {
		rb = append([]byte{0x00}, rb...)
	}
	if sb[0]&0x80 != 0 {
		sb = append([]byte{0x00}, sb...)
	}

	seq := []byte{0x02, byte(len(rb))}
	seq = append(seq, rb...)
	seq = append(seq, 0x02, byte(len(sb)))
	seq = append(seq, sb...)

	return append([]byte{0x30, byte(len(seq))}, seq...)
}

// padTo32 zero-pads b to exactly 32 bytes on the left (big-endian).
func padTo32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

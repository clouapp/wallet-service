package mpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"filippo.io/edwards25519"

	"github.com/bnb-chain/tss-lib/v2/common"
	tsscrypto "github.com/bnb-chain/tss-lib/v2/crypto"
	"github.com/bnb-chain/tss-lib/v2/crypto/vss"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	eddsaKeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/tss"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/edwards/v2"
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
	sigCh := make(chan partySignature, partyCount)
	forwardCtx, stopForwarding := context.WithCancel(ctx)
	defer stopForwarding()
	go forward(forwardCtx, endCh, sigCh, newPartySignature)
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

	delta, err := applyKeyDerivationDelta(&saveA, &saveB, inputs.KeyDerivationDelta, inputs.ExpectedPublicKey)
	if err != nil {
		return nil, fmt.Errorf("Sign: %w", err)
	}

	paramsA := tss.NewParameters(tss.S256(), peerCtx, sortedIDs[idxA], partyCount, threshold)
	paramsB := tss.NewParameters(tss.S256(), peerCtx, sortedIDs[idxB], partyCount, threshold)

	newParty := func(params *tss.Parameters, save keygen.LocalPartySaveData) tss.Party {
		if delta == nil {
			return signing.NewLocalParty(msgBigInt, params, save, outCh, endCh)
		}
		return signing.NewLocalPartyWithKDD(msgBigInt, params, save, delta, outCh, endCh)
	}
	partyA := newParty(paramsA, saveA)
	partyB := newParty(paramsB, saveB)
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
	sigs := make([]partySignature, 0, partyCount)

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

		case sig := <-sigCh:
			sigs = append(sigs, sig)
			if len(sigs) == partyCount {
				break loop
			}
		}
	}

	// Use the first completed signature to produce the DER encoding.
	sigData := sigs[0]
	if !signatureMatchesKey(saveA, inputs.TxHashes[0], sigData.r, sigData.s) {
		return nil, fmt.Errorf("Sign: signature does not verify against the signing public key")
	}

	// If the library produced a pre-encoded DER signature, return it.
	if len(sigData.signature) > 0 {
		return sigData.signature, nil
	}

	// Otherwise DER-encode from R and S components.
	if len(sigData.r) == 0 || len(sigData.s) == 0 {
		return nil, fmt.Errorf("Sign: signature data missing R or S")
	}
	return derEncode(sigData.r, sigData.s), nil
}

// partySignature is what Sign reads from tss-lib's SignatureData. That message
// embeds a mutex, so it must not be copied around; the library's end channel
// carries it by value, which is why it is converted once, at the channel.
type partySignature struct {
	r, s, signature []byte
}

func newPartySignature(data *common.SignatureData) partySignature {
	return partySignature{r: data.R, s: data.S, signature: data.Signature}
}

// forward moves each value of in to out as convert(&value) until ctx ends. The
// value never leaves this function, so a caller sees pointers only.
func forward[T, R any](ctx context.Context, in <-chan T, out chan<- R, convert func(*T) R) {
	for {
		select {
		case <-ctx.Done():
			return
		case value := <-in:
			out <- convert(&value)
		}
	}
}

// applyKeyDerivationDelta checks that both shares hold the same wallet key and, for a
// BIP-32 child, moves the shares' public data to the child key: ECDSAPub becomes
// wallet key + delta·G and every BigXj gains delta·G, matching the delta tss-lib adds
// to each Xi in round 1. The private shares are never combined. It returns the delta
// to hand to NewLocalPartyWithKDD, or nil to sign with the wallet key.
func applyKeyDerivationDelta(saveA, saveB *keygen.LocalPartySaveData, delta *big.Int, expectedPublicKey []byte) (*big.Int, error) {
	if saveA.ECDSAPub == nil || saveB.ECDSAPub == nil || !saveA.ECDSAPub.Equals(saveB.ECDSAPub) {
		return nil, errors.New("secp256k1 shares do not agree on the public key")
	}
	walletPublicKey := compressSecp256k1(saveA.ECDSAPub.X(), saveA.ECDSAPub.Y())
	if delta == nil {
		if len(expectedPublicKey) > 0 && subtle.ConstantTimeCompare(walletPublicKey, expectedPublicKey) != 1 {
			return nil, errors.New("secp256k1 shares do not belong to the expected public key")
		}
		return nil, nil
	}
	if len(expectedPublicKey) != secp256k1CompressedKeySize {
		return nil, errors.New("signing for a derived key needs the expected child public key")
	}
	curve := tss.S256()
	if delta.Sign() <= 0 || delta.Cmp(curve.Params().N) >= 0 {
		return nil, errors.New("key derivation delta is out of range")
	}
	tweak := new(big.Int).Set(delta)
	childPoint, err := saveA.ECDSAPub.Add(tsscrypto.ScalarBaseMult(curve, tweak))
	if err != nil {
		return nil, fmt.Errorf("derive child public key: %w", err)
	}
	if subtle.ConstantTimeCompare(compressSecp256k1(childPoint.X(), childPoint.Y()), expectedPublicKey) != 1 {
		return nil, errors.New("key derivation delta does not produce the expected child public key")
	}
	keys := []keygen.LocalPartySaveData{*saveA, *saveB}
	if err := signing.UpdatePublicKeyAndAdjustBigXj(tweak, keys, childPoint.ToECDSAPubKey(), curve); err != nil {
		return nil, fmt.Errorf("move shares to the child key: %w", err)
	}
	*saveA, *saveB = keys[0], keys[1]
	return tweak, nil
}

const secp256k1CompressedKeySize = 33

// signatureMatchesKey verifies (R, S) over digest with the key the ceremony signed
// for (the child key after applyKeyDerivationDelta).
func signatureMatchesKey(save keygen.LocalPartySaveData, digest, rBytes, sBytes []byte) bool {
	if save.ECDSAPub == nil || len(rBytes) == 0 || len(sBytes) == 0 {
		return false
	}
	r := new(big.Int).SetBytes(rBytes)
	s := new(big.Int).SetBytes(sBytes)
	return ecdsa.Verify(save.ECDSAPub.ToECDSAPubKey(), digest, r, s)
}

// ReconstructEd25519PrivateKey returns (XiA + XiB) mod N, the SLIP-0010 master key
// every ed25519 child address was derived from. It is NOT the private key of the
// wallet public key (the shares are Shamir points, see ReconstructEd25519Scalar), so
// it must never sign for the genesis address; it only re-derives child seeds.
// The caller MUST zero the returned bytes after use.
func (s *TSSService) ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error) {
	var saveA, saveB eddsaKeygen.LocalPartySaveData
	defer func() {
		wipeBigInt(saveA.Xi)
		wipeBigInt(saveB.Xi)
	}()
	if err := json.Unmarshal(shareA, &saveA); err != nil {
		return nil, errMalformedEd25519Share
	}
	if err := json.Unmarshal(shareB, &saveB); err != nil {
		return nil, errMalformedEd25519Share
	}

	if saveA.Xi == nil || saveB.Xi == nil {
		return nil, fmt.Errorf("shares missing private key components")
	}

	curveOrder := tss.Edwards().Params().N
	sum := new(big.Int).Add(saveA.Xi, saveB.Xi)
	defer wipeBigInt(sum)
	privateScalar := new(big.Int).Mod(sum, curveOrder)
	defer wipeBigInt(privateScalar)

	privBytes := make([]byte, ed25519ScalarSize)
	privateScalar.FillBytes(privBytes)
	return privBytes, nil
}

// ReconstructEd25519Scalar Lagrange-interpolates the two Shamir shares into the
// ed25519 scalar a with a·B equal to the wallet public key, and refuses to return
// it when the shares disagree on that key. The result is 32 bytes big-endian; it is
// a scalar, not an RFC 8032 seed. The caller MUST zero the returned bytes after use.
func (s *TSSService) ReconstructEd25519Scalar(shareA, shareB []byte) ([]byte, error) {
	var saveA, saveB eddsaKeygen.LocalPartySaveData
	defer func() {
		wipeBigInt(saveA.Xi)
		wipeBigInt(saveB.Xi)
	}()
	// json errors can quote the offending value, which may be share material.
	if err := json.Unmarshal(shareA, &saveA); err != nil {
		return nil, errMalformedEd25519Share
	}
	if err := json.Unmarshal(shareB, &saveB); err != nil {
		return nil, errMalformedEd25519Share
	}
	if saveA.Xi == nil || saveB.Xi == nil || saveA.ShareID == nil || saveB.ShareID == nil {
		return nil, fmt.Errorf("shares missing private key components")
	}
	if saveA.EDDSAPub == nil || saveB.EDDSAPub == nil || !saveA.EDDSAPub.Equals(saveB.EDDSAPub) {
		return nil, fmt.Errorf("ed25519 shares do not agree on the public key")
	}
	curve := tss.Edwards()
	publicKey := (&edwards.PublicKey{Curve: curve, X: saveA.EDDSAPub.X(), Y: saveA.EDDSAPub.Y()}).Serialize()

	secret, err := interpolateTwoSharesAtZero(saveA.ShareID, saveA.Xi, saveB.ShareID, saveB.Xi, curve.Params().N)
	if err != nil {
		return nil, err
	}
	defer wipeBigInt(secret)

	out := make([]byte, ed25519ScalarSize)
	secret.FillBytes(out)
	if !ed25519ScalarMatchesPublicKey(out, publicKey) {
		zeroBytes(out)
		return nil, fmt.Errorf("reconstructed ed25519 scalar does not match the wallet public key")
	}
	return out, nil
}

const ed25519ScalarSize = 32

var errMalformedEd25519Share = errors.New("malformed ed25519 share")

// interpolateTwoSharesAtZero returns f(0) mod n for the degree-1 polynomial through
// (idA, shareA) and (idB, shareB): (shareA·idB − shareB·idA) / (idB − idA). Party ids
// are tss-lib keys and may exceed n; like tss-lib they are used modulo n.
func interpolateTwoSharesAtZero(idA, shareA, idB, shareB, n *big.Int) (*big.Int, error) {
	for _, id := range []*big.Int{idA, idB} {
		if id.Sign() <= 0 || new(big.Int).Mod(id, n).Sign() == 0 {
			return nil, fmt.Errorf("ed25519 share id is out of range")
		}
	}
	for _, share := range []*big.Int{shareA, shareB} {
		if share.Sign() < 0 || share.Cmp(n) >= 0 {
			return nil, fmt.Errorf("ed25519 share is out of range")
		}
	}
	idDelta := new(big.Int).Sub(idB, idA)
	idDelta.Mod(idDelta, n)
	if idDelta.Sign() == 0 {
		return nil, fmt.Errorf("ed25519 shares must come from two different parties")
	}
	inverse := new(big.Int).ModInverse(idDelta, n)
	if inverse == nil {
		return nil, fmt.Errorf("ed25519 share ids are not interpolable")
	}

	termA := new(big.Int).Mul(shareA, idB)
	defer wipeBigInt(termA)
	termB := new(big.Int).Mul(shareB, idA)
	defer wipeBigInt(termB)
	scaledSecret := new(big.Int).Sub(termA, termB)
	defer wipeBigInt(scaledSecret)
	unreducedSecret := new(big.Int).Mul(scaledSecret, inverse)
	defer wipeBigInt(unreducedSecret)
	secret := new(big.Int).Mod(unreducedSecret, n)
	if secret.Sign() == 0 {
		wipeBigInt(secret)
		return nil, fmt.Errorf("reconstructed ed25519 scalar is zero")
	}
	return secret, nil
}

// ed25519ScalarMatchesPublicKey reports, in constant time, whether the big-endian
// scalar times the base point encodes to publicKey.
func ed25519ScalarMatchesPublicKey(scalarBigEndian, publicKey []byte) bool {
	littleEndian := make([]byte, ed25519ScalarSize)
	defer zeroBytes(littleEndian)
	for i := range scalarBigEndian {
		littleEndian[i] = scalarBigEndian[ed25519ScalarSize-1-i]
	}
	scalar, err := edwards25519.NewScalar().SetCanonicalBytes(littleEndian)
	if err != nil {
		return false
	}
	defer scalar.Set(edwards25519.NewScalar())
	derived := new(edwards25519.Point).ScalarBaseMult(scalar).Bytes()
	return subtle.ConstantTimeCompare(derived, publicKey) == 1
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ReconstructSecp256k1PrivateKey temporarily reconstructs the secp256k1 scalar
// from both MPC shares. The caller MUST zero the returned bytes after use.
func (s *TSSService) ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error) {
	var saveA, saveB keygen.LocalPartySaveData
	defer func() {
		wipeBigInt(saveA.Xi)
		wipeBigInt(saveB.Xi)
	}()
	if err := json.Unmarshal(shareA, &saveA); err != nil {
		return nil, fmt.Errorf("unmarshal secp256k1 shareA: %w", err)
	}
	if err := json.Unmarshal(shareB, &saveB); err != nil {
		return nil, fmt.Errorf("unmarshal secp256k1 shareB: %w", err)
	}
	if saveA.Xi == nil || saveB.Xi == nil || saveA.ShareID == nil || saveB.ShareID == nil {
		return nil, fmt.Errorf("shares missing private key components")
	}
	secret, err := (vss.Shares{
		{Threshold: 1, ID: saveA.ShareID, Share: saveA.Xi},
		{Threshold: 1, ID: saveB.ShareID, Share: saveB.Xi},
	}).ReConstruct(btcec.S256())
	if err != nil {
		return nil, fmt.Errorf("reconstruct secp256k1 scalar: %w", err)
	}
	defer wipeBigInt(secret)
	out := make([]byte, 32)
	b := secret.Bytes()
	copy(out[32-len(b):], b)
	zeroBytes(b)
	return out, nil
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

// wipeBigInt overwrites the words backing n before resetting it; SetInt64(0)
// alone leaves the old limbs in the underlying array.
func wipeBigInt(n *big.Int) {
	if n == nil {
		return
	}
	words := n.Bits()
	words = words[:cap(words)]
	for i := range words {
		words[i] = 0
	}
	n.SetInt64(0)
}

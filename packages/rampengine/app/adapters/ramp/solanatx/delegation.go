package solanatx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/gagliardetto/solana-go"
)

// ErrDelegationRefused is what CheckDelegation wraps every refusal in: the
// transaction is not exactly the delegation the quote needs. The wrapped
// reason names the rule that failed and never an address or an amount, so a
// caller may log it.
var ErrDelegationRefused = errors.New("solanatx: transaction is not the expected delegation")

// approveCheckedTag is the SPL Token instruction tag of ApproveChecked, the
// same in the classic program and in Token-2022.
const approveCheckedTag = 13

// approveCheckedDataLen is the tag, the u64 little-endian amount and the u8
// decimals.
const approveCheckedDataLen = 1 + 8 + 1

// DelegationTerms is the one delegation a bank payout quote's funding may
// carry: Owner approves Delegate to move Amount base units of Mint, at
// Decimals, from Owner's associated token account, through TokenProgram.
type DelegationTerms struct {
	Owner        solana.PublicKey
	Mint         solana.PublicKey
	Delegate     solana.PublicKey
	TokenProgram solana.PublicKey // solana.TokenProgramID: the provider prepares classic SPL Token
	Amount       uint64           // base units
	Decimals     uint8
}

// CheckDelegation enforces spec §5.7 item 5 on an unsigned legacy or v0
// transaction: the owner is the fee payer and the only signer, the message
// uses no address table lookup, and its one instruction is
// TokenProgram.ApproveChecked(Amount, Decimals) with the accounts
// [ATA(Owner, Mint), Mint, Delegate, Owner]. Anything else is refused,
// wrapping ErrDelegationRefused.
//
// The instruction is parsed by hand: solana-go's instruction decoders can
// panic on malformed data, and every account index is bounds-checked here.
func CheckDelegation(tx *solana.Transaction, t DelegationTerms) error {
	if err := checkDelegation(tx, t); err != nil {
		return fmt.Errorf("%w: %w", ErrDelegationRefused, err)
	}
	return nil
}

func checkDelegation(tx *solana.Transaction, t DelegationTerms) error {
	if err := checkTerms(t); err != nil {
		return err
	}
	if tx == nil {
		return errors.New("no transaction")
	}
	msg := &tx.Message
	switch msg.GetVersion() {
	case solana.MessageVersionLegacy, solana.MessageVersionV0:
	default:
		return errors.New("message is neither legacy nor v0")
	}
	if len(msg.AddressTableLookups) != 0 {
		return errors.New("message uses address table lookups")
	}
	if msg.Header.NumRequiredSignatures != 1 {
		return fmt.Errorf("message requires %d signatures, not 1", msg.Header.NumRequiredSignatures)
	}
	// The owner pays the fee, so its account is debited: it must be writable.
	if msg.Header.NumReadonlySignedAccounts != 0 {
		return errors.New("message marks the signing owner read-only")
	}
	if len(msg.AccountKeys) == 0 || !msg.AccountKeys[0].Equals(t.Owner) {
		return errors.New("fee payer is not the owner")
	}
	if len(msg.Instructions) != 1 {
		return fmt.Errorf("message carries %d instructions, not 1", len(msg.Instructions))
	}
	return checkApproveChecked(msg, msg.Instructions[0], t)
}

// checkTerms refuses terms that could not describe a real delegation, so a
// blank setting never matches a zero key in a transaction.
func checkTerms(t DelegationTerms) error {
	switch {
	case t.Owner.IsZero():
		return errors.New("terms carry no owner")
	case t.Mint.IsZero():
		return errors.New("terms carry no mint")
	case t.Delegate.IsZero():
		return errors.New("terms carry no delegate")
	case t.TokenProgram.IsZero():
		return errors.New("terms carry no token program")
	case t.Amount == 0:
		return errors.New("terms carry no amount")
	}
	return nil
}

// checkApproveChecked checks the one compiled instruction against the terms.
func checkApproveChecked(msg *solana.Message, ix solana.CompiledInstruction, t DelegationTerms) error {
	program, ok := accountKey(msg, ix.ProgramIDIndex)
	if !ok {
		return errors.New("instruction program index is out of range")
	}
	if !program.Equals(t.TokenProgram) {
		return errors.New("instruction program is not the token program")
	}
	data := ix.Data
	if len(data) != approveCheckedDataLen {
		return fmt.Errorf("instruction data is %d bytes, not %d", len(data), approveCheckedDataLen)
	}
	if data[0] != approveCheckedTag {
		return errors.New("instruction is not ApproveChecked")
	}
	if binary.LittleEndian.Uint64(data[1:9]) != t.Amount {
		return errors.New("approved amount differs from the quote")
	}
	if data[9] != t.Decimals {
		return errors.New("approved decimals differ from the mint's")
	}
	if len(ix.Accounts) != 4 {
		return fmt.Errorf("instruction carries %d accounts, not 4", len(ix.Accounts))
	}
	source, err := ownerTokenAccount(t)
	if err != nil {
		return err
	}
	want := []struct {
		key  solana.PublicKey
		name string
	}{{source, "source is not the owner's associated token account"}, {t.Mint, "mint differs"}, {t.Delegate, "delegate differs"}, {t.Owner, "authority is not the owner"}}
	for i, w := range want {
		key, ok := accountKey(msg, ix.Accounts[i])
		if !ok {
			return fmt.Errorf("instruction account %d index is out of range", i)
		}
		if !key.Equals(w.key) {
			return errors.New(w.name)
		}
	}
	// The owner signs as the fee payer, the only signer: its index must be
	// that signer's, not a second, unsigned copy of the same key.
	if ix.Accounts[3] != 0 {
		return errors.New("authority is not the signing owner")
	}
	// ApproveChecked writes the delegate onto the source token account: the
	// header must leave it writable. Accounts past the signers are writable
	// up to the last NumReadonlyUnsignedAccounts keys.
	readonly := int(msg.Header.NumReadonlyUnsignedAccounts)
	if readonly > len(msg.AccountKeys)-1 || int(ix.Accounts[0]) >= len(msg.AccountKeys)-readonly {
		return errors.New("message marks the source token account read-only")
	}
	return nil
}

// accountKey answers the static account key at index, if there is one.
func accountKey(msg *solana.Message, index uint16) (solana.PublicKey, bool) {
	if int(index) >= len(msg.AccountKeys) {
		return solana.PublicKey{}, false
	}
	return msg.AccountKeys[index], true
}

// ownerTokenAccount derives the owner's associated token account for the
// mint under the terms' token program. solana-go's FindAssociatedTokenAddress
// hard-codes the classic program, so the seeds are spelled out.
func ownerTokenAccount(t DelegationTerms) (solana.PublicKey, error) {
	ata, _, err := solana.FindProgramAddress(
		[][]byte{t.Owner[:], t.TokenProgram[:], t.Mint[:]},
		solana.SPLAssociatedTokenAccountProgramID,
	)
	if err != nil {
		return solana.PublicKey{}, errors.New("owner's associated token account does not derive")
	}
	return ata, nil
}

// BaseUnits converts the quote's decimal amount ("10.12") to base units at
// decimals, refusing more places than decimals and overflow. The amount is
// digits with an optional fraction ("10", "10.12"); a sign, an exponent, a
// blank side of the point or anything else is refused, so the conversion is
// exact and never rounds.
func BaseUnits(amount string, decimals uint8) (uint64, error) {
	if amount == "" {
		return 0, errors.New("solanatx: amount is empty")
	}
	whole, fraction, hasPoint := amount, "", false
	for i := 0; i < len(amount); i++ {
		if amount[i] == '.' {
			whole, fraction, hasPoint = amount[:i], amount[i+1:], true
			break
		}
	}
	if whole == "" || (hasPoint && fraction == "") {
		return 0, errors.New("solanatx: amount is not a decimal number")
	}
	if len(fraction) > int(decimals) {
		return 0, fmt.Errorf("solanatx: amount has more than %d decimal places", decimals)
	}
	var units uint64
	for _, digits := range []string{whole, fraction} {
		for i := 0; i < len(digits); i++ {
			c := digits[i]
			if c < '0' || c > '9' {
				return 0, errors.New("solanatx: amount is not a decimal number")
			}
			var err error
			if units, err = timesTenPlus(units, uint64(c-'0')); err != nil {
				return 0, err
			}
		}
	}
	for range int(decimals) - len(fraction) {
		var err error
		if units, err = timesTenPlus(units, 0); err != nil {
			return 0, err
		}
	}
	return units, nil
}

// timesTenPlus answers units*10 + digit, refusing overflow.
func timesTenPlus(units, digit uint64) (uint64, error) {
	hi, lo := bits.Mul64(units, 10)
	if hi != 0 || lo > math.MaxUint64-digit {
		return 0, errOverflow
	}
	return lo + digit, nil
}

var errOverflow = errors.New("solanatx: amount overflows base units")

package bitcoin

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/wire"
	"github.com/stretchr/testify/require"
)

func TestMemo_IsPartOfTheSignedTransactionAndItsFee(t *testing.T) {
	f := newP2WPKHFixture(t)
	outputs, err := outputsFrom(f.unsigned)
	require.NoError(t, err)
	const memo = "=:ETH.ETH:0x1111111111111111111111111111111111111111"
	f.unsigned.Metadata["outputs"] = append(outputs, btcOutput{Memo: []byte(memo)})
	signed := f.sign(t, f.key)
	var decoded wire.MsgTx
	require.NoError(t, decoded.Deserialize(bytes.NewReader(signed.RawBytes)))
	require.Equal(t, byte(0x6a), decoded.TxOut[2].PkScript[0])
	require.Equal(t, int64(0), decoded.TxOut[2].Value)
	require.Equal(t, []byte(memo), decoded.TxOut[2].PkScript[2:])
	base := btcFeePolicy{milliSatPerVByte: 10000}
	withMemo := base
	withMemo.extraVBytes = int64(11 + len(memo))
	require.Equal(t, int64(10*(11+len(memo))), withMemo.fee(1, 2)-base.fee(1, 2))
	changed, _ := outputsFrom(f.unsigned)
	changed[2].Memo = []byte("different")
	f.unsigned.Metadata["outputs"] = changed
	require.Error(t, verifySignedP2WPKH(f.unsigned, signed, f.address, netParams(f.unsigned)))
}

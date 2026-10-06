package tron

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

// tronTransferEventTopic is keccak256("Transfer(address,address,uint256)").
const tronTransferEventTopic = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

const tronTransferEventTopics = 3

type tronBlockJSON struct {
	tronBlockHeaderJSON
	Transactions []tronBlockTransaction `json:"transactions"`
}

type tronBlockTransaction struct {
	TxID string `json:"txID"`
	Ret  []struct {
		ContractRet string `json:"contractRet"`
	} `json:"ret"`
	RawData struct {
		Contract []struct {
			Type      string `json:"type"`
			Parameter struct {
				Value struct {
					OwnerAddress string `json:"owner_address"`
					ToAddress    string `json:"to_address"`
					Amount       int64  `json:"amount"`
				} `json:"value"`
			} `json:"parameter"`
		} `json:"contract"`
	} `json:"raw_data"`
}

type tronEventLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// ScanBlock returns the deposits of block blockNum: successful TRX TransferContracts,
// and Transfer events of the registered TRC-20 contracts from successful calls
// (read from /wallet/gettransactioninfobyblocknum whenever the block holds a
// contract call, so token transfers made by other contracts are found too). Any
// failed read fails the scan, so the block is retried rather than marked scanned
// without its token deposits. Addresses are Base58Check.
func (a *TronLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	if a.tokensErr != nil {
		return nil, a.tokensErr
	}
	var block tronBlockJSON
	if err := a.post(ctx, "/wallet/getblockbynum", map[string]any{"num": blockNum}, &block); err != nil {
		return nil, fmt.Errorf("fetch tron block %d: %w", blockNum, err)
	}
	head, err := parseTronBlockHeader(block.tronBlockHeaderJSON, blockNum)
	if err != nil {
		return nil, err
	}
	blockHash := hex.EncodeToString(head.blockID)
	blockTime := time.UnixMilli(head.timestamp)

	transfers, hasContractCall, err := a.nativeTransfers(block.Transactions, blockNum, blockHash, blockTime)
	if err != nil {
		return nil, err
	}
	if !hasContractCall || len(a.tokens) == 0 {
		return transfers, nil
	}
	tokenTransfers, err := a.tokenTransfers(ctx, blockNum, blockHash, blockTime, len(block.Transactions))
	if err != nil {
		return nil, err
	}
	return append(transfers, tokenTransfers...), nil
}

func (a *TronLive) nativeTransfers(txs []tronBlockTransaction, blockNum uint64, blockHash string, blockTime time.Time) ([]types.DetectedTransfer, bool, error) {
	var transfers []types.DetectedTransfer
	hasContractCall := false
	for _, tx := range txs {
		if len(tx.RawData.Contract) != 1 {
			return nil, false, fmt.Errorf("tron block %d: tx %s has %d contracts, want 1", blockNum, tx.TxID, len(tx.RawData.Contract))
		}
		contract := tx.RawData.Contract[0]
		if contract.Type == tronTriggerSmartContractName {
			hasContractCall = true
			continue
		}
		if contract.Type != tronTransferContractName || !tronTxSucceeded(tx) {
			continue
		}
		value := contract.Parameter.Value
		if value.Amount <= 0 {
			continue
		}
		from, err := addressing.TronAddressFromHex(value.OwnerAddress)
		if err != nil {
			return nil, false, fmt.Errorf("tron block %d tx %s owner: %w", blockNum, tx.TxID, err)
		}
		to, err := addressing.TronAddressFromHex(value.ToAddress)
		if err != nil {
			return nil, false, fmt.Errorf("tron block %d tx %s recipient: %w", blockNum, tx.TxID, err)
		}
		txID, err := normalizeTronTxID(tx.TxID)
		if err != nil {
			return nil, false, fmt.Errorf("tron block %d: %w", blockNum, err)
		}
		transfers = append(transfers, types.DetectedTransfer{
			TxHash: txID, BlockNumber: blockNum, BlockHash: blockHash,
			From: from, To: to, Amount: big.NewInt(value.Amount),
			Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
		})
	}
	return transfers, hasContractCall, nil
}

func tronTxSucceeded(tx tronBlockTransaction) bool {
	return len(tx.Ret) > 0 && tx.Ret[0].ContractRet == tronReceiptSuccess
}

// tokenTransfers reads the block's transaction infos (one per transaction, in block
// order) and returns the registered tokens' Transfer events. LogIndex is the event's
// position among all the block's logs.
func (a *TronLive) tokenTransfers(ctx context.Context, blockNum uint64, blockHash string, blockTime time.Time, txCount int) ([]types.DetectedTransfer, error) {
	var raw json.RawMessage
	if err := a.post(ctx, "/wallet/gettransactioninfobyblocknum", map[string]any{"num": blockNum}, &raw); err != nil {
		return nil, fmt.Errorf("fetch tron transaction infos of block %d: %w", blockNum, err)
	}
	var infos []tronTransactionInfo
	if trimmed := bytes.TrimSpace(raw); !bytes.Equal(trimmed, []byte("{}")) {
		if err := json.Unmarshal(trimmed, &infos); err != nil {
			return nil, fmt.Errorf("parse tron transaction infos of block %d: %w", blockNum, err)
		}
	}
	if len(infos) != txCount {
		return nil, fmt.Errorf("tron block %d has %d transactions but %d transaction infos", blockNum, txCount, len(infos))
	}

	var transfers []types.DetectedTransfer
	logIndex := uint(0)
	for _, info := range infos {
		if info.BlockNumber != int64(blockNum) {
			return nil, fmt.Errorf("tron transaction info %s is in block %d, want %d", info.ID, info.BlockNumber, blockNum)
		}
		succeeded := info.failure() == ""
		for _, log := range info.Log {
			index := logIndex
			logIndex++
			if !succeeded {
				continue
			}
			transfer, ok, err := a.tokenTransfer(log, info.ID, index)
			if err != nil {
				return nil, fmt.Errorf("tron block %d tx %s: %w", blockNum, info.ID, err)
			}
			if !ok {
				continue
			}
			transfer.BlockNumber, transfer.BlockHash, transfer.Timestamp = blockNum, blockHash, blockTime
			transfers = append(transfers, transfer)
		}
	}
	return transfers, nil
}

// tokenTransfer decodes log as a registered token's Transfer event; ok is false
// for any other event or contract.
func (a *TronLive) tokenTransfer(log tronEventLog, txHash string, logIndex uint) (types.DetectedTransfer, bool, error) {
	contract, err := hex.DecodeString(strings.TrimPrefix(log.Address, "0x"))
	if err != nil || len(contract) != addressing.TronAddressBodySize {
		return types.DetectedTransfer{}, false, nil
	}
	var body [addressing.TronAddressBodySize]byte
	copy(body[:], contract)
	token, registered := a.tokens[body]
	if !registered || len(log.Topics) != tronTransferEventTopics || !strings.EqualFold(log.Topics[0], tronTransferEventTopic) {
		return types.DetectedTransfer{}, false, nil
	}
	from, err := tronAddressFromTopic(log.Topics[1])
	if err != nil {
		return types.DetectedTransfer{}, false, fmt.Errorf("transfer from: %w", err)
	}
	to, err := tronAddressFromTopic(log.Topics[2])
	if err != nil {
		return types.DetectedTransfer{}, false, fmt.Errorf("transfer to: %w", err)
	}
	data, err := hex.DecodeString(strings.TrimPrefix(log.Data, "0x"))
	if err != nil || len(data) != tronABIWordBytes {
		return types.DetectedTransfer{}, false, fmt.Errorf("transfer amount %q is not one ABI word", log.Data)
	}
	amount := new(big.Int).SetBytes(data)
	if amount.Sign() == 0 {
		return types.DetectedTransfer{}, false, nil
	}
	txID, err := normalizeTronTxID(txHash)
	if err != nil {
		return types.DetectedTransfer{}, false, err
	}
	tokenCopy := token
	return types.DetectedTransfer{
		TxHash: txID, From: from, To: to, Amount: amount,
		Asset: token.Symbol, Token: &tokenCopy, LogIndex: logIndex,
	}, true, nil
}

// tronAddressFromTopic reads an indexed address: a word whose last 20 bytes are
// the address body and whose first 12 are zero.
func tronAddressFromTopic(topic string) (string, error) {
	word, err := hex.DecodeString(strings.TrimPrefix(topic, "0x"))
	if err != nil || len(word) != tronABIWordBytes {
		return "", fmt.Errorf("topic %q is not one ABI word", topic)
	}
	padding := tronABIWordBytes - addressing.TronAddressBodySize
	if !bytes.Equal(word[:padding], make([]byte, padding)) {
		return "", fmt.Errorf("topic %q is not an address", topic)
	}
	return addressing.EncodeTronAddress(append([]byte{addressing.TronAddressPrefix}, word[padding:]...))
}

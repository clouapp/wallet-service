package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/macrowallets/waas/app/services/addressing"
)

// Field numbers and contract types of java-tron's protocol/core/Tron.proto and
// core/contract/*.proto. Only the fields this adapter writes are listed; decoding
// rejects any other field so a verified transaction is exactly what was built.
const (
	tronTransactionRawDataField   protowire.Number = 1
	tronTransactionSignatureField protowire.Number = 2

	tronRawRefBlockBytesField protowire.Number = 1
	tronRawRefBlockHashField  protowire.Number = 4
	tronRawExpirationField    protowire.Number = 8
	tronRawContractField      protowire.Number = 11
	tronRawTimestampField     protowire.Number = 14
	tronRawFeeLimitField      protowire.Number = 18

	tronContractTypeField      protowire.Number = 1
	tronContractParameterField protowire.Number = 2

	tronAnyTypeURLField protowire.Number = 1
	tronAnyValueField   protowire.Number = 2

	tronTransferOwnerField  protowire.Number = 1
	tronTransferToField     protowire.Number = 2
	tronTransferAmountField protowire.Number = 3

	tronTriggerOwnerField     protowire.Number = 1
	tronTriggerContractField  protowire.Number = 2
	tronTriggerCallValueField protowire.Number = 3
	tronTriggerDataField      protowire.Number = 4

	tronContractTypeTransfer        int32 = 1
	tronContractTypeTriggerSmart    int32 = 31
	tronTransferContractTypeURL           = "type.googleapis.com/protocol.TransferContract"
	tronTriggerSmartContractTypeURL       = "type.googleapis.com/protocol.TriggerSmartContract"
	tronTransferContractName              = "TransferContract"
	tronTriggerSmartContractName          = "TriggerSmartContract"

	tronRefBlockBytesSize = 2
	tronRefBlockHashSize  = 8
	tronBlockIDSize       = 32
	tronTxIDSize          = sha256.Size
	tronSignatureSize     = 65
)

// tronRefBlock is the TaPoS reference of a transaction: bytes 6..7 of the block
// number and bytes 8..15 of the block id.
type tronRefBlock struct {
	bytes []byte
	hash  []byte
}

func newTronRefBlock(number int64, blockID []byte) (tronRefBlock, error) {
	if number <= 0 {
		return tronRefBlock{}, fmt.Errorf("tron ref block: number %d is not positive", number)
	}
	if len(blockID) != tronBlockIDSize {
		return tronRefBlock{}, fmt.Errorf("tron ref block: block id has %d bytes, want %d", len(blockID), tronBlockIDSize)
	}
	var numberBytes [8]byte
	binary.BigEndian.PutUint64(numberBytes[:], uint64(number))
	return tronRefBlock{
		bytes: append([]byte(nil), numberBytes[6:8]...),
		hash:  append([]byte(nil), blockID[8:16]...),
	}, nil
}

// tronContract is the single contract of a transaction: a TransferContract (TRX)
// or a TriggerSmartContract (TRC-20 call).
type tronContract struct {
	contractType int32
	owner        []byte
	to           []byte // TransferContract recipient
	amount       int64  // TransferContract sun
	contract     []byte // TriggerSmartContract address
	callValue    int64  // TriggerSmartContract sun sent with the call
	data         []byte // TriggerSmartContract ABI call
}

// tronRawData is Transaction.raw with the fields this adapter writes.
type tronRawData struct {
	refBlockBytes []byte
	refBlockHash  []byte
	expiration    int64
	contract      tronContract
	timestamp     int64
	feeLimit      int64
}

// encode serializes raw in field order with proto3 defaults omitted, the canonical
// form java-tron produces; txID = sha256 of these bytes.
func (raw tronRawData) encode() ([]byte, error) {
	if len(raw.refBlockBytes) != tronRefBlockBytesSize || len(raw.refBlockHash) != tronRefBlockHashSize {
		return nil, fmt.Errorf("tron raw data: ref block must be %d + %d bytes", tronRefBlockBytesSize, tronRefBlockHashSize)
	}
	if raw.expiration <= 0 || raw.timestamp < 0 || raw.feeLimit < 0 {
		return nil, fmt.Errorf("tron raw data: expiration %d, timestamp %d or fee limit %d out of range", raw.expiration, raw.timestamp, raw.feeLimit)
	}
	contract, err := raw.contract.encode()
	if err != nil {
		return nil, err
	}
	var out []byte
	out = appendBytesField(out, tronRawRefBlockBytesField, raw.refBlockBytes)
	out = appendBytesField(out, tronRawRefBlockHashField, raw.refBlockHash)
	out = appendVarintField(out, tronRawExpirationField, uint64(raw.expiration))
	out = appendBytesField(out, tronRawContractField, contract)
	out = appendVarintField(out, tronRawTimestampField, uint64(raw.timestamp))
	out = appendVarintField(out, tronRawFeeLimitField, uint64(raw.feeLimit))
	return out, nil
}

func (c tronContract) encode() ([]byte, error) {
	if err := requireTronRaw("owner", c.owner); err != nil {
		return nil, err
	}
	var typeURL string
	var value []byte
	switch c.contractType {
	case tronContractTypeTransfer:
		if err := requireTronRaw("recipient", c.to); err != nil {
			return nil, err
		}
		if c.amount <= 0 {
			return nil, fmt.Errorf("tron transfer: amount %d must be positive", c.amount)
		}
		typeURL = tronTransferContractTypeURL
		value = appendBytesField(value, tronTransferOwnerField, c.owner)
		value = appendBytesField(value, tronTransferToField, c.to)
		value = appendVarintField(value, tronTransferAmountField, uint64(c.amount))
	case tronContractTypeTriggerSmart:
		if err := requireTronRaw("contract", c.contract); err != nil {
			return nil, err
		}
		if c.callValue < 0 || len(c.data) == 0 {
			return nil, fmt.Errorf("tron trigger: call value %d or empty call data", c.callValue)
		}
		typeURL = tronTriggerSmartContractTypeURL
		value = appendBytesField(value, tronTriggerOwnerField, c.owner)
		value = appendBytesField(value, tronTriggerContractField, c.contract)
		value = appendVarintField(value, tronTriggerCallValueField, uint64(c.callValue))
		value = appendBytesField(value, tronTriggerDataField, c.data)
	default:
		return nil, fmt.Errorf("tron contract type %d is not supported", c.contractType)
	}
	var parameter []byte
	parameter = appendStringField(parameter, tronAnyTypeURLField, typeURL)
	parameter = appendBytesField(parameter, tronAnyValueField, value)

	var out []byte
	out = appendVarintField(out, tronContractTypeField, uint64(c.contractType))
	out = appendBytesField(out, tronContractParameterField, parameter)
	return out, nil
}

func (c tronContract) name() string {
	if c.contractType == tronContractTypeTriggerSmart {
		return tronTriggerSmartContractName
	}
	return tronTransferContractName
}

func requireTronRaw(field string, raw []byte) error {
	if len(raw) != addressing.TronAddressSize || raw[0] != addressing.TronAddressPrefix {
		return fmt.Errorf("tron %s address must be %d bytes starting with 0x%x", field, addressing.TronAddressSize, addressing.TronAddressPrefix)
	}
	return nil
}

func appendVarintField(out []byte, field protowire.Number, value uint64) []byte {
	if value == 0 {
		return out
	}
	out = protowire.AppendTag(out, field, protowire.VarintType)
	return protowire.AppendVarint(out, value)
}

func appendBytesField(out []byte, field protowire.Number, value []byte) []byte {
	if len(value) == 0 {
		return out
	}
	out = protowire.AppendTag(out, field, protowire.BytesType)
	return protowire.AppendBytes(out, value)
}

func appendStringField(out []byte, field protowire.Number, value string) []byte {
	return appendBytesField(out, field, []byte(value))
}

// tronTxID is the transaction id: sha256 of the serialized raw data, the digest the
// owner signs.
func tronTxID(rawBytes []byte) []byte {
	sum := sha256.Sum256(rawBytes)
	return sum[:]
}

// encodeTronTransaction is Transaction{raw_data, signature...} as broadcast.
func encodeTronTransaction(rawBytes []byte, signatures ...[]byte) []byte {
	var out []byte
	out = protowire.AppendTag(out, tronTransactionRawDataField, protowire.BytesType)
	out = protowire.AppendBytes(out, rawBytes)
	for _, signature := range signatures {
		out = protowire.AppendTag(out, tronTransactionSignatureField, protowire.BytesType)
		out = protowire.AppendBytes(out, signature)
	}
	return out
}

// tronSignedSize is the serialized size of rawBytes signed by one key.
func tronSignedSize(rawBytes []byte) int64 {
	raw := protowire.SizeTag(tronTransactionRawDataField) + protowire.SizeBytes(len(rawBytes))
	signature := protowire.SizeTag(tronTransactionSignatureField) + protowire.SizeBytes(tronSignatureSize)
	return int64(raw + signature)
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

// decodeTronTransaction splits a serialized Transaction into its raw data bytes and
// signatures; any other field (ret, unknown) is refused.
func decodeTronTransaction(encoded []byte) ([]byte, [][]byte, error) {
	var rawBytes []byte
	var signatures [][]byte
	err := walkTronFields(encoded, func(field protowire.Number, wireType protowire.Type, value []byte, _ uint64) error {
		if wireType != protowire.BytesType {
			return fmt.Errorf("field %d has wire type %d", field, wireType)
		}
		switch field {
		case tronTransactionRawDataField:
			if rawBytes != nil {
				return fmt.Errorf("raw_data appears twice")
			}
			rawBytes = append([]byte{}, value...)
		case tronTransactionSignatureField:
			signatures = append(signatures, append([]byte(nil), value...))
		default:
			return fmt.Errorf("unexpected transaction field %d", field)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("decode tron transaction: %w", err)
	}
	if len(rawBytes) == 0 {
		return nil, nil, fmt.Errorf("decode tron transaction: raw_data is missing")
	}
	return rawBytes, signatures, nil
}

// decodeTronRawData parses raw data written by encode; fields must appear once, in
// order, and re-encoding must reproduce the input byte for byte.
func decodeTronRawData(rawBytes []byte) (tronRawData, error) {
	var raw tronRawData
	var contractBytes []byte
	last := protowire.Number(0)
	err := walkTronFields(rawBytes, func(field protowire.Number, wireType protowire.Type, value []byte, number uint64) error {
		if field <= last {
			return fmt.Errorf("raw_data field %d out of order", field)
		}
		last = field
		switch field {
		case tronRawRefBlockBytesField:
			raw.refBlockBytes = append([]byte(nil), value...)
		case tronRawRefBlockHashField:
			raw.refBlockHash = append([]byte(nil), value...)
		case tronRawExpirationField:
			raw.expiration = int64(number)
		case tronRawContractField:
			contractBytes = value
		case tronRawTimestampField:
			raw.timestamp = int64(number)
		case tronRawFeeLimitField:
			raw.feeLimit = int64(number)
		default:
			return fmt.Errorf("unexpected raw_data field %d", field)
		}
		return requireTronWireType(field, wireType, field == tronRawExpirationField || field == tronRawTimestampField || field == tronRawFeeLimitField)
	})
	if err != nil {
		return tronRawData{}, fmt.Errorf("decode tron raw data: %w", err)
	}
	contract, err := decodeTronContract(contractBytes)
	if err != nil {
		return tronRawData{}, err
	}
	raw.contract = contract
	reencoded, err := raw.encode()
	if err != nil {
		return tronRawData{}, fmt.Errorf("decode tron raw data: %w", err)
	}
	if string(reencoded) != string(rawBytes) {
		return tronRawData{}, fmt.Errorf("decode tron raw data: input is not in canonical form")
	}
	return raw, nil
}

func decodeTronContract(encoded []byte) (tronContract, error) {
	if len(encoded) == 0 {
		return tronContract{}, fmt.Errorf("decode tron contract: contract is missing")
	}
	var contract tronContract
	var typeURL string
	var value []byte
	err := walkTronFields(encoded, func(field protowire.Number, wireType protowire.Type, payload []byte, number uint64) error {
		switch field {
		case tronContractTypeField:
			contract.contractType = int32(number)
			return requireTronWireType(field, wireType, true)
		case tronContractParameterField:
			if err := requireTronWireType(field, wireType, false); err != nil {
				return err
			}
			return walkTronFields(payload, func(anyField protowire.Number, anyType protowire.Type, anyPayload []byte, _ uint64) error {
				switch anyField {
				case tronAnyTypeURLField:
					typeURL = string(anyPayload)
				case tronAnyValueField:
					value = anyPayload
				default:
					return fmt.Errorf("unexpected Any field %d", anyField)
				}
				return requireTronWireType(anyField, anyType, false)
			})
		default:
			return fmt.Errorf("unexpected contract field %d", field)
		}
	})
	if err != nil {
		return tronContract{}, fmt.Errorf("decode tron contract: %w", err)
	}
	switch {
	case contract.contractType == tronContractTypeTransfer && typeURL == tronTransferContractTypeURL:
		err = decodeTronTransferValue(value, &contract)
	case contract.contractType == tronContractTypeTriggerSmart && typeURL == tronTriggerSmartContractTypeURL:
		err = decodeTronTriggerValue(value, &contract)
	default:
		err = fmt.Errorf("contract type %d with type_url %q is not supported", contract.contractType, typeURL)
	}
	if err != nil {
		return tronContract{}, fmt.Errorf("decode tron contract: %w", err)
	}
	return contract, nil
}

func decodeTronTransferValue(value []byte, contract *tronContract) error {
	return walkTronFields(value, func(field protowire.Number, wireType protowire.Type, payload []byte, number uint64) error {
		switch field {
		case tronTransferOwnerField:
			contract.owner = append([]byte(nil), payload...)
		case tronTransferToField:
			contract.to = append([]byte(nil), payload...)
		case tronTransferAmountField:
			contract.amount = int64(number)
		default:
			return fmt.Errorf("unexpected TransferContract field %d", field)
		}
		return requireTronWireType(field, wireType, field == tronTransferAmountField)
	})
}

func decodeTronTriggerValue(value []byte, contract *tronContract) error {
	return walkTronFields(value, func(field protowire.Number, wireType protowire.Type, payload []byte, number uint64) error {
		switch field {
		case tronTriggerOwnerField:
			contract.owner = append([]byte(nil), payload...)
		case tronTriggerContractField:
			contract.contract = append([]byte(nil), payload...)
		case tronTriggerCallValueField:
			contract.callValue = int64(number)
		case tronTriggerDataField:
			contract.data = append([]byte(nil), payload...)
		default:
			return fmt.Errorf("unexpected TriggerSmartContract field %d", field)
		}
		return requireTronWireType(field, wireType, field == tronTriggerCallValueField)
	})
}

func requireTronWireType(field protowire.Number, got protowire.Type, wantVarint bool) error {
	want := protowire.BytesType
	if wantVarint {
		want = protowire.VarintType
	}
	if got != want {
		return fmt.Errorf("field %d has wire type %d, want %d", field, got, want)
	}
	return nil
}

// walkTronFields calls visit for every field of a protobuf message: payload for
// length-delimited fields, number for varints. Groups and fixed-width types are
// refused (no TRON field this adapter reads uses them).
func walkTronFields(message []byte, visit func(field protowire.Number, wireType protowire.Type, payload []byte, number uint64) error) error {
	for len(message) > 0 {
		field, wireType, tagLength := protowire.ConsumeTag(message)
		if tagLength < 0 {
			return protowire.ParseError(tagLength)
		}
		message = message[tagLength:]
		var payload []byte
		var number uint64
		var valueLength int
		switch wireType {
		case protowire.VarintType:
			number, valueLength = protowire.ConsumeVarint(message)
		case protowire.BytesType:
			payload, valueLength = protowire.ConsumeBytes(message)
		default:
			return fmt.Errorf("field %d has unsupported wire type %d", field, wireType)
		}
		if valueLength < 0 {
			return protowire.ParseError(valueLength)
		}
		message = message[valueLength:]
		if err := visit(field, wireType, payload, number); err != nil {
			return err
		}
	}
	return nil
}

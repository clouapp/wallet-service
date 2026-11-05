package ramps

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
)

func validOfframpSnapshot() estimateSnapshot {
	return estimateSnapshot{
		SourceAmount:   "18.500000",
		SourceCurrency: "USDC",
		PixKey:         "k",
		PixKeyType:     "cpf",
		Chain:          "solana",
		FromAddress:    "8nWZBsolanawallet",
	}
}

func validOfframpCreateInput() dto.CreateOfframpDTO {
	return dto.CreateOfframpDTO{
		EstimateOfframpDTO: dto.EstimateOfframpDTO{
			AmountToken: "18.500000",
			Token:       "USDC",
			Chain:       "solana",
			PixKey:      "k",
			PixKeyType:  "cpf",
			FromAddress: "8nWZBsolanawallet",
		},
	}
}

func TestBindOfframpCreate(t *testing.T) {
	toPhoneKey := func(in *dto.CreateOfframpDTO, snap *estimateSnapshot) {
		snap.PixKeyType, snap.PixKey, snap.RecipientDocument = "phone", "+5511999999999", "39053344705"
		in.PixKeyType, in.PixKey = "phone", "+5511999999999"
	}
	tests := []struct {
		name    string
		mutate  func(*dto.CreateOfframpDTO, *estimateSnapshot)
		wantErr error
	}{
		{name: "matching input", mutate: func(*dto.CreateOfframpDTO, *estimateSnapshot) {}},
		{
			name:   "equivalent amounts",
			mutate: func(_ *dto.CreateOfframpDTO, snap *estimateSnapshot) { snap.SourceAmount = "18.5" },
		},
		{
			name:    "amount mismatch",
			mutate:  func(in *dto.CreateOfframpDTO, _ *estimateSnapshot) { in.AmountToken = "1.000000" },
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name:    "pix key mismatch",
			mutate:  func(in *dto.CreateOfframpDTO, _ *estimateSnapshot) { in.PixKey = "other" },
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name:    "pix key type mismatch",
			mutate:  func(in *dto.CreateOfframpDTO, _ *estimateSnapshot) { in.PixKeyType = "email" },
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name:    "token mismatch",
			mutate:  func(in *dto.CreateOfframpDTO, _ *estimateSnapshot) { in.Token = "USDT" },
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name:    "from address mismatch",
			mutate:  func(in *dto.CreateOfframpDTO, _ *estimateSnapshot) { in.FromAddress = "other-wallet" },
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name:    "chain mismatch",
			mutate:  func(in *dto.CreateOfframpDTO, _ *estimateSnapshot) { in.Chain = "polygon" },
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name: "recipient name mismatch",
			mutate: func(in *dto.CreateOfframpDTO, snap *estimateSnapshot) {
				snap.RecipientName, in.RecipientName = "Luiza Souza", "Other Person"
			},
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name: "phone key with another recipient document",
			mutate: func(in *dto.CreateOfframpDTO, snap *estimateSnapshot) {
				toPhoneKey(in, snap)
				in.RecipientDocument = "12345678901"
			},
			wantErr: ramp.ErrEstimateMismatch,
		},
		{
			name: "phone key with the same recipient document",
			mutate: func(in *dto.CreateOfframpDTO, snap *estimateSnapshot) {
				toPhoneKey(in, snap)
				in.RecipientDocument = "39053344705"
			},
		},
		{
			name: "cpf key with another recipient document",
			mutate: func(in *dto.CreateOfframpDTO, snap *estimateSnapshot) {
				snap.RecipientDocument, in.RecipientDocument = "39053344705", "12345678901"
			},
			wantErr: ramp.ErrEstimateMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := validOfframpSnapshot()
			in := validOfframpCreateInput()
			tt.mutate(&in, &snap)

			require.ErrorIs(t, bindOfframpCreate(in, &snap), tt.wantErr)
		})
	}
}

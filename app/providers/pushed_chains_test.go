package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

type activeSubscriptions struct {
	subs []models.WebhookSubscription
	err  error
}

func (a activeSubscriptions) FindAllActive(context.Context) ([]models.WebhookSubscription, error) {
	return a.subs, a.err
}

func TestPushed_Chains_AreTheChainsOfTheActiveSubscriptionsOnceEachInOrder(t *testing.T) {
	source := PushedChains(activeSubscriptions{subs: []models.WebhookSubscription{
		{ChainID: "sol", Provider: "helius"}, {ChainID: "arbitrum", Provider: "alchemy"},
		{ChainID: "eth", Provider: "alchemy"}, {ChainID: "arbitrum", Provider: "quicknode"},
	}})

	chains, err := source.PushedChains(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"arbitrum", "eth", "sol"}, chains)
}

func TestPushed_Chains_PassOnAReadThatFails(t *testing.T) {
	_, err := PushedChains(activeSubscriptions{err: errors.New("db down")}).PushedChains(context.Background())

	require.Error(t, err)
}

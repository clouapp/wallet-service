package providers

import (
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadOptionsFor(t *testing.T, region, profile string) awsconfig.LoadOptions {
	t.Helper()
	var options awsconfig.LoadOptions
	for _, apply := range awsLoadOptions(region, profile) {
		require.NoError(t, apply(&options))
	}
	return options
}

func TestAWS_LoadOptions_UseTheConfiguredRegionAndProfile(t *testing.T) {
	options := loadOptionsFor(t, "us-east-1", "waas-dev")

	assert.Equal(t, "us-east-1", options.Region)
	assert.Equal(t, "waas-dev", options.SharedConfigProfile)
}

func TestAWS_LoadOptions_LeaveTheProfileToTheSDKWhenEmpty(t *testing.T) {
	options := loadOptionsFor(t, "us-east-1", "")

	assert.Equal(t, "us-east-1", options.Region)
	assert.Empty(t, options.SharedConfigProfile)
}

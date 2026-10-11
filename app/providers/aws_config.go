package providers

import awsconfig "github.com/aws/aws-sdk-go-v2/config"

// awsLoadOptions turns vault.aws.region and vault.aws.profile into SDK load options.
// A process under supervisor does not inherit .env, so AWS_REGION/AWS_PROFILE there
// only reach the SDK through the app config. An empty profile keeps the SDK's own
// resolution (environment, then the default profile).
func awsLoadOptions(region, profile string) []func(*awsconfig.LoadOptions) error {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(profile))
	}
	return options
}

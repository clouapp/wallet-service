package snapshot

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	listPageSize          = 100
	localstackAccessKey   = "test"
	localstackSecretKey   = "test"
	minClientTokenLength  = 32
	maxClientTokenLength  = 64
	resourceNotFoundCode  = "ResourceNotFoundException"
	unknownAWSErrorCode   = "unknown"
	internalRequestHeader = "x-localstack-data"
	internalRequestDTO    = "{}"
)

var arnSuffixPattern = regexp.MustCompile(`-([A-Za-z]{6})$`)

// SecretsAPI is the slice of Secrets Manager the tool needs.
type SecretsAPI interface {
	ListSecretNames(ctx context.Context) ([]string, error)
	ReadSecret(ctx context.Context, name string) (Entry, error)
	CreateWithOriginalARN(ctx context.Context, entry Entry) (string, error)
}

// AWSSecrets talks to the LocalStack Secrets Manager endpoint.
type AWSSecrets struct {
	client *secretsmanager.Client
}

// NewAWSSecrets builds a client for the LocalStack endpoint with its fixed test
// credentials. internal marks every request as a LocalStack-internal call, which
// the gateway still serves while the runtime is shutting down (external calls get
// 503 then), so a shutdown hook can take a final snapshot over HTTP.
func NewAWSSecrets(cfg Config, internal bool) *AWSSecrets {
	awsConfig := aws.Config{
		Region: cfg.Region,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: localstackAccessKey, SecretAccessKey: localstackSecretKey}, nil
		}),
	}
	client := secretsmanager.NewFromConfig(awsConfig, func(options *secretsmanager.Options) {
		options.BaseEndpoint = aws.String(cfg.EndpointURL)
		if internal {
			options.APIOptions = append(options.APIOptions, smithyhttp.SetHeaderValue(internalRequestHeader, internalRequestDTO))
		}
	})
	return &AWSSecrets{client: client}
}

func (a *AWSSecrets) ListSecretNames(ctx context.Context) ([]string, error) {
	var names []string
	paginator := secretsmanager.NewListSecretsPaginator(a.client, &secretsmanager.ListSecretsInput{MaxResults: aws.Int32(listPageSize)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, listed := range page.SecretList {
			if listed.DeletedDate == nil && listed.Name != nil {
				names = append(names, *listed.Name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func (a *AWSSecrets) ReadSecret(ctx context.Context, name string) (Entry, error) {
	described, err := a.client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(name)})
	if err != nil {
		return Entry{}, err
	}
	value, err := a.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(name)})
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{
		Name:         name,
		ARN:          aws.ToString(value.ARN),
		VersionID:    value.VersionId,
		Description:  described.Description,
		Tags:         withoutCustomID(fromAWSTags(described.Tags)),
		SecretString: value.SecretString,
	}
	if value.SecretBinary != nil {
		encoded := base64.StdEncoding.EncodeToString(value.SecretBinary)
		zero(value.SecretBinary)
		entry.SecretBinaryB64 = &encoded
	}
	return entry, nil
}

// CreateWithOriginalARN recreates a secret; the moto _custom_id_ tag makes the new
// ARN end with the original 6-letter suffix, and is removed right after.
func (a *AWSSecrets) CreateWithOriginalARN(ctx context.Context, entry Entry) (string, error) {
	suffix := arnSuffixPattern.FindStringSubmatch(entry.ARN)
	tags := append([]Tag(nil), entry.Tags...)
	if suffix != nil {
		tags = append(tags, Tag{Key: CustomIDTag, Value: suffix[1]})
	}
	input := &secretsmanager.CreateSecretInput{Name: aws.String(entry.Name), Tags: toAWSTags(tags)}
	if entry.Description != nil && *entry.Description != "" {
		input.Description = entry.Description
	}
	if entry.SecretBinaryB64 != nil {
		binary, err := base64.StdEncoding.DecodeString(*entry.SecretBinaryB64)
		if err != nil {
			return "", safeErrorf("snapshot holds a SecretBinary of %s that is not base64", entry.Name)
		}
		defer zero(binary)
		input.SecretBinary = binary
	} else {
		input.SecretString = entry.SecretString
	}
	if entry.VersionID != nil && len(*entry.VersionID) >= minClientTokenLength && len(*entry.VersionID) <= maxClientTokenLength {
		input.ClientRequestToken = entry.VersionID
	}
	created, err := a.client.CreateSecret(ctx, input)
	if err != nil {
		return "", err
	}
	if suffix != nil {
		if _, err := a.client.UntagResource(ctx, &secretsmanager.UntagResourceInput{
			SecretId: created.ARN,
			TagKeys:  []string{CustomIDTag},
		}); err != nil {
			return "", err
		}
	}
	return aws.ToString(created.ARN), nil
}

func fromAWSTags(tags []smtypes.Tag) []Tag {
	converted := make([]Tag, 0, len(tags))
	for _, tag := range tags {
		converted = append(converted, Tag{Key: aws.ToString(tag.Key), Value: aws.ToString(tag.Value)})
	}
	return converted
}

func toAWSTags(tags []Tag) []smtypes.Tag {
	converted := make([]smtypes.Tag, 0, len(tags))
	for _, tag := range tags {
		converted = append(converted, smtypes.Tag{Key: aws.String(tag.Key), Value: aws.String(tag.Value)})
	}
	return converted
}

// awsErrorCode returns the API error code (e.g. ResourceNotFoundException), or ""
// when err is not an AWS API error (network, serialization...).
func awsErrorCode(err error) string {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		if code := apiError.ErrorCode(); code != "" {
			return code
		}
		return unknownAWSErrorCode
	}
	return ""
}

func isNotFound(err error) bool {
	return awsErrorCode(err) == resourceNotFoundCode
}

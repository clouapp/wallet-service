module github.com/macromarkets/vault

go 1.22

require (
	// Goravel framework
	github.com/goravel/framework v1.14.1
	github.com/goravel/gin v1.2.3

	// AWS
	github.com/aws/aws-lambda-go v1.47.0
	github.com/aws/aws-sdk-go-v2 v1.26.0
	github.com/aws/aws-sdk-go-v2/config v1.27.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.31.0
	github.com/aws/aws-sdk-go-v2/service/kms v1.30.0
	github.com/awslabs/aws-lambda-go-api-proxy v0.16.2

	// HTTP + Database
	github.com/gin-gonic/gin v1.9.1
	github.com/jmoiron/sqlx v1.3.5
	github.com/lib/pq v1.10.9
	github.com/goravel/postgres v0.2.0

	// Redis (for address cache only — queues are SQS)
	github.com/redis/go-redis/v9 v9.5.1

	// Crypto
	github.com/google/uuid v1.6.0
	golang.org/x/crypto v0.22.0

	// Testing
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/stretchr/testify v1.9.0
	github.com/alicebob/miniredis/v2 v2.33.0
)

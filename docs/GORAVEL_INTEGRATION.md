# Goravel Integration - Migration System

## Overview

The wallet-service now uses **Goravel's migration system** for database schema management while maintaining its serverless Lambda architecture for the core application logic.

## Key Components

### 1. Models (`app/models/`)

All models now use Goravel's ORM pattern:

- **`orm.Model`**: Embedded in all models, provides ID, CreatedAt, UpdatedAt fields
- **UUID Primary Keys**: All tables use UUID primary keys
- **GORM Tags**: Define database schema using GORM struct tags
- **Relationships**: Support for Has Many, Belongs To relationships

**Example**:
```go
type Wallet struct {
    orm.Model
    ID             uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
    Chain          string    `gorm:"type:varchar(50);not null;index" json:"chain"`
    Label          string    `gorm:"type:varchar(255)" json:"label"`
    // ... other fields
}
```

### 2. Migrations (`database/migrations/`)

Each migration file follows Goravel's structure:

- **Signature()**: Unique migration identifier
- **Up()**: Create/modify database schema
- **Down()**: Rollback changes

**Available Migrations**:
1. `20260317000001_create_wallets_table.go` - HD wallet storage
2. `20260317000002_create_addresses_table.go` - Derived addresses for deposits
3. `20260317000003_create_transactions_table.go` - Transaction history
4. `20260317000004_create_webhook_configs_table.go` - Webhook configurations

### 3. Migration Command (`cmd/migrate/main.go`)

Custom migration runner that integrates with Goravel's Schema facade:

```bash
# Run all pending migrations
make migrate

# Show migration status
make migrate-status

# Rollback last batch
make migrate-rollback

# Drop all tables and re-migrate
make migrate-fresh
```

## Database Schema

### Wallets Table
```sql
CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    chain VARCHAR(50) NOT NULL,
    label VARCHAR(255),
    master_pubkey TEXT NOT NULL,
    key_vault_ref VARCHAR(255) NOT NULL,
    derivation_path VARCHAR(100) NOT NULL,
    address_index INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

### Addresses Table
```sql
CREATE TABLE addresses (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    chain VARCHAR(50) NOT NULL,
    address VARCHAR(255) NOT NULL UNIQUE,
    derivation_index INT NOT NULL,
    external_user_id VARCHAR(255) NOT NULL,
    metadata TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

### Transactions Table
```sql
CREATE TABLE transactions (
    id UUID PRIMARY KEY,
    address_id UUID REFERENCES addresses(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    external_user_id VARCHAR(255) NOT NULL,
    chain VARCHAR(50) NOT NULL,
    tx_type VARCHAR(20) NOT NULL,
    tx_hash VARCHAR(255),
    from_address VARCHAR(255),
    to_address VARCHAR(255) NOT NULL,
    amount VARCHAR(100) NOT NULL,
    asset VARCHAR(50) NOT NULL,
    token_contract VARCHAR(255),
    confirmations INT NOT NULL DEFAULT 0,
    required_confs INT NOT NULL DEFAULT 12,
    status VARCHAR(20) NOT NULL,
    fee VARCHAR(100),
    block_number BIGINT,
    block_hash VARCHAR(255),
    error_message TEXT,
    idempotency_key VARCHAR(255) UNIQUE,
    confirmed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

### Webhook Configs Table
```sql
CREATE TABLE webhook_configs (
    id UUID PRIMARY KEY,
    url VARCHAR(500) NOT NULL,
    secret VARCHAR(255) NOT NULL,
    events TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

## Environment Configuration

Add these database variables to `.env.dev`:

```bash
DB_CONNECTION=postgres
DB_HOST=localhost
DB_PORT=5432
DB_DATABASE=vault
DB_USERNAME=vault
DB_PASSWORD=vault
```

## Migration Workflow

### Development

1. **Start Database**:
   ```bash
   make docker-up
   ```

2. **Run Migrations**:
   ```bash
   make migrate
   ```

3. **Verify Schema**:
   ```bash
   make migrate-status
   ```

### Creating New Migrations

To create a new migration:

1. **Create Migration File**:
   ```bash
   touch database/migrations/20260318000001_add_column_to_users.go
   ```

2. **Implement Migration**:
   ```go
   package migrations

   import (
       "github.com/goravel/framework/contracts/database/schema"
       "github.com/goravel/framework/facades"
   )

   type M20260318000001AddColumnToUsers struct {}

   func (r *M20260318000001AddColumnToUsers) Signature() string {
       return "20260318000001_add_column_to_users"
   }

   func (r *M20260318000001AddColumnToUsers) Up() error {
       return facades.Schema().Table("users", func(table schema.Blueprint) {
           table.String("new_column", 255).Nullable()
       })
   }

   func (r *M20260318000001AddColumnToUsers) Down() error {
       return facades.Schema().Table("users", func(table schema.Blueprint) {
           table.DropColumn("new_column")
       })
   }
   ```

3. **Register Migration** in `bootstrap/migrations.go`:
   ```go
   func Migrations() []schema.Migration {
       return []schema.Migration{
           // ... existing migrations
           &migrations.M20260318000001AddColumnToUsers{},
       }
   }
   ```

4. **Run Migration**:
   ```bash
   make migrate
   ```

## Goravel Schema Builder Methods

### Column Types
- `String(name, length)` - VARCHAR column
- `Text(name)` - TEXT column
- `Integer(name)` - INT column
- `BigInteger(name)` - BIGINT column
- `Boolean(name)` - BOOLEAN column
- `Timestamp(name)` - TIMESTAMP column
- `Uuid(name)` - UUID column
- `Timestamps()` - Adds created_at and updated_at

### Column Modifiers
- `.Nullable()` - Allow NULL values
- `.Default(value)` - Set default value
- `.Comment(text)` - Add column comment
- `.Primary()` - Set as primary key
- `.Unsigned()` - Make integer unsigned (MySQL)

### Indexes
- `table.Index(columns...)` - Create index
- `table.Unique(columns...)` - Create unique index
- `table.Primary(columns...)` - Create primary key
- `table.Foreign(column).References(column).On(table)` - Foreign key constraint

### Table Operations
- `facades.Schema().Create(name, callback)` - Create new table
- `facades.Schema().Table(name, callback)` - Modify existing table
- `facades.Schema().Drop(name)` - Drop table
- `facades.Schema().DropIfExists(name)` - Drop table if exists
- `facades.Schema().Rename(from, to)` - Rename table

## Known Issues & TODO

### ⚠️ Current Status: INCOMPLETE

The Goravel integration is **partially complete**. The following work remains:

1. **Service Layer Compatibility** ❌
   - Services still use `database/sql` types (sql.NullString, etc.)
   - Need to update all services to work with Goravel ORM models
   - Files affected:
     - `app/services/wallet/service.go`
     - `app/services/webhook/service.go`
     - `app/services/deposit/service.go`
     - `app/services/withdraw/service.go`

2. **Migration Command** ⚠️
   - Basic cmd/migrate/main.go created but not fully tested
   - Need to properly initialize Goravel's Schema facade
   - Database connection configuration incomplete

3. **Testing** ❌
   - All unit tests currently use sqlx mocks
   - Need to create Goravel ORM-compatible test mocks
   - Migration tests not yet written

4. **Lambda Integration** ❓
   - Unclear how Goravel ORM will work with Lambda cold starts
   - May need connection pooling adjustments
   - Need to test actual Lambda deployments

## Next Steps

1. **Fix Service Layer** (High Priority)
   - Update services to use Goravel ORM queries
   - Replace `sqlx` usage with `facades.Orm().Query()`
   - Update model instantiation (remove manual timestamp fields)

2. **Complete Migration Runner** (High Priority)
   - Fix database configuration loading
   - Test migration up/down/status commands
   - Add migration tracking table

3. **Update Tests** (Medium Priority)
   - Create Goravel ORM test helpers
   - Update all service tests
   - Add migration tests

4. **Documentation** (Medium Priority)
   - Update CLAUDE.md with Goravel ORM usage
   - Add ORM query examples
   - Document model relationships

5. **Production Readiness** (Low Priority)
   - Test Lambda cold start performance
   - Optimize database connections
   - Add migration rollback safety checks

## References

- [Goravel ORM Documentation](https://goravel.dev/orm/getting-started)
- [Goravel Migrations Documentation](https://goravel.dev/database/migrations)
- [GORM Tags Reference](https://gorm.io/docs/models.html)

---

**Last Updated**: March 17, 2026
**Status**: 🟡 In Progress (40% Complete)
**Maintained By**: Vault Development Team

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE wallets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(10) NOT NULL UNIQUE,
    label           VARCHAR(100) DEFAULT '',
    master_pubkey   TEXT NOT NULL,
    key_vault_ref   VARCHAR(255) NOT NULL,
    derivation_path TEXT NOT NULL,
    address_index   INTEGER DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE addresses (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id         UUID NOT NULL REFERENCES wallets(id),
    chain             VARCHAR(10) NOT NULL,
    address           VARCHAR(255) NOT NULL,
    derivation_index  INTEGER NOT NULL,
    external_user_id  VARCHAR(255) NOT NULL,
    metadata          JSONB DEFAULT '{}',
    is_active         BOOLEAN DEFAULT TRUE,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(chain, address),
    UNIQUE(wallet_id, derivation_index)
);
CREATE INDEX idx_addresses_user ON addresses(external_user_id);
CREATE INDEX idx_addresses_chain_addr ON addresses(chain, address);

CREATE TABLE transactions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    address_id       UUID REFERENCES addresses(id),
    wallet_id        UUID NOT NULL REFERENCES wallets(id),
    external_user_id VARCHAR(255) DEFAULT '',
    chain            VARCHAR(10) NOT NULL,
    tx_type          VARCHAR(10) NOT NULL CHECK (tx_type IN ('deposit', 'withdrawal')),
    tx_hash          VARCHAR(255),
    from_address     VARCHAR(255),
    to_address       VARCHAR(255) NOT NULL,
    amount           NUMERIC(36, 18) NOT NULL,
    asset            VARCHAR(20) NOT NULL,
    token_contract   VARCHAR(255),
    confirmations    INTEGER DEFAULT 0,
    required_confs   INTEGER NOT NULL DEFAULT 1,
    status           VARCHAR(20) NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'confirming', 'confirmed', 'failed')),
    fee              NUMERIC(36, 18),
    block_number     BIGINT,
    block_hash       VARCHAR(255),
    error_message    TEXT,
    idempotency_key  VARCHAR(255) UNIQUE,
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    confirmed_at     TIMESTAMPTZ
);
CREATE INDEX idx_tx_status ON transactions(status);
CREATE INDEX idx_tx_chain_hash ON transactions(chain, tx_hash);
CREATE INDEX idx_tx_user ON transactions(external_user_id);
CREATE INDEX idx_tx_idempotency ON transactions(idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE webhook_configs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url         VARCHAR(500) NOT NULL,
    secret      VARCHAR(255) NOT NULL,
    events      TEXT[] NOT NULL,
    is_active   BOOLEAN DEFAULT TRUE,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE webhook_events (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id   UUID NOT NULL REFERENCES transactions(id),
    event_type       VARCHAR(50) NOT NULL,
    payload          JSONB NOT NULL,
    delivery_url     VARCHAR(500) NOT NULL,
    delivery_status  VARCHAR(20) DEFAULT 'pending'
                     CHECK (delivery_status IN ('pending', 'delivered', 'failed')),
    attempts         INTEGER DEFAULT 0,
    max_attempts     INTEGER DEFAULT 10,
    next_retry_at    TIMESTAMPTZ,
    last_error       TEXT,
    delivered_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_webhook_pending ON webhook_events(delivery_status, next_retry_at) WHERE delivery_status = 'pending';

CREATE TABLE withdrawal_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id       UUID NOT NULL REFERENCES wallets(id),
    max_per_tx      NUMERIC(36, 18),
    max_daily       NUMERIC(36, 18),
    whitelist_only  BOOLEAN DEFAULT FALSE,
    cooldown_secs   INTEGER DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE withdrawal_whitelist (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id   UUID NOT NULL REFERENCES wallets(id),
    address     VARCHAR(255) NOT NULL,
    label       VARCHAR(100) DEFAULT '',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(wallet_id, address)
);

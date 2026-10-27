package migrations

// M00000000000700CreateWithdrawalRecoveryAttempts reserves an exact EVM sender
// nonce for one existing withdrawal. Attempts are never deleted or economically
// rewritten; execution advances by CAS and every transition has an append-only
// event. An unresolved recovery also prevents the ordinary retry path from
// switching the withdrawal back to its old transaction or changing its amount.
type M00000000000700CreateWithdrawalRecoveryAttempts struct{}

func (*M00000000000700CreateWithdrawalRecoveryAttempts) Signature() string {
	return "00000000000700_create_withdrawal_recovery_attempts"
}

func (*M00000000000700CreateWithdrawalRecoveryAttempts) Up() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS withdrawal_recovery_attempts (
 id UUID PRIMARY KEY,
 operation_id UUID NOT NULL UNIQUE REFERENCES withdrawals(id),
 original_transaction_id UUID NOT NULL REFERENCES transactions(id),
 account_id UUID NOT NULL REFERENCES accounts(id),
 wallet_id UUID NOT NULL REFERENCES wallets(id),
 source_address_id UUID NOT NULL REFERENCES addresses(id),
 network_id BIGINT NOT NULL CHECK (network_id > 0),
 sender VARCHAR(42) NOT NULL CHECK (sender = lower(sender)),
 sender_nonce NUMERIC(20,0) NOT NULL CHECK (sender_nonce >= 0 AND sender_nonce <= 18446744073709551615),
 binding_hash CHAR(64) NOT NULL,
 binding_json JSONB NOT NULL,
 proposal_hash CHAR(64) NOT NULL,
 proposal_json JSONB NOT NULL,
 review_json JSONB NOT NULL,
 operator VARCHAR(128) NOT NULL,
 state VARCHAR(32) NOT NULL CHECK (state IN ('claimed','gas_claimed','gas_ready','signing','signed','send_claimed','send_unknown','broadcast','needs_reconciliation')),
 call_json JSONB NOT NULL DEFAULT '{}',
 gas_result_json JSONB NOT NULL DEFAULT '{}',
 candidate_transaction_id UUID REFERENCES transactions(id),
 candidate_hash VARCHAR(66) NOT NULL DEFAULT '',
 sealed_signed_tx TEXT NOT NULL DEFAULT '',
 observation_json JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL,
 CONSTRAINT withdrawal_recovery_sender_nonce_unique UNIQUE (network_id,sender,sender_nonce),
 CONSTRAINT withdrawal_recovery_candidate_complete CHECK (
 (candidate_transaction_id IS NULL AND candidate_hash = '' AND sealed_signed_tx = '') OR
 (candidate_transaction_id IS NOT NULL AND candidate_hash <> '' AND sealed_signed_tx <> ''))
 )`,
		`CREATE INDEX IF NOT EXISTS withdrawal_recovery_original_transaction_index ON withdrawal_recovery_attempts(original_transaction_id)`,
		`CREATE TABLE IF NOT EXISTS withdrawal_recovery_events (
 id UUID PRIMARY KEY,
 attempt_id UUID NOT NULL REFERENCES withdrawal_recovery_attempts(id),
 from_state VARCHAR(32) NOT NULL,
 to_state VARCHAR(32) NOT NULL,
 observation_json JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL
 )`,
		`CREATE INDEX IF NOT EXISTS withdrawal_recovery_events_attempt_time ON withdrawal_recovery_events(attempt_id,created_at,id)`,
		`CREATE OR REPLACE FUNCTION guard_withdrawal_recovery_attempt() RETURNS trigger AS $$
 BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'withdrawal recovery attempts cannot be deleted'; END IF;
 IF ROW(NEW.id,NEW.operation_id,NEW.original_transaction_id,NEW.account_id,NEW.wallet_id,NEW.source_address_id,NEW.network_id,NEW.sender,NEW.sender_nonce,NEW.binding_hash,NEW.binding_json,NEW.proposal_hash,NEW.proposal_json,NEW.review_json,NEW.operator,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.operation_id,OLD.original_transaction_id,OLD.account_id,OLD.wallet_id,OLD.source_address_id,OLD.network_id,OLD.sender,OLD.sender_nonce,OLD.binding_hash,OLD.binding_json,OLD.proposal_hash,OLD.proposal_json,OLD.review_json,OLD.operator,OLD.created_at)
 THEN RAISE EXCEPTION 'withdrawal recovery economic and review binding is immutable'; END IF;
 IF OLD.call_json <> '{}'::jsonb AND NEW.call_json IS DISTINCT FROM OLD.call_json THEN RAISE EXCEPTION 'withdrawal recovery exact call is immutable'; END IF;
 IF OLD.gas_result_json <> '{}'::jsonb AND NEW.gas_result_json IS DISTINCT FROM OLD.gas_result_json THEN RAISE EXCEPTION 'withdrawal recovery gas proof is immutable'; END IF;
 IF OLD.candidate_transaction_id IS NOT NULL AND ROW(NEW.candidate_transaction_id,NEW.candidate_hash,NEW.sealed_signed_tx) IS DISTINCT FROM ROW(OLD.candidate_transaction_id,OLD.candidate_hash,OLD.sealed_signed_tx)
 THEN RAISE EXCEPTION 'withdrawal recovery signed candidate is immutable'; END IF;
 RETURN NEW;
 END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS withdrawal_recovery_attempt_immutable ON withdrawal_recovery_attempts`,
		`CREATE TRIGGER withdrawal_recovery_attempt_immutable BEFORE UPDATE OR DELETE ON withdrawal_recovery_attempts FOR EACH ROW EXECUTE FUNCTION guard_withdrawal_recovery_attempt()`,
		`CREATE OR REPLACE FUNCTION guard_withdrawal_recovery_event() RETURNS trigger AS $$
 BEGIN RAISE EXCEPTION 'withdrawal recovery events are append-only'; END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS withdrawal_recovery_event_immutable ON withdrawal_recovery_events`,
		`CREATE TRIGGER withdrawal_recovery_event_immutable BEFORE UPDATE OR DELETE ON withdrawal_recovery_events FOR EACH ROW EXECUTE FUNCTION guard_withdrawal_recovery_event()`,
		`CREATE OR REPLACE FUNCTION guard_withdrawal_recovery_aggregate() RETURNS trigger AS $$
 DECLARE attempt withdrawal_recovery_attempts%ROWTYPE; candidate_status TEXT;
 BEGIN
 SELECT * INTO attempt FROM withdrawal_recovery_attempts WHERE operation_id = OLD.id;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF ROW(NEW.id,NEW.wallet_id,NEW.account_id,NEW.amount,NEW.destination_address,NEW.destination_tag)
 IS DISTINCT FROM ROW(OLD.id,OLD.wallet_id,OLD.account_id,OLD.amount,OLD.destination_address,OLD.destination_tag)
 THEN RAISE EXCEPTION 'a claimed recovery withdrawal cannot change economic fields'; END IF;
 IF attempt.candidate_transaction_id IS NULL THEN
 IF ROW(NEW.status,NEW.transaction_id,NEW.failure_reason) IS DISTINCT FROM ROW(OLD.status,OLD.transaction_id,OLD.failure_reason)
 THEN RAISE EXCEPTION 'an occupied recovery cannot use ordinary withdrawal retry'; END IF;
 ELSE
 IF NEW.transaction_id IS DISTINCT FROM attempt.candidate_transaction_id THEN RAISE EXCEPTION 'recovery withdrawal must retain its current candidate'; END IF;
 SELECT status::text INTO candidate_status FROM transactions WHERE id = attempt.candidate_transaction_id;
 IF NEW.status = 'failed' AND candidate_status <> 'failed' THEN RAISE EXCEPTION 'stale predecessor cannot fail recovery candidate'; END IF;
 IF NEW.status = 'confirmed' AND candidate_status <> 'confirmed' THEN RAISE EXCEPTION 'stale predecessor cannot confirm recovery candidate'; END IF;
 END IF;
 RETURN NEW;
 END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS withdrawals_recovery_aggregate_guard ON withdrawals`,
		`CREATE TRIGGER withdrawals_recovery_aggregate_guard BEFORE UPDATE ON withdrawals FOR EACH ROW EXECUTE FUNCTION guard_withdrawal_recovery_aggregate()`,
		`COMMENT ON TABLE withdrawal_recovery_attempts IS 'One immutable same-business withdrawal recovery claim; occupied sender nonce never released by an absent receipt'`,
		`COMMENT ON COLUMN withdrawal_recovery_attempts.sealed_signed_tx IS 'Verified signed bytes sealed before send; retained after refusals and unknown outcomes'`,
		`COMMENT ON COLUMN transactions.parent_transaction_id IS 'Parent withdrawal for sweeps/gas seeds; for explicit recovery withdrawals, original transaction identified again by recovery_predecessor_transaction_id in raw_payload'`,
	}
	for _, statement := range statements {
		if _, err := migrationQuery().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (*M00000000000700CreateWithdrawalRecoveryAttempts) Down() error {
	statements := []string{
		`DROP TRIGGER IF EXISTS withdrawals_recovery_aggregate_guard ON withdrawals`,
		`DROP FUNCTION IF EXISTS guard_withdrawal_recovery_aggregate()`,
		`DROP TABLE IF EXISTS withdrawal_recovery_events`,
		`DROP FUNCTION IF EXISTS guard_withdrawal_recovery_event()`,
		`DROP TABLE IF EXISTS withdrawal_recovery_attempts`,
		`DROP FUNCTION IF EXISTS guard_withdrawal_recovery_attempt()`,
	}
	for _, statement := range statements {
		if _, err := migrationQuery().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

CREATE SCHEMA IF NOT EXISTS macro_ramps;







CREATE TABLE macro_ramps.admin_audit_logs (
    id bigint NOT NULL,
    admin_id bigint,
    action character varying(64) NOT NULL,
    subject_type character varying(32),
    subject_id bigint,
    provider character varying(32),
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip character varying(64) DEFAULT ''::character varying NOT NULL,
    created_at timestamp(6) without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);



CREATE SEQUENCE macro_ramps.admin_audit_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.admin_audit_logs_id_seq OWNED BY macro_ramps.admin_audit_logs.id;



CREATE TABLE macro_ramps.admins (
    id bigint NOT NULL,
    name character varying(255) NOT NULL,
    email character varying(255) NOT NULL,
    password character varying(255) NOT NULL,
    email_verified_at timestamp(0) without time zone,
    last_login_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    sessions_revoked_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.admins_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.admins_id_seq OWNED BY macro_ramps.admins.id;



CREATE TABLE macro_ramps.bank_account_destinations (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    user_bank_account_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    destination_ref character varying(64) NOT NULL,
    payment_rails text DEFAULT '[]'::text NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.bank_account_destinations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.bank_account_destinations_id_seq OWNED BY macro_ramps.bank_account_destinations.id;



CREATE TABLE macro_ramps.customer_associated_persons (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    local_ref character varying(64) NOT NULL,
    roles jsonb NOT NULL,
    ownership_percent numeric(5,2),
    individual_customer_id bigint,
    profile text NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_associated_persons_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_associated_persons_id_seq OWNED BY macro_ramps.customer_associated_persons.id;



CREATE TABLE macro_ramps.customer_bank_beneficiaries (
    id bigint NOT NULL,
    external_id uuid NOT NULL,
    customer_id bigint NOT NULL,
    label character varying(64) NOT NULL,
    account_class character varying(16) NOT NULL,
    account_type character varying(16) NOT NULL,
    relationship character varying(32) NOT NULL,
    account_last4 character varying(4) NOT NULL,
    routing_last4 character varying(4) NOT NULL,
    fingerprint character varying(64) NOT NULL,
    revision integer DEFAULT 1 NOT NULL,
    routing_number text NOT NULL,
    account_number text NOT NULL,
    beneficiary_name text NOT NULL,
    address_line_1 text NOT NULL,
    address_line_2 text,
    city text NOT NULL,
    state_code text NOT NULL,
    postal_code text NOT NULL,
    business_industry text,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_bank_beneficiaries_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_bank_beneficiaries_id_seq OWNED BY macro_ramps.customer_bank_beneficiaries.id;



CREATE TABLE macro_ramps.customer_bank_beneficiary_provider_accounts (
    id bigint NOT NULL,
    external_id uuid NOT NULL,
    beneficiary_id bigint NOT NULL,
    beneficiary_revision integer NOT NULL,
    provider character varying(32) NOT NULL,
    rail character varying(32) NOT NULL,
    customer_external_id character varying(64) NOT NULL,
    external_account_id character varying(64),
    status character varying(16) NOT NULL,
    registration_idempotency_key character varying(255) NOT NULL,
    registration_request_hash character varying(64) NOT NULL,
    registration_attempt integer DEFAULT 1 NOT NULL,
    registration_attempted_at timestamp(0) without time zone,
    retry_deadline timestamp(0) without time zone,
    last_checked_at timestamp(0) without time zone,
    failure_code character varying(64),
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    CONSTRAINT customer_bank_beneficiary_provider_accounts_status_check CHECK ((((status)::text = ANY ((ARRAY['pending'::character varying, 'verifying'::character varying, 'approved'::character varying, 'rejected'::character varying, 'failed'::character varying, 'superseded'::character varying])::text[])) AND (((status)::text <> ALL ((ARRAY['verifying'::character varying, 'approved'::character varying, 'rejected'::character varying])::text[])) OR (external_account_id IS NOT NULL))))
);



CREATE SEQUENCE macro_ramps.customer_bank_beneficiary_provider_accounts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_bank_beneficiary_provider_accounts_id_seq OWNED BY macro_ramps.customer_bank_beneficiary_provider_accounts.id;



CREATE TABLE macro_ramps.customer_business_profiles (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    profile text NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_business_profiles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_business_profiles_id_seq OWNED BY macro_ramps.customer_business_profiles.id;



CREATE TABLE macro_ramps.customer_individual_profiles (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    profile text NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    tax_id_hash character varying(64),
    phone_hash character varying(64)
);



CREATE SEQUENCE macro_ramps.customer_individual_profiles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_individual_profiles_id_seq OWNED BY macro_ramps.customer_individual_profiles.id;



CREATE TABLE macro_ramps.customer_members (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    user_id bigint NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_members_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_members_id_seq OWNED BY macro_ramps.customer_members.id;



CREATE TABLE macro_ramps.customer_provider_integrations (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    external_customer_id character varying(64),
    status character varying(16) NOT NULL,
    metadata jsonb NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_provider_integrations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_provider_integrations_id_seq OWNED BY macro_ramps.customer_provider_integrations.id;



CREATE TABLE macro_ramps.customer_provider_kycs (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    kyc_status character varying(24) NOT NULL,
    preferred_mode character varying(24) NOT NULL,
    collection_mode character varying(24) NOT NULL,
    tos_status character varying(16) NOT NULL,
    tos_link_url text,
    signed_agreement_id character varying(64),
    hosted_kyc_link_id character varying(64),
    hosted_kyc_url text,
    endorsements jsonb NOT NULL,
    requirements jsonb NOT NULL,
    rejection_reasons jsonb NOT NULL,
    raw_status jsonb NOT NULL,
    last_synced_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    last_error_code character varying(64),
    last_error_detail text,
    last_error_at timestamp(0) without time zone,
    tos_session_id character varying(64),
    tos_expires_at timestamp(0) without time zone,
    tos_accepted_at timestamp(0) without time zone,
    rejection_categories jsonb DEFAULT '[]'::jsonb NOT NULL,
    rejection_details jsonb DEFAULT '[]'::jsonb NOT NULL,
    associated_person_ids jsonb,
    application_deferred_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_provider_kycs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_provider_kycs_id_seq OWNED BY macro_ramps.customer_provider_kycs.id;



CREATE TABLE macro_ramps.customer_virtual_accounts (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    external_account_id character varying(64) NOT NULL,
    source_currency character varying(8) NOT NULL,
    status character varying(16) NOT NULL,
    destination_currency character varying(16) NOT NULL,
    destination_chain character varying(32) NOT NULL,
    destination_address character varying(64) NOT NULL,
    fee_percent numeric(5,2),
    deposit_instructions jsonb NOT NULL,
    raw_payload jsonb NOT NULL,
    last_synced_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    customer_id bigint NOT NULL
);



CREATE SEQUENCE macro_ramps.customer_virtual_accounts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_virtual_accounts_id_seq OWNED BY macro_ramps.customer_virtual_accounts.id;



CREATE TABLE macro_ramps.customer_wallets (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    chain character varying(16) NOT NULL,
    address character varying(64) NOT NULL,
    registered_by_user_id bigint NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.customer_wallets_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.customer_wallets_id_seq OWNED BY macro_ramps.customer_wallets.id;



CREATE TABLE macro_ramps.customers (
    id bigint NOT NULL,
    type character varying(16) NOT NULL,
    status character varying(16) NOT NULL,
    tax_id_hash character varying(64),
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    CONSTRAINT customers_status_check CHECK (((status)::text = ANY ((ARRAY['draft'::character varying, 'active'::character varying, 'closed'::character varying])::text[]))),
    CONSTRAINT customers_type_check CHECK (((type)::text = ANY ((ARRAY['individual'::character varying, 'business'::character varying])::text[])))
);



CREATE SEQUENCE macro_ramps.customers_business_id_seq
    START WITH 1000000000000
    INCREMENT BY 1
    MINVALUE 1000000000000
    NO MAXVALUE
    CACHE 1;



CREATE TABLE macro_ramps.device_tokens (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    platform character varying(16) NOT NULL,
    token text NOT NULL,
    apns_environment character varying(16),
    locale character varying(16) NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.device_tokens_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.device_tokens_id_seq OWNED BY macro_ramps.device_tokens.id;



CREATE SEQUENCE macro_ramps.failed_jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



CREATE TABLE macro_ramps.features (
    id bigint NOT NULL,
    name character varying(255) NOT NULL,
    scope character varying(255) NOT NULL,
    value jsonb NOT NULL,
    created_at timestamp(6) without time zone,
    updated_at timestamp(6) without time zone
);



CREATE SEQUENCE macro_ramps.features_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.features_id_seq OWNED BY macro_ramps.features.id;



CREATE SEQUENCE macro_ramps.jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



CREATE TABLE macro_ramps.mfa_backup_codes (
    id bigint NOT NULL,
    subject_type character varying(16) NOT NULL,
    subject_id bigint NOT NULL,
    code_hash character varying(255) NOT NULL,
    used_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.mfa_backup_codes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.mfa_backup_codes_id_seq OWNED BY macro_ramps.mfa_backup_codes.id;



CREATE TABLE macro_ramps.mfa_credentials (
    id bigint NOT NULL,
    subject_type character varying(16) NOT NULL,
    subject_id bigint NOT NULL,
    secret text NOT NULL,
    confirmed_at timestamp(0) without time zone,
    last_used_counter bigint DEFAULT '0'::bigint NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    CONSTRAINT mfa_credentials_subject_type_check CHECK (((subject_type)::text = ANY ((ARRAY['users'::character varying, 'admins'::character varying])::text[])))
);



CREATE SEQUENCE macro_ramps.mfa_credentials_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.mfa_credentials_id_seq OWNED BY macro_ramps.mfa_credentials.id;



CREATE SEQUENCE macro_ramps.migrations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



CREATE TABLE macro_ramps.model_has_permissions (
    permission_id bigint NOT NULL,
    subject_type character varying(16) NOT NULL,
    subject_id bigint NOT NULL,
    CONSTRAINT model_has_permissions_subject_type_check CHECK (((subject_type)::text = ANY ((ARRAY['admins'::character varying, 'customer_members'::character varying])::text[])))
);



CREATE TABLE macro_ramps.model_has_roles (
    role_id bigint NOT NULL,
    subject_type character varying(16) NOT NULL,
    subject_id bigint NOT NULL,
    CONSTRAINT model_has_roles_subject_type_check CHECK (((subject_type)::text = ANY ((ARRAY['admins'::character varying, 'customer_members'::character varying])::text[])))
);



CREATE TABLE macro_ramps.offramp_destinations (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    pix_key_hash character varying(64) NOT NULL,
    document_hash character varying(64) NOT NULL,
    destination_ref character varying(64) DEFAULT ''::character varying NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    status character varying(16) DEFAULT 'ready'::character varying NOT NULL,
    name character varying(64) DEFAULT ''::character varying NOT NULL,
    registration_idempotency_key character varying(255) DEFAULT ''::character varying NOT NULL,
    registration_request_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    registration_attempt integer DEFAULT 1 NOT NULL,
    retry_deadline timestamp with time zone,
    failure_code character varying(64),
    provider_customer_id character varying(64) DEFAULT ''::character varying NOT NULL,
    CONSTRAINT offramp_destinations_status_check CHECK (((status)::text = ANY ((ARRAY['ready'::character varying, 'pending'::character varying, 'verifying'::character varying, 'rejected'::character varying, 'failed'::character varying])::text[])))
);



CREATE SEQUENCE macro_ramps.offramp_destinations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.offramp_destinations_id_seq OWNED BY macro_ramps.offramp_destinations.id;



CREATE TABLE macro_ramps.offramp_funding_authorizations (
    id bigint NOT NULL,
    external_id uuid NOT NULL,
    transaction_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    action_type character varying(32) NOT NULL,
    submission_mode character varying(32) NOT NULL,
    provider_state_ciphertext text NOT NULL,
    action_payload_ciphertext text NOT NULL,
    unsigned_action_fingerprint character(64) NOT NULL,
    expires_at timestamp(0) without time zone NOT NULL,
    action_revision integer DEFAULT 1 NOT NULL,
    state character varying(16) NOT NULL,
    failure_code character varying(64),
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    finalize_client_key_hash character varying(64),
    finalize_request_hash character varying(64),
    provider_finalize_key character varying(255),
    finalize_started_at timestamp(0) without time zone,
    finalize_lease_expires_at timestamp(0) without time zone,
    signed_payload_ciphertext text,
    signed_payload_retain_until timestamp(0) without time zone,
    recovery_attempts integer DEFAULT 0 NOT NULL,
    recovery_next_attempt_at timestamp(0) without time zone,
    payout_binding text,
    CONSTRAINT offramp_funding_authorizations_action_revision_check CHECK ((action_revision >= 1)),
    CONSTRAINT offramp_funding_authorizations_action_type_check CHECK (((action_type)::text = 'solana_transaction'::text)),
    CONSTRAINT offramp_funding_authorizations_failure_code_check CHECK ((((state)::text = 'failed'::text) = (failure_code IS NOT NULL))),
    CONSTRAINT offramp_funding_authorizations_fingerprint_check CHECK ((unsigned_action_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT offramp_funding_authorizations_state_check CHECK (((state)::text = ANY ((ARRAY['prepared'::character varying, 'finalizing'::character varying, 'finalized'::character varying, 'failed'::character varying, 'unknown'::character varying, 'needs_operator'::character varying])::text[]))),
    CONSTRAINT offramp_funding_authorizations_submission_mode_check CHECK (((submission_mode)::text = 'backend_finalize'::text))
);



CREATE SEQUENCE macro_ramps.offramp_funding_authorizations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.offramp_funding_authorizations_id_seq OWNED BY macro_ramps.offramp_funding_authorizations.id;



CREATE TABLE macro_ramps.otp_codes (
    id bigint NOT NULL,
    subject_type character varying(16) NOT NULL,
    subject_id bigint NOT NULL,
    purpose character varying(32) NOT NULL,
    code character varying(6) NOT NULL,
    expires_at timestamp(0) without time zone NOT NULL,
    used_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    CONSTRAINT otp_codes_subject_type_check CHECK (((subject_type)::text = ANY ((ARRAY['users'::character varying, 'admins'::character varying])::text[])))
);



CREATE SEQUENCE macro_ramps.otp_codes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.otp_codes_id_seq OWNED BY macro_ramps.otp_codes.id;



CREATE TABLE macro_ramps.permissions (
    id bigint NOT NULL,
    name character varying(255) NOT NULL,
    guard character varying(255) DEFAULT 'admin'::character varying NOT NULL,
    description character varying(255),
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.permissions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.permissions_id_seq OWNED BY macro_ramps.permissions.id;



CREATE TABLE macro_ramps.provider_calls (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    operation character varying(48) NOT NULL,
    outcome character varying(16) NOT NULL,
    error_code character varying(64),
    latency_ms integer,
    submission_id character varying(64),
    request_id character varying(64),
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    customer_id bigint NOT NULL
);



CREATE SEQUENCE macro_ramps.provider_calls_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.provider_calls_id_seq OWNED BY macro_ramps.provider_calls.id;



CREATE TABLE macro_ramps.provider_detached_customers (
    id bigint NOT NULL,
    customer_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    external_customer_id character varying(128) NOT NULL,
    detached_at timestamp(0) without time zone NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.provider_detached_customers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.provider_detached_customers_id_seq OWNED BY macro_ramps.provider_detached_customers.id;



CREATE TABLE macro_ramps.provider_kyc_submission_intents (
    id bigint NOT NULL,
    external_id uuid NOT NULL,
    customer_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    submission_id character varying(64) NOT NULL,
    state character varying(16) NOT NULL,
    provider_idempotency_key character varying(128) NOT NULL,
    material_ciphertext text NOT NULL,
    body_sha256 character varying(64),
    provider_customer_id character varying(64),
    attempt_count integer DEFAULT 0 NOT NULL,
    last_attempt_at timestamp(0) without time zone,
    retry_deadline timestamp(0) without time zone NOT NULL,
    recovery_next_attempt_at timestamp(0) without time zone,
    recovery_lease_expires_at timestamp(0) without time zone,
    recovery_attempts integer DEFAULT 0 NOT NULL,
    failure_code character varying(64),
    material_retain_until timestamp(0) without time zone,
    material_cleared_at timestamp(0) without time zone,
    resolved_at timestamp(0) without time zone,
    resolution_outcome character varying(32),
    resolution_reason character varying(255),
    revision integer DEFAULT 1 NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    CONSTRAINT provider_kyc_submission_intents_body_check CHECK ((((state)::text = ANY ((ARRAY['preparing'::character varying, 'failed'::character varying, 'manual_review'::character varying, 'resolved'::character varying])::text[])) OR (body_sha256 IS NOT NULL))),
    CONSTRAINT provider_kyc_submission_intents_outcome_check CHECK (((resolution_outcome IS NULL) OR ((resolution_outcome)::text = ANY ((ARRAY['reconciled'::character varying, 'rejected'::character varying, 'cancelled'::character varying])::text[])))),
    CONSTRAINT provider_kyc_submission_intents_recovery_check CHECK (((recovery_lease_expires_at IS NULL) OR (recovery_next_attempt_at IS NULL))),
    CONSTRAINT provider_kyc_submission_intents_state_check CHECK (((state)::text = ANY ((ARRAY['preparing'::character varying, 'prepared'::character varying, 'submitting'::character varying, 'unknown'::character varying, 'completed'::character varying, 'failed'::character varying, 'manual_review'::character varying, 'resolved'::character varying])::text[])))
);



CREATE SEQUENCE macro_ramps.provider_kyc_submission_intents_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.provider_kyc_submission_intents_id_seq OWNED BY macro_ramps.provider_kyc_submission_intents.id;



CREATE TABLE macro_ramps.ramp_estimates (
    id bigint NOT NULL,
    external_id uuid NOT NULL,
    user_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    rail character varying(24) NOT NULL,
    provider_quote_id character varying(64),
    binding boolean DEFAULT false NOT NULL,
    expires_at timestamp(0) without time zone,
    consumed_at timestamp(0) without time zone,
    fee_percent numeric(5,2) NOT NULL,
    raw_request jsonb NOT NULL,
    raw_response jsonb NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    customer_id bigint NOT NULL
);



CREATE SEQUENCE macro_ramps.ramp_estimates_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.ramp_estimates_id_seq OWNED BY macro_ramps.ramp_estimates.id;



CREATE TABLE macro_ramps.request_logs (
    id bigint NOT NULL,
    request_id uuid NOT NULL,
    audience character varying(16) NOT NULL,
    method character varying(8) NOT NULL,
    path character varying(512) NOT NULL,
    query jsonb NOT NULL,
    status smallint NOT NULL,
    duration_ms integer NOT NULL,
    client_ip character varying(45),
    user_id bigint,
    admin_id bigint,
    request_headers jsonb NOT NULL,
    request_body text,
    response_headers jsonb NOT NULL,
    response_body text,
    error text,
    created_at timestamp(0) without time zone NOT NULL,
    customer_id bigint
);



CREATE SEQUENCE macro_ramps.request_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.request_logs_id_seq OWNED BY macro_ramps.request_logs.id;



CREATE TABLE macro_ramps.role_has_permissions (
    role_id bigint NOT NULL,
    permission_id bigint NOT NULL
);



CREATE TABLE macro_ramps.roles (
    id bigint NOT NULL,
    name character varying(255) NOT NULL,
    guard character varying(255) DEFAULT 'admin'::character varying NOT NULL,
    description character varying(255),
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.roles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.roles_id_seq OWNED BY macro_ramps.roles.id;



CREATE TABLE macro_ramps.settings (
    id bigint NOT NULL,
    "group" character varying(255) NOT NULL,
    key character varying(255) NOT NULL,
    value text NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.settings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.settings_id_seq OWNED BY macro_ramps.settings.id;



CREATE TABLE macro_ramps.transactions (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    type character varying(16) NOT NULL,
    external_id uuid NOT NULL,
    gateway_order_id character varying(64) NOT NULL,
    gateway_quote_id character varying(64),
    status character varying(32) NOT NULL,
    amount_fiat numeric(20,2) NOT NULL,
    amount_token numeric(38,18) NOT NULL,
    target_token character varying(16) NOT NULL,
    destination_wallet character varying(64) NOT NULL,
    pix_payment_code text,
    expires_at timestamp(0) without time zone,
    completed_at timestamp(0) without time zone,
    raw_payload jsonb NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    pix_key text,
    pix_key_type character varying(16),
    recipient_name character varying(255),
    recipient_document character varying(18),
    payment_reference character varying(64),
    deposit_tx_signature character varying(96),
    failure_reason text,
    rail character varying(24),
    fee_percent numeric(5,2),
    fee_amount numeric(38,18),
    fee_currency character varying(16),
    estimate_id bigint,
    provider_destination_ref character varying(64),
    from_address character varying(64),
    idempotency_key character varying(64),
    deposit_instructions jsonb,
    virtual_account_id bigint,
    deposit_id character varying(64),
    sender_name character varying(255),
    sender_reference character varying(255),
    exchange_fee_amount numeric(38,18),
    deposit_mode character varying(16),
    delivery_order_id character varying(64),
    chain character varying(24),
    exchange_rate numeric(38,18),
    delivery_attempts integer DEFAULT 0 NOT NULL,
    fiat_currency character varying(8) DEFAULT 'brl'::character varying NOT NULL,
    payment_rail character varying(16),
    bank_account_id bigint,
    liquidation_address_id bigint,
    customer_id bigint NOT NULL,
    bank_beneficiary_id bigint,
    bank_rail character varying(24),
    bank_account_type character varying(16),
    bank_account_last4 character varying(4),
    bank_routing_last4 character varying(4),
    attention_code character varying(64),
    attention_at timestamp(0) without time zone,
    support_ticket_ref character varying(64),
    support_ticket_note text,
    support_ticket_at timestamp(0) without time zone,
    funding_mode character varying(16),
    CONSTRAINT transactions_bank_rail_check CHECK (((bank_rail IS NULL) OR ((bank_rail)::text = ANY ((ARRAY['ach_payout'::character varying, 'wire_payout'::character varying])::text[])))),
    CONSTRAINT transactions_funding_mode_check CHECK (((funding_mode IS NULL) OR ((funding_mode)::text = 'prepared'::text)))
);



CREATE SEQUENCE macro_ramps.transactions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.transactions_id_seq OWNED BY macro_ramps.transactions.id;



CREATE TABLE macro_ramps.user_bank_accounts (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    label text NOT NULL,
    account_owner_name text NOT NULL,
    bank_name text NOT NULL,
    routing_number text NOT NULL,
    account_number text NOT NULL,
    routing_last4 character varying(4) NOT NULL,
    account_last4 character varying(4) NOT NULL,
    account_type character varying(16) NOT NULL,
    address text NOT NULL,
    account_hash character varying(64) NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.user_bank_accounts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.user_bank_accounts_id_seq OWNED BY macro_ramps.user_bank_accounts.id;



CREATE TABLE macro_ramps.user_liquidation_addresses (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    user_bank_account_id bigint NOT NULL,
    rail character varying(24) NOT NULL,
    chain character varying(32) NOT NULL,
    currency character varying(16) NOT NULL,
    external_address_id character varying(64) NOT NULL,
    address character varying(128) NOT NULL,
    return_address character varying(64) NOT NULL,
    reference character varying(140),
    fee_percent numeric(5,2),
    status character varying(16) NOT NULL,
    raw_payload jsonb NOT NULL,
    last_synced_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    customer_id bigint NOT NULL
);



CREATE SEQUENCE macro_ramps.user_liquidation_addresses_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.user_liquidation_addresses_id_seq OWNED BY macro_ramps.user_liquidation_addresses.id;



CREATE TABLE macro_ramps.user_pix_keys (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    label text NOT NULL,
    pix_key text NOT NULL,
    pix_key_hash character varying(64) NOT NULL,
    pix_key_type character varying(16) NOT NULL,
    recipient_name text NOT NULL,
    recipient_document text,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.user_pix_keys_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.user_pix_keys_id_seq OWNED BY macro_ramps.user_pix_keys.id;



CREATE TABLE macro_ramps.users (
    id bigint NOT NULL,
    handle character varying(20) NOT NULL,
    email character varying(255) NOT NULL,
    password character varying(255) NOT NULL,
    email_verified_at timestamp(0) without time zone,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone,
    deleted_at timestamp(0) without time zone,
    sessions_revoked_at timestamp(0) without time zone,
    suspended_at timestamp(0) without time zone,
    suspension_reason character varying(500)
);



CREATE SEQUENCE macro_ramps.users_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.users_id_seq OWNED BY macro_ramps.users.id;



CREATE TABLE macro_ramps.webhook_events (
    id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    event_id character varying(128) NOT NULL,
    event_type character varying(64) NOT NULL,
    external_ref character varying(64),
    transaction_id bigint,
    received_at timestamp(0) without time zone NOT NULL,
    processed_at timestamp(0) without time zone,
    processing_error text,
    raw_payload jsonb NOT NULL,
    created_at timestamp(0) without time zone,
    updated_at timestamp(0) without time zone
);



CREATE SEQUENCE macro_ramps.webhook_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE macro_ramps.webhook_events_id_seq OWNED BY macro_ramps.webhook_events.id;



ALTER TABLE ONLY macro_ramps.admin_audit_logs ALTER COLUMN id SET DEFAULT nextval('macro_ramps.admin_audit_logs_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.admins ALTER COLUMN id SET DEFAULT nextval('macro_ramps.admins_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.bank_account_destinations ALTER COLUMN id SET DEFAULT nextval('macro_ramps.bank_account_destinations_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_associated_persons ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_associated_persons_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiaries ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_bank_beneficiaries_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiary_provider_accounts ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_bank_beneficiary_provider_accounts_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_business_profiles ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_business_profiles_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_individual_profiles ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_individual_profiles_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_members ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_members_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_provider_integrations ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_provider_integrations_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_provider_kycs ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_provider_kycs_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_virtual_accounts ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_virtual_accounts_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.customer_wallets ALTER COLUMN id SET DEFAULT nextval('macro_ramps.customer_wallets_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.device_tokens ALTER COLUMN id SET DEFAULT nextval('macro_ramps.device_tokens_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.features ALTER COLUMN id SET DEFAULT nextval('macro_ramps.features_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.mfa_backup_codes ALTER COLUMN id SET DEFAULT nextval('macro_ramps.mfa_backup_codes_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.mfa_credentials ALTER COLUMN id SET DEFAULT nextval('macro_ramps.mfa_credentials_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.offramp_destinations ALTER COLUMN id SET DEFAULT nextval('macro_ramps.offramp_destinations_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.offramp_funding_authorizations ALTER COLUMN id SET DEFAULT nextval('macro_ramps.offramp_funding_authorizations_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.otp_codes ALTER COLUMN id SET DEFAULT nextval('macro_ramps.otp_codes_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.permissions ALTER COLUMN id SET DEFAULT nextval('macro_ramps.permissions_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.provider_calls ALTER COLUMN id SET DEFAULT nextval('macro_ramps.provider_calls_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.provider_detached_customers ALTER COLUMN id SET DEFAULT nextval('macro_ramps.provider_detached_customers_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.provider_kyc_submission_intents ALTER COLUMN id SET DEFAULT nextval('macro_ramps.provider_kyc_submission_intents_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.ramp_estimates ALTER COLUMN id SET DEFAULT nextval('macro_ramps.ramp_estimates_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.request_logs ALTER COLUMN id SET DEFAULT nextval('macro_ramps.request_logs_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.roles ALTER COLUMN id SET DEFAULT nextval('macro_ramps.roles_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.settings ALTER COLUMN id SET DEFAULT nextval('macro_ramps.settings_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.transactions ALTER COLUMN id SET DEFAULT nextval('macro_ramps.transactions_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.user_bank_accounts ALTER COLUMN id SET DEFAULT nextval('macro_ramps.user_bank_accounts_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.user_liquidation_addresses ALTER COLUMN id SET DEFAULT nextval('macro_ramps.user_liquidation_addresses_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.user_pix_keys ALTER COLUMN id SET DEFAULT nextval('macro_ramps.user_pix_keys_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.users ALTER COLUMN id SET DEFAULT nextval('macro_ramps.users_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.webhook_events ALTER COLUMN id SET DEFAULT nextval('macro_ramps.webhook_events_id_seq'::regclass);



ALTER TABLE ONLY macro_ramps.admin_audit_logs
    ADD CONSTRAINT admin_audit_logs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.admins
    ADD CONSTRAINT admins_email_unique UNIQUE (email);



ALTER TABLE ONLY macro_ramps.admins
    ADD CONSTRAINT admins_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.bank_account_destinations
    ADD CONSTRAINT bank_account_destinations_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.bank_account_destinations
    ADD CONSTRAINT bank_account_destinations_unique UNIQUE (customer_id, user_bank_account_id, provider);



ALTER TABLE ONLY macro_ramps.customer_associated_persons
    ADD CONSTRAINT customer_associated_persons_customer_id_local_ref_unique UNIQUE (customer_id, local_ref);



ALTER TABLE ONLY macro_ramps.customer_associated_persons
    ADD CONSTRAINT customer_associated_persons_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiaries
    ADD CONSTRAINT customer_bank_beneficiaries_external_id_unique UNIQUE (external_id);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiaries
    ADD CONSTRAINT customer_bank_beneficiaries_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiary_provider_accounts
    ADD CONSTRAINT customer_bank_beneficiary_provider_accounts_external_id_unique UNIQUE (external_id);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiary_provider_accounts
    ADD CONSTRAINT customer_bank_beneficiary_provider_accounts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_business_profiles
    ADD CONSTRAINT customer_business_profiles_customer_id_unique UNIQUE (customer_id);



ALTER TABLE ONLY macro_ramps.customer_business_profiles
    ADD CONSTRAINT customer_business_profiles_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_individual_profiles
    ADD CONSTRAINT customer_individual_profiles_customer_id_unique UNIQUE (customer_id);



ALTER TABLE ONLY macro_ramps.customer_individual_profiles
    ADD CONSTRAINT customer_individual_profiles_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_members
    ADD CONSTRAINT customer_members_customer_id_user_id_unique UNIQUE (customer_id, user_id);



ALTER TABLE ONLY macro_ramps.customer_members
    ADD CONSTRAINT customer_members_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_provider_integrations
    ADD CONSTRAINT customer_provider_integrations_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_provider_kycs
    ADD CONSTRAINT customer_provider_kycs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_virtual_accounts
    ADD CONSTRAINT customer_virtual_accounts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customer_wallets
    ADD CONSTRAINT customer_wallets_chain_address_unique UNIQUE (chain, address);



ALTER TABLE ONLY macro_ramps.customer_wallets
    ADD CONSTRAINT customer_wallets_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.customers
    ADD CONSTRAINT customers_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.device_tokens
    ADD CONSTRAINT device_tokens_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.device_tokens
    ADD CONSTRAINT device_tokens_token_unique UNIQUE (token);



ALTER TABLE ONLY macro_ramps.features
    ADD CONSTRAINT features_name_scope_key UNIQUE (name, scope);



ALTER TABLE ONLY macro_ramps.features
    ADD CONSTRAINT features_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.mfa_backup_codes
    ADD CONSTRAINT mfa_backup_codes_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.mfa_credentials
    ADD CONSTRAINT mfa_credentials_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.mfa_credentials
    ADD CONSTRAINT mfa_credentials_subject_type_subject_id_unique UNIQUE (subject_type, subject_id);



ALTER TABLE ONLY macro_ramps.model_has_permissions
    ADD CONSTRAINT model_has_permissions_pkey PRIMARY KEY (permission_id, subject_type, subject_id);



ALTER TABLE ONLY macro_ramps.model_has_roles
    ADD CONSTRAINT model_has_roles_pkey PRIMARY KEY (role_id, subject_type, subject_id);



ALTER TABLE ONLY macro_ramps.offramp_destinations
    ADD CONSTRAINT offramp_destinations_identity_unique UNIQUE (customer_id, provider, pix_key_hash, document_hash);



ALTER TABLE ONLY macro_ramps.offramp_destinations
    ADD CONSTRAINT offramp_destinations_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.offramp_funding_authorizations
    ADD CONSTRAINT offramp_funding_authorizations_external_id_unique UNIQUE (external_id);



ALTER TABLE ONLY macro_ramps.offramp_funding_authorizations
    ADD CONSTRAINT offramp_funding_authorizations_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.offramp_funding_authorizations
    ADD CONSTRAINT offramp_funding_authorizations_transaction_id_unique UNIQUE (transaction_id);



ALTER TABLE ONLY macro_ramps.otp_codes
    ADD CONSTRAINT otp_codes_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.permissions
    ADD CONSTRAINT permissions_name_guard_unique UNIQUE (name, guard);



ALTER TABLE ONLY macro_ramps.permissions
    ADD CONSTRAINT permissions_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.provider_calls
    ADD CONSTRAINT provider_calls_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.provider_detached_customers
    ADD CONSTRAINT provider_detached_customers_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.provider_detached_customers
    ADD CONSTRAINT provider_detached_customers_provider_external_customer_id_uniqu UNIQUE (provider, external_customer_id);



ALTER TABLE ONLY macro_ramps.provider_kyc_submission_intents
    ADD CONSTRAINT provider_kyc_submission_intents_external_id_unique UNIQUE (external_id);



ALTER TABLE ONLY macro_ramps.provider_kyc_submission_intents
    ADD CONSTRAINT provider_kyc_submission_intents_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.provider_kyc_submission_intents
    ADD CONSTRAINT provider_kyc_submission_intents_provider_idempotency_key_unique UNIQUE (provider_idempotency_key);



ALTER TABLE ONLY macro_ramps.ramp_estimates
    ADD CONSTRAINT ramp_estimates_external_id_unique UNIQUE (external_id);



ALTER TABLE ONLY macro_ramps.ramp_estimates
    ADD CONSTRAINT ramp_estimates_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.request_logs
    ADD CONSTRAINT request_logs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.role_has_permissions
    ADD CONSTRAINT role_has_permissions_pkey PRIMARY KEY (role_id, permission_id);



ALTER TABLE ONLY macro_ramps.roles
    ADD CONSTRAINT roles_name_guard_unique UNIQUE (name, guard);



ALTER TABLE ONLY macro_ramps.roles
    ADD CONSTRAINT roles_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.settings
    ADD CONSTRAINT settings_group_key_unique UNIQUE ("group", key);



ALTER TABLE ONLY macro_ramps.settings
    ADD CONSTRAINT settings_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_customer_id_provider_estimate_id_unique UNIQUE (customer_id, provider, estimate_id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_external_id_unique UNIQUE (external_id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_provider_idempotency_key_unique UNIQUE (provider, idempotency_key);



ALTER TABLE ONLY macro_ramps.user_bank_accounts
    ADD CONSTRAINT user_bank_accounts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.user_liquidation_addresses
    ADD CONSTRAINT user_liquidation_addresses_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.user_pix_keys
    ADD CONSTRAINT user_pix_keys_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.user_pix_keys
    ADD CONSTRAINT user_pix_keys_user_id_pix_key_hash_unique UNIQUE (user_id, pix_key_hash);







ALTER TABLE ONLY macro_ramps.users
    ADD CONSTRAINT users_handle_unique UNIQUE (handle);



ALTER TABLE ONLY macro_ramps.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.webhook_events
    ADD CONSTRAINT webhook_events_pkey PRIMARY KEY (id);



ALTER TABLE ONLY macro_ramps.webhook_events
    ADD CONSTRAINT webhook_events_provider_event_id_unique UNIQUE (provider, event_id);



CREATE INDEX admin_audit_logs_action_index ON macro_ramps.admin_audit_logs USING btree (action);



CREATE INDEX admin_audit_logs_admin_created_index ON macro_ramps.admin_audit_logs USING btree (admin_id, created_at DESC);



CREATE INDEX admin_audit_logs_subject_created_index ON macro_ramps.admin_audit_logs USING btree (subject_type, subject_id, created_at DESC);



CREATE INDEX bank_account_destinations_user_bank_account_id_index ON macro_ramps.bank_account_destinations USING btree (user_bank_account_id);



CREATE INDEX customer_bank_beneficiaries_customer_id_index ON macro_ramps.customer_bank_beneficiaries USING btree (customer_id);



CREATE UNIQUE INDEX customer_bank_beneficiaries_fingerprint_unique ON macro_ramps.customer_bank_beneficiaries USING btree (customer_id, fingerprint) WHERE (deleted_at IS NULL);



CREATE INDEX customer_bank_beneficiary_provider_accounts_beneficiary_id_inde ON macro_ramps.customer_bank_beneficiary_provider_accounts USING btree (beneficiary_id);



CREATE UNIQUE INDEX customer_bank_beneficiary_provider_accounts_live_unique ON macro_ramps.customer_bank_beneficiary_provider_accounts USING btree (beneficiary_id, provider, rail) WHERE ((status)::text <> 'superseded'::text);



CREATE INDEX customer_individual_profiles_phone_hash_unique ON macro_ramps.customer_individual_profiles USING btree (phone_hash) WHERE (phone_hash IS NOT NULL);



CREATE INDEX customer_individual_profiles_tax_id_hash_unique ON macro_ramps.customer_individual_profiles USING btree (tax_id_hash) WHERE (tax_id_hash IS NOT NULL);



CREATE INDEX customer_members_user_id_index ON macro_ramps.customer_members USING btree (user_id);



CREATE INDEX customer_provider_integrations_customer_id_index ON macro_ramps.customer_provider_integrations USING btree (customer_id);



CREATE UNIQUE INDEX customer_provider_integrations_customer_provider_unique ON macro_ramps.customer_provider_integrations USING btree (customer_id, provider) WHERE (deleted_at IS NULL);



CREATE UNIQUE INDEX customer_provider_integrations_external_customer_unique ON macro_ramps.customer_provider_integrations USING btree (provider, external_customer_id) WHERE (deleted_at IS NULL);



CREATE INDEX customer_provider_integrations_provider_index ON macro_ramps.customer_provider_integrations USING btree (provider);



CREATE INDEX customer_provider_integrations_status_index ON macro_ramps.customer_provider_integrations USING btree (status);



CREATE INDEX customer_provider_kycs_customer_id_index ON macro_ramps.customer_provider_kycs USING btree (customer_id);



CREATE UNIQUE INDEX customer_provider_kycs_customer_provider_unique ON macro_ramps.customer_provider_kycs USING btree (customer_id, provider) WHERE (deleted_at IS NULL);



CREATE INDEX customer_provider_kycs_kyc_status_index ON macro_ramps.customer_provider_kycs USING btree (kyc_status);



CREATE INDEX customer_provider_kycs_provider_index ON macro_ramps.customer_provider_kycs USING btree (provider);



CREATE INDEX customer_virtual_accounts_customer_id_index ON macro_ramps.customer_virtual_accounts USING btree (customer_id);



CREATE INDEX customer_virtual_accounts_provider_index ON macro_ramps.customer_virtual_accounts USING btree (provider);



CREATE INDEX customer_virtual_accounts_user_id_index ON macro_ramps.customer_virtual_accounts USING btree (user_id);



CREATE INDEX customer_wallets_customer_id_index ON macro_ramps.customer_wallets USING btree (customer_id);



CREATE UNIQUE INDEX customers_business_tax_id_hash_unique ON macro_ramps.customers USING btree (tax_id_hash) WHERE (((type)::text = 'business'::text) AND ((status)::text <> 'draft'::text));



CREATE INDEX device_tokens_user_id_index ON macro_ramps.device_tokens USING btree (user_id);



CREATE INDEX mfa_backup_codes_subject_type_subject_id_used_at_index ON macro_ramps.mfa_backup_codes USING btree (subject_type, subject_id, used_at);



CREATE INDEX model_has_permissions_subject_type_subject_id_index ON macro_ramps.model_has_permissions USING btree (subject_type, subject_id);



CREATE INDEX model_has_roles_subject_type_subject_id_index ON macro_ramps.model_has_roles USING btree (subject_type, subject_id);



CREATE UNIQUE INDEX offramp_funding_authorizations_provider_finalize_key_unique ON macro_ramps.offramp_funding_authorizations USING btree (provider_finalize_key) WHERE (provider_finalize_key IS NOT NULL);



CREATE INDEX offramp_funding_authorizations_state_recovery_next_index ON macro_ramps.offramp_funding_authorizations USING btree (state, recovery_next_attempt_at);



CREATE INDEX otp_codes_subject_type_subject_id_purpose_used_at_index ON macro_ramps.otp_codes USING btree (subject_type, subject_id, purpose, used_at);



CREATE INDEX provider_calls_customer_id_created_at_index ON macro_ramps.provider_calls USING btree (customer_id, created_at DESC);



CREATE INDEX provider_calls_user_created_index ON macro_ramps.provider_calls USING btree (user_id, created_at DESC);



CREATE UNIQUE INDEX provider_kyc_submission_intents_active_unique ON macro_ramps.provider_kyc_submission_intents USING btree (customer_id, provider) WHERE ((state)::text = ANY ((ARRAY['preparing'::character varying, 'prepared'::character varying, 'submitting'::character varying, 'unknown'::character varying, 'manual_review'::character varying])::text[]));



CREATE INDEX provider_kyc_submission_intents_state_recovery_lease_expires_at ON macro_ramps.provider_kyc_submission_intents USING btree (state, recovery_lease_expires_at);



CREATE INDEX provider_kyc_submission_intents_state_recovery_next_attempt_at_ ON macro_ramps.provider_kyc_submission_intents USING btree (state, recovery_next_attempt_at);



CREATE INDEX provider_kyc_submission_intents_submission_index ON macro_ramps.provider_kyc_submission_intents USING btree (customer_id, provider, submission_id);



CREATE INDEX ramp_estimates_customer_id_index ON macro_ramps.ramp_estimates USING btree (customer_id);



CREATE INDEX ramp_estimates_expires_at_index ON macro_ramps.ramp_estimates USING btree (expires_at);



CREATE INDEX ramp_estimates_provider_index ON macro_ramps.ramp_estimates USING btree (provider);



CREATE INDEX ramp_estimates_user_id_index ON macro_ramps.ramp_estimates USING btree (user_id);



CREATE INDEX request_logs_admin_id_idx ON macro_ramps.request_logs USING btree (admin_id) WHERE (admin_id IS NOT NULL);



CREATE INDEX request_logs_created_at_desc ON macro_ramps.request_logs USING btree (created_at DESC);



CREATE INDEX request_logs_customer_id_idx ON macro_ramps.request_logs USING btree (customer_id) WHERE (customer_id IS NOT NULL);



CREATE INDEX request_logs_path_index ON macro_ramps.request_logs USING btree (path);



CREATE INDEX request_logs_request_id_index ON macro_ramps.request_logs USING btree (request_id);



CREATE INDEX request_logs_status_index ON macro_ramps.request_logs USING btree (status);



CREATE INDEX request_logs_user_id_idx ON macro_ramps.request_logs USING btree (user_id) WHERE (user_id IS NOT NULL);



CREATE INDEX transactions_attention_at_index ON macro_ramps.transactions USING btree (attention_at) WHERE (attention_code IS NOT NULL);



CREATE INDEX transactions_bank_beneficiary_id_index ON macro_ramps.transactions USING btree (bank_beneficiary_id);



CREATE INDEX transactions_created_at_type_index ON macro_ramps.transactions USING btree (created_at, type);



CREATE INDEX transactions_customer_id_index ON macro_ramps.transactions USING btree (customer_id);



CREATE UNIQUE INDEX transactions_customer_provider_quote_partial_unique ON macro_ramps.transactions USING btree (customer_id, provider, gateway_quote_id) WHERE (gateway_quote_id IS NOT NULL);



CREATE INDEX transactions_liquidation_address_id_index ON macro_ramps.transactions USING btree (liquidation_address_id);



CREATE UNIQUE INDEX transactions_offramp_provider_deposit_tx_signature_unique ON macro_ramps.transactions USING btree (provider, deposit_tx_signature) WHERE ((deposit_tx_signature IS NOT NULL) AND ((deposit_tx_signature)::text <> ''::text) AND ((type)::text = 'offramp'::text));



CREATE UNIQUE INDEX transactions_provider_gateway_order_id_unique ON macro_ramps.transactions USING btree (provider, gateway_order_id) WHERE (((gateway_order_id)::text <> ''::text) AND (deleted_at IS NULL));



CREATE INDEX transactions_provider_index ON macro_ramps.transactions USING btree (provider);



CREATE INDEX transactions_rail_index ON macro_ramps.transactions USING btree (rail);



CREATE INDEX transactions_status_created_at_index ON macro_ramps.transactions USING btree (status, created_at);



CREATE INDEX transactions_status_index ON macro_ramps.transactions USING btree (status);



CREATE INDEX transactions_status_updated_at_index ON macro_ramps.transactions USING btree (status, updated_at);



CREATE INDEX transactions_type_index ON macro_ramps.transactions USING btree (type);



CREATE INDEX transactions_type_provider_status_updated_at_index ON macro_ramps.transactions USING btree (type, provider, status, updated_at);



CREATE INDEX transactions_user_id_index ON macro_ramps.transactions USING btree (user_id);



CREATE INDEX transactions_virtual_account_id_index ON macro_ramps.transactions USING btree (virtual_account_id);



CREATE UNIQUE INDEX uniq_cva_customer_provider_currency ON macro_ramps.customer_virtual_accounts USING btree (customer_id, provider, source_currency) WHERE ((deleted_at IS NULL) AND ((status)::text = ANY ((ARRAY['active'::character varying, 'provisioning'::character varying])::text[])));



CREATE UNIQUE INDEX uniq_cva_provider_external ON macro_ramps.customer_virtual_accounts USING btree (provider, external_account_id) WHERE (deleted_at IS NULL);



CREATE UNIQUE INDEX uniq_tx_provider_deposit ON macro_ramps.transactions USING btree (provider, deposit_id) WHERE ((deposit_id IS NOT NULL) AND (deleted_at IS NULL));



CREATE UNIQUE INDEX uniq_uba_user_account ON macro_ramps.user_bank_accounts USING btree (user_id, account_hash) WHERE (deleted_at IS NULL);



CREATE UNIQUE INDEX uniq_ula_provider_external ON macro_ramps.user_liquidation_addresses USING btree (provider, external_address_id) WHERE (deleted_at IS NULL);



CREATE UNIQUE INDEX uniq_ula_route ON macro_ramps.user_liquidation_addresses USING btree (customer_id, user_bank_account_id, rail, chain, currency) WHERE ((deleted_at IS NULL) AND ((status)::text = ANY ((ARRAY['active'::character varying, 'provisioning'::character varying])::text[])));



CREATE INDEX user_bank_accounts_user_id_index ON macro_ramps.user_bank_accounts USING btree (user_id);



CREATE INDEX user_liquidation_addresses_customer_id_index ON macro_ramps.user_liquidation_addresses USING btree (customer_id);



CREATE INDEX user_liquidation_addresses_user_id_index ON macro_ramps.user_liquidation_addresses USING btree (user_id);



CREATE INDEX webhook_events_provider_external_ref_index ON macro_ramps.webhook_events USING btree (provider, external_ref);



CREATE INDEX webhook_events_transaction_id_index ON macro_ramps.webhook_events USING btree (transaction_id);



ALTER TABLE ONLY macro_ramps.admin_audit_logs
    ADD CONSTRAINT admin_audit_logs_admin_id_foreign FOREIGN KEY (admin_id) REFERENCES macro_ramps.admins(id) ON DELETE SET NULL;



ALTER TABLE ONLY macro_ramps.bank_account_destinations
    ADD CONSTRAINT bank_account_destinations_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.customer_associated_persons
    ADD CONSTRAINT customer_associated_persons_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.customer_associated_persons
    ADD CONSTRAINT customer_associated_persons_individual_customer_id_foreign FOREIGN KEY (individual_customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiaries
    ADD CONSTRAINT customer_bank_beneficiaries_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.customer_bank_beneficiary_provider_accounts
    ADD CONSTRAINT customer_bank_beneficiary_provider_accounts_beneficiary_id_fore FOREIGN KEY (beneficiary_id) REFERENCES macro_ramps.customer_bank_beneficiaries(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.customer_business_profiles
    ADD CONSTRAINT customer_business_profiles_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.customer_individual_profiles
    ADD CONSTRAINT customer_individual_profiles_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.customer_members
    ADD CONSTRAINT customer_members_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.customer_members
    ADD CONSTRAINT customer_members_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id);



ALTER TABLE ONLY macro_ramps.customer_provider_integrations
    ADD CONSTRAINT customer_provider_integrations_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.customer_provider_kycs
    ADD CONSTRAINT customer_provider_kycs_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.customer_virtual_accounts
    ADD CONSTRAINT customer_virtual_accounts_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.customer_virtual_accounts
    ADD CONSTRAINT customer_virtual_accounts_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id);



ALTER TABLE ONLY macro_ramps.customer_wallets
    ADD CONSTRAINT customer_wallets_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.customer_wallets
    ADD CONSTRAINT customer_wallets_registered_by_user_id_foreign FOREIGN KEY (registered_by_user_id) REFERENCES macro_ramps.users(id);



ALTER TABLE ONLY macro_ramps.device_tokens
    ADD CONSTRAINT device_tokens_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.model_has_permissions
    ADD CONSTRAINT model_has_permissions_permission_id_foreign FOREIGN KEY (permission_id) REFERENCES macro_ramps.permissions(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.model_has_roles
    ADD CONSTRAINT model_has_roles_role_id_foreign FOREIGN KEY (role_id) REFERENCES macro_ramps.roles(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.offramp_destinations
    ADD CONSTRAINT offramp_destinations_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.offramp_funding_authorizations
    ADD CONSTRAINT offramp_funding_authorizations_transaction_id_foreign FOREIGN KEY (transaction_id) REFERENCES macro_ramps.transactions(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.provider_calls
    ADD CONSTRAINT provider_calls_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id);



ALTER TABLE ONLY macro_ramps.provider_detached_customers
    ADD CONSTRAINT provider_detached_customers_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.provider_kyc_submission_intents
    ADD CONSTRAINT provider_kyc_submission_intents_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.ramp_estimates
    ADD CONSTRAINT ramp_estimates_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.ramp_estimates
    ADD CONSTRAINT ramp_estimates_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id);



ALTER TABLE ONLY macro_ramps.role_has_permissions
    ADD CONSTRAINT role_has_permissions_permission_id_foreign FOREIGN KEY (permission_id) REFERENCES macro_ramps.permissions(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.role_has_permissions
    ADD CONSTRAINT role_has_permissions_role_id_foreign FOREIGN KEY (role_id) REFERENCES macro_ramps.roles(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_bank_beneficiary_id_foreign FOREIGN KEY (bank_beneficiary_id) REFERENCES macro_ramps.customer_bank_beneficiaries(id) ON DELETE SET NULL;



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_estimate_id_foreign FOREIGN KEY (estimate_id) REFERENCES macro_ramps.ramp_estimates(id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id);



ALTER TABLE ONLY macro_ramps.transactions
    ADD CONSTRAINT transactions_virtual_account_id_foreign FOREIGN KEY (virtual_account_id) REFERENCES macro_ramps.customer_virtual_accounts(id);



ALTER TABLE ONLY macro_ramps.user_bank_accounts
    ADD CONSTRAINT user_bank_accounts_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.user_liquidation_addresses
    ADD CONSTRAINT user_liquidation_addresses_customer_id_foreign FOREIGN KEY (customer_id) REFERENCES macro_ramps.customers(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.user_liquidation_addresses
    ADD CONSTRAINT user_liquidation_addresses_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id) ON DELETE CASCADE;



ALTER TABLE ONLY macro_ramps.user_pix_keys
    ADD CONSTRAINT user_pix_keys_user_id_foreign FOREIGN KEY (user_id) REFERENCES macro_ramps.users(id) ON DELETE CASCADE;

ALTER TABLE macro_ramps.customers ADD COLUMN tenant_key varchar(36) NOT NULL DEFAULT '';
ALTER TABLE macro_ramps.customers ADD COLUMN environment varchar(16) NOT NULL DEFAULT '';
DROP INDEX macro_ramps.customers_business_tax_id_hash_unique;
CREATE UNIQUE INDEX customers_business_tax_id_hash_unique ON macro_ramps.customers (tenant_key, tax_id_hash) WHERE type = 'business' AND status <> 'draft';
INSERT INTO macro_ramps.roles (name, guard, description) VALUES ('owner', 'customer', 'Owner of the verified holder') ON CONFLICT DO NOTHING;
CREATE TABLE ramp_customers (
 id uuid PRIMARY KEY, account_id uuid NOT NULL REFERENCES accounts(id),
 external_id varchar(255) NOT NULL, relationship varchar(24) NOT NULL CHECK (relationship IN ('account_holder','end_customer')),
 entity_type varchar(16) NOT NULL CHECK (entity_type IN ('individual','business')),
 name varchar(255) NOT NULL,
 engine_customer_id bigint NOT NULL UNIQUE REFERENCES macro_ramps.customers(id),
 engine_contact_id bigint NOT NULL UNIQUE REFERENCES macro_ramps.users(id),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (account_id, external_id), UNIQUE (account_id, id),
 CHECK (relationship <> 'account_holder' OR entity_type = 'business')
);
CREATE UNIQUE INDEX ramp_account_holder_unique ON ramp_customers(account_id) WHERE relationship = 'account_holder';
CREATE TABLE ramp_customer_actors (
 id uuid PRIMARY KEY, engine_admin_id bigint UNIQUE NOT NULL REFERENCES macro_ramps.admins(id)
);
CREATE TABLE ramp_tasks (
 id uuid PRIMARY KEY, account_id uuid REFERENCES accounts(id), customer_id uuid REFERENCES ramp_customers(id),
 kind varchar(40) NOT NULL, payload jsonb NOT NULL DEFAULT '{}', available_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0, locked_until timestamptz, completed_at timestamptz, last_error text,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ramp_tasks_due ON ramp_tasks(available_at) WHERE completed_at IS NULL;
CREATE TABLE ramp_outbox_events (
 id uuid PRIMARY KEY, account_id uuid NOT NULL REFERENCES accounts(id), customer_id uuid NOT NULL REFERENCES ramp_customers(id),
 event_type varchar(64) NOT NULL, payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 delivered_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ramp_outbox_pending ON ramp_outbox_events(created_at) WHERE delivered_at IS NULL;

-- Preserve identity uniqueness inside a tenant, including concurrent submissions.
ALTER TABLE macro_ramps.customer_individual_profiles ADD COLUMN tenant_key varchar(36) NOT NULL DEFAULT '';
CREATE FUNCTION macro_ramps.bind_profile_tenant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  SELECT tenant_key INTO STRICT NEW.tenant_key FROM macro_ramps.customers WHERE id = NEW.customer_id;
  RETURN NEW;
END $$;
CREATE TRIGGER bind_profile_tenant BEFORE INSERT OR UPDATE ON macro_ramps.customer_individual_profiles
 FOR EACH ROW EXECUTE FUNCTION macro_ramps.bind_profile_tenant();
DROP INDEX macro_ramps.customer_individual_profiles_tax_id_hash_unique;
DROP INDEX macro_ramps.customer_individual_profiles_phone_hash_unique;
CREATE UNIQUE INDEX customer_individual_profiles_tax_id_hash_unique ON macro_ramps.customer_individual_profiles(tenant_key,tax_id_hash) WHERE tax_id_hash IS NOT NULL;
CREATE UNIQUE INDEX customer_individual_profiles_phone_hash_unique ON macro_ramps.customer_individual_profiles(tenant_key,phone_hash) WHERE phone_hash IS NOT NULL;
CREATE TABLE ramp_customer_features (
 customer_id uuid NOT NULL REFERENCES ramp_customers(id) ON DELETE CASCADE,
 feature varchar(120) NOT NULL, enabled boolean NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(customer_id,feature)
);

CREATE UNIQUE INDEX ramp_webhook_task_pending ON ramp_tasks(kind,(payload->>'mode'),(payload->>'id')) WHERE kind='webhook' AND completed_at IS NULL;

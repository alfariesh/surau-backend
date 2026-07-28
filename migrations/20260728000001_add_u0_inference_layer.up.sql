-- U-0: one metered, attributable inference path for every LLM task.

ALTER TABLE service_principal_scopes
    DROP CONSTRAINT service_principal_scopes_scope_check;
ALTER TABLE service_principal_scopes
    ADD CONSTRAINT service_principal_scopes_scope_check CHECK (scope IN (
        'collab:draft:write',
        'rag-eval:read',
        'enrichment:read',
        'prompt-registry:manage',
        'inference-budget:manage',
        'inference:invoke'
    )) NOT VALID;
ALTER TABLE service_principal_scopes
    VALIDATE CONSTRAINT service_principal_scopes_scope_check;

INSERT INTO service_principals (id, principal_name, description)
VALUES (
    '753e6454-2392-4ee8-9c73-816416f16a94',
    'u0-inference',
    'Shared inference gateway for controlled generators'
)
ON CONFLICT (principal_name) DO NOTHING;

INSERT INTO service_principal_scopes (principal_id, scope)
SELECT id, scope
FROM service_principals
CROSS JOIN unnest(ARRAY[
    'inference:invoke',
    'prompt-registry:manage',
    'inference-budget:manage'
]) AS scope
WHERE principal_name = 'u0-inference'
ON CONFLICT DO NOTHING;

CREATE TABLE inference_tasks (
    task_key TEXT PRIMARY KEY,
    task_class TEXT NOT NULL,
    output_kind TEXT NOT NULL DEFAULT 'chat',
    cache_ttl_seconds INTEGER NOT NULL DEFAULT 0,
    persistent_enrichment BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT inference_tasks_key_check CHECK (
        task_key ~ '^[a-z][a-z0-9-]{2,95}$'
    ),
    CONSTRAINT inference_tasks_class_check CHECK (
        task_class IN ('rewrite', 'rerank', 'embed', 'answer', 'judge')
    ),
    CONSTRAINT inference_tasks_output_check CHECK (
        output_kind IN ('chat', 'structured', 'embedding')
    ),
    CONSTRAINT inference_tasks_cache_check CHECK (
        cache_ttl_seconds BETWEEN 0 AND 2592000
    ),
    CONSTRAINT inference_tasks_enrichment_cache_check CHECK (
        NOT persistent_enrichment OR cache_ttl_seconds = 0
    ),
    UNIQUE (task_key, task_class)
);

CREATE TABLE inference_providers (
    provider_key TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_key_env TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT inference_providers_key_check CHECK (
        provider_key ~ '^[a-z][a-z0-9-]{1,62}$'
    ),
    CONSTRAINT inference_providers_url_check CHECK (
        base_url ~ '^https?://'
    ),
    CONSTRAINT inference_providers_env_check CHECK (
        api_key_env ~ '^[A-Z][A-Z0-9_]{2,95}$'
    )
);

CREATE TABLE inference_models (
    provider_key TEXT NOT NULL REFERENCES inference_providers(provider_key),
    model_key TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    price_version TEXT,
    input_nano_usd_per_million BIGINT,
    cached_input_nano_usd_per_million BIGINT,
    output_nano_usd_per_million BIGINT,
    supports_json BOOLEAN NOT NULL DEFAULT true,
    supports_embeddings BOOLEAN NOT NULL DEFAULT false,
    timeout_ms INTEGER NOT NULL DEFAULT 45000,
    max_output_tokens INTEGER NOT NULL DEFAULT 1400,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_key, model_key),
    CONSTRAINT inference_models_key_check CHECK (
        model_key ~ '^[a-zA-Z0-9][a-zA-Z0-9._:/-]{1,126}$'
    ),
    CONSTRAINT inference_models_price_tuple_check CHECK (
        (price_version IS NULL
            AND input_nano_usd_per_million IS NULL
            AND cached_input_nano_usd_per_million IS NULL
            AND output_nano_usd_per_million IS NULL)
        OR
        (btrim(price_version) <> ''
            AND input_nano_usd_per_million >= 0
            AND cached_input_nano_usd_per_million >= 0
            AND output_nano_usd_per_million >= 0)
    ),
    CONSTRAINT inference_models_limits_check CHECK (
        timeout_ms BETWEEN 100 AND 300000
        AND max_output_tokens BETWEEN 1 AND 1000000
    )
);

CREATE TABLE inference_model_prices (
    provider_key TEXT NOT NULL,
    model_key TEXT NOT NULL,
    price_version TEXT NOT NULL,
    input_nano_usd_per_million BIGINT NOT NULL,
    cached_input_nano_usd_per_million BIGINT NOT NULL,
    output_nano_usd_per_million BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_key, model_key, price_version),
    CONSTRAINT inference_model_prices_model_fk FOREIGN KEY (provider_key, model_key)
        REFERENCES inference_models(provider_key, model_key),
    CONSTRAINT inference_model_prices_nonnegative_check CHECK (
        input_nano_usd_per_million >= 0
        AND cached_input_nano_usd_per_million >= 0
        AND output_nano_usd_per_million >= 0
    ),
    CONSTRAINT inference_model_prices_version_check CHECK (btrim(price_version) <> '')
);

CREATE TABLE inference_response_schemas (
    task_key TEXT NOT NULL REFERENCES inference_tasks(task_key),
    version TEXT NOT NULL,
    schema_body JSONB NOT NULL,
    content_sha256 TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_key, version),
    CONSTRAINT inference_schemas_version_check CHECK (btrim(version) <> ''),
    CONSTRAINT inference_schemas_body_check CHECK (
        jsonb_typeof(schema_body) = 'object'
    ),
    CONSTRAINT inference_schemas_hash_check CHECK (
        content_sha256 ~ '^[0-9a-f]{64}$'
    )
);

CREATE TABLE inference_prompt_versions (
    task_key TEXT NOT NULL REFERENCES inference_tasks(task_key),
    version TEXT NOT NULL,
    messages_template JSONB NOT NULL,
    content_sha256 TEXT NOT NULL,
    policy_sha256 TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_key, version),
    CONSTRAINT inference_prompts_version_check CHECK (btrim(version) <> ''),
    CONSTRAINT inference_prompts_body_check CHECK (
        jsonb_typeof(messages_template) = 'array'
    ),
    CONSTRAINT inference_prompts_hash_check CHECK (
        content_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT inference_prompts_policy_hash_check CHECK (
        policy_sha256 IS NULL OR policy_sha256 ~ '^[0-9a-f]{64}$'
    )
);

CREATE OR REPLACE FUNCTION inference_immutable_registry_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'inference prompt/schema versions are immutable'
        USING ERRCODE = 'object_not_in_prerequisite_state';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_inference_prompts_immutable
    BEFORE UPDATE OR DELETE ON inference_prompt_versions
    FOR EACH ROW EXECUTE FUNCTION inference_immutable_registry_guard();
CREATE TRIGGER trg_inference_schemas_immutable
    BEFORE UPDATE OR DELETE ON inference_response_schemas
    FOR EACH ROW EXECUTE FUNCTION inference_immutable_registry_guard();
CREATE TRIGGER trg_inference_model_prices_immutable
    BEFORE UPDATE OR DELETE ON inference_model_prices
    FOR EACH ROW EXECUTE FUNCTION inference_immutable_registry_guard();

CREATE TABLE inference_routes (
    task_key TEXT NOT NULL REFERENCES inference_tasks(task_key),
    priority SMALLINT NOT NULL,
    provider_key TEXT NOT NULL,
    model_key TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    response_schema_version TEXT NOT NULL,
    temperature DOUBLE PRECISION NOT NULL DEFAULT 0.1,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_key, priority),
    UNIQUE (task_key, provider_key, model_key),
    CONSTRAINT inference_routes_priority_check CHECK (priority IN (1, 2)),
    CONSTRAINT inference_routes_temperature_check CHECK (
        temperature BETWEEN 0 AND 2
    ),
    CONSTRAINT inference_routes_model_fk FOREIGN KEY (provider_key, model_key)
        REFERENCES inference_models(provider_key, model_key),
    CONSTRAINT inference_routes_prompt_fk FOREIGN KEY (task_key, prompt_version)
        REFERENCES inference_prompt_versions(task_key, version),
    CONSTRAINT inference_routes_schema_fk FOREIGN KEY (task_key, response_schema_version)
        REFERENCES inference_response_schemas(task_key, version)
);

CREATE TABLE inference_sessions (
    id UUID PRIMARY KEY,
    task_key TEXT NOT NULL REFERENCES inference_tasks(task_key),
    status TEXT NOT NULL DEFAULT 'open',
    pinned_provider_key TEXT,
    pinned_model_key TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT inference_sessions_status_check CHECK (
        status IN ('open', 'pinned', 'failed', 'completed')
    ),
    CONSTRAINT inference_sessions_pin_check CHECK (
        (pinned_provider_key IS NULL AND pinned_model_key IS NULL)
        OR
        (pinned_provider_key IS NOT NULL AND pinned_model_key IS NOT NULL)
    ),
    CONSTRAINT inference_sessions_model_fk
        FOREIGN KEY (pinned_provider_key, pinned_model_key)
        REFERENCES inference_models(provider_key, model_key)
);

CREATE TABLE inference_calls (
    id UUID PRIMARY KEY,
    task_key TEXT NOT NULL REFERENCES inference_tasks(task_key),
    task_class TEXT NOT NULL,
    session_id UUID REFERENCES inference_sessions(id),
    status TEXT NOT NULL DEFAULT 'started',
    final_generation_run_id UUID REFERENCES generation_runs(id),
    request_id TEXT,
    trace_id TEXT,
    cache_key TEXT,
    cache_status TEXT NOT NULL DEFAULT 'miss',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    cached_input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_nano_usd BIGINT NOT NULL DEFAULT 0,
    usage_source TEXT NOT NULL DEFAULT 'estimated',
    cost_source TEXT NOT NULL DEFAULT 'registry',
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT inference_calls_status_check CHECK (
        status IN ('started', 'succeeded', 'failed', 'budget_rejected', 'cache_hit')
    ),
    CONSTRAINT inference_calls_class_check CHECK (
        task_class IN ('rewrite', 'rerank', 'embed', 'answer', 'judge')
    ),
    CONSTRAINT inference_calls_cache_check CHECK (
        cache_status IN ('miss', 'hit', 'bypass')
    ),
    CONSTRAINT inference_calls_usage_check CHECK (
        usage_source IN ('provider', 'estimated', 'cache')
    ),
    CONSTRAINT inference_calls_cost_check CHECK (
        cost_source IN ('provider', 'registry', 'cache')
    ),
    CONSTRAINT inference_calls_nonnegative_check CHECK (
        input_tokens >= 0 AND cached_input_tokens >= 0 AND output_tokens >= 0
        AND cost_nano_usd >= 0
    ),
    CONSTRAINT inference_calls_task_class_fk FOREIGN KEY (task_key, task_class)
        REFERENCES inference_tasks(task_key, task_class)
);

CREATE INDEX idx_inference_calls_created
    ON inference_calls (created_at DESC, id);
CREATE INDEX idx_inference_calls_task_created
    ON inference_calls (task_key, created_at DESC, id);

CREATE TABLE inference_attempts (
    id UUID PRIMARY KEY,
    call_id UUID NOT NULL REFERENCES inference_calls(id) ON DELETE CASCADE,
    attempt_no SMALLINT NOT NULL,
    generation_run_id UUID NOT NULL REFERENCES generation_runs(id),
    task_key TEXT NOT NULL,
    task_class TEXT NOT NULL,
    provider_key TEXT NOT NULL,
    model_key TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    prompt_sha256 TEXT NOT NULL,
    response_schema_version TEXT NOT NULL,
    response_schema_sha256 TEXT NOT NULL,
    price_version TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'started',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    cached_input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_nano_usd BIGINT NOT NULL DEFAULT 0,
    usage_source TEXT NOT NULL DEFAULT 'estimated',
    cost_source TEXT NOT NULL DEFAULT 'registry',
    failover BOOLEAN NOT NULL DEFAULT false,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (call_id, attempt_no),
    UNIQUE (generation_run_id),
    CONSTRAINT inference_attempts_no_check CHECK (attempt_no IN (1, 2)),
    CONSTRAINT inference_attempts_labels_check CHECK (
        btrim(task_key) <> '' AND btrim(task_class) <> ''
        AND btrim(provider_key) <> '' AND btrim(model_key) <> ''
        AND btrim(provider_model_id) <> '' AND btrim(prompt_version) <> ''
        AND prompt_sha256 ~ '^[0-9a-f]{64}$'
        AND btrim(response_schema_version) <> ''
        AND response_schema_sha256 ~ '^[0-9a-f]{64}$'
        AND btrim(price_version) <> ''
    ),
    CONSTRAINT inference_attempts_status_check CHECK (
        status IN ('started', 'succeeded', 'failed')
    ),
    CONSTRAINT inference_attempts_usage_check CHECK (
        usage_source IN ('provider', 'estimated')
    ),
    CONSTRAINT inference_attempts_cost_check CHECK (
        cost_source IN ('provider', 'registry')
    ),
    CONSTRAINT inference_attempts_nonnegative_check CHECK (
        input_tokens >= 0 AND cached_input_tokens >= 0 AND output_tokens >= 0
        AND cost_nano_usd >= 0
    ),
    CONSTRAINT inference_attempts_task_class_fk FOREIGN KEY (task_key, task_class)
        REFERENCES inference_tasks(task_key, task_class),
    CONSTRAINT inference_attempts_model_fk FOREIGN KEY (provider_key, model_key)
        REFERENCES inference_models(provider_key, model_key),
    CONSTRAINT inference_attempts_price_fk FOREIGN KEY (
        provider_key, model_key, price_version
    ) REFERENCES inference_model_prices(provider_key, model_key, price_version),
    CONSTRAINT inference_attempts_prompt_fk FOREIGN KEY (task_key, prompt_version)
        REFERENCES inference_prompt_versions(task_key, version),
    CONSTRAINT inference_attempts_schema_fk FOREIGN KEY (task_key, response_schema_version)
        REFERENCES inference_response_schemas(task_key, version)
);

CREATE OR REPLACE FUNCTION inference_attempt_generation_guard() RETURNS TRIGGER AS $$
DECLARE
    generation generation_runs%ROWTYPE;
    registered_prompt_sha256 TEXT;
    registered_schema_sha256 TEXT;
    registered_model_id TEXT;
    registered_price_version TEXT;
BEGIN
    SELECT * INTO generation
    FROM generation_runs
    WHERE id = NEW.generation_run_id;

    IF generation.id IS NULL
       OR generation.task_name <> NEW.task_key
       OR generation.model_id <> NEW.provider_model_id
       OR generation.prompt_version <> NEW.prompt_version
       OR generation.provider IS DISTINCT FROM NEW.provider_key THEN
        RAISE EXCEPTION 'inference attempt attribution does not match generation run'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    SELECT content_sha256 INTO registered_prompt_sha256
    FROM inference_prompt_versions
    WHERE task_key = NEW.task_key AND version = NEW.prompt_version;
    SELECT content_sha256 INTO registered_schema_sha256
    FROM inference_response_schemas
    WHERE task_key = NEW.task_key AND version = NEW.response_schema_version;
    SELECT provider_model_id, price_version
    INTO registered_model_id, registered_price_version
    FROM inference_models
    WHERE provider_key = NEW.provider_key AND model_key = NEW.model_key;
    IF registered_prompt_sha256 IS DISTINCT FROM NEW.prompt_sha256
       OR registered_schema_sha256 IS DISTINCT FROM NEW.response_schema_sha256
       OR registered_model_id IS DISTINCT FROM NEW.provider_model_id
       OR registered_price_version IS DISTINCT FROM NEW.price_version THEN
        RAISE EXCEPTION 'inference attempt attribution does not match immutable registry'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_inference_attempt_generation
    BEFORE INSERT OR UPDATE ON inference_attempts
    FOR EACH ROW EXECUTE FUNCTION inference_attempt_generation_guard();

CREATE TABLE inference_budget_policies (
    revision BIGSERIAL PRIMARY KEY,
    mode TEXT NOT NULL DEFAULT 'baseline',
    timezone TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    baseline_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    baseline_days INTEGER NOT NULL DEFAULT 30,
    monthly_cap_nano_usd BIGINT,
    daily_cap_nano_usd BIGINT,
    alert_percent NUMERIC(5,2) NOT NULL DEFAULT 80.00,
    reason TEXT NOT NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT inference_budget_mode_check CHECK (
        mode IN ('baseline', 'enforce', 'disabled')
    ),
    CONSTRAINT inference_budget_baseline_check CHECK (
        baseline_days BETWEEN 1 AND 365
    ),
    CONSTRAINT inference_budget_caps_check CHECK (
        (monthly_cap_nano_usd IS NULL OR monthly_cap_nano_usd > 0)
        AND (daily_cap_nano_usd IS NULL OR daily_cap_nano_usd > 0)
    ),
    CONSTRAINT inference_budget_alert_check CHECK (
        alert_percent > 0 AND alert_percent <= 100
    ),
    CONSTRAINT inference_budget_reason_check CHECK (btrim(reason) <> '')
);

INSERT INTO inference_budget_policies (mode, reason)
VALUES ('baseline', 'U-0 safe default: measure 30 days before enforcing 2x baseline');

CREATE TABLE inference_budget_reservations (
    call_id UUID PRIMARY KEY REFERENCES inference_calls(id) ON DELETE CASCADE,
    reserved_nano_usd BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at TIMESTAMPTZ,
    CONSTRAINT inference_reservations_amount_check CHECK (
        reserved_nano_usd >= 0
    ),
    CONSTRAINT inference_reservations_status_check CHECK (
        status IN ('active', 'settled', 'released')
    )
);

CREATE INDEX idx_inference_reservations_active
    ON inference_budget_reservations (created_at)
    WHERE status = 'active';

CREATE TABLE inference_cache (
    cache_key TEXT PRIMARY KEY,
    task_key TEXT NOT NULL REFERENCES inference_tasks(task_key),
    generation_run_id UUID NOT NULL REFERENCES generation_runs(id),
    ciphertext TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_hit_at TIMESTAMPTZ,
    hit_count BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT inference_cache_key_check CHECK (
        cache_key ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT inference_cache_ciphertext_check CHECK (
        btrim(ciphertext) <> ''
    ),
    CONSTRAINT inference_cache_expiry_check CHECK (
        expires_at > created_at
    )
);

CREATE INDEX idx_inference_cache_expiry ON inference_cache (expires_at);

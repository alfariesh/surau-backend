DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM inference_calls LIMIT 1) THEN
        RAISE EXCEPTION
            'refusing U-0 down migration: inference ledger contains real calls; archive it explicitly first'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
END;
$$;

DROP TABLE IF EXISTS inference_cache;
DROP TABLE IF EXISTS inference_budget_reservations;
DROP TABLE IF EXISTS inference_budget_policies;
DROP TRIGGER IF EXISTS trg_inference_attempt_generation ON inference_attempts;
DROP FUNCTION IF EXISTS inference_attempt_generation_guard();
DROP TABLE IF EXISTS inference_attempts;
DROP TABLE IF EXISTS inference_calls;
DROP TABLE IF EXISTS inference_sessions;
DROP TABLE IF EXISTS inference_routes;
DROP TRIGGER IF EXISTS trg_inference_model_prices_immutable ON inference_model_prices;
DROP TRIGGER IF EXISTS trg_inference_schemas_immutable ON inference_response_schemas;
DROP TRIGGER IF EXISTS trg_inference_prompts_immutable ON inference_prompt_versions;
DROP FUNCTION IF EXISTS inference_immutable_registry_guard();
DROP TABLE IF EXISTS inference_prompt_versions;
DROP TABLE IF EXISTS inference_response_schemas;
DROP TABLE IF EXISTS inference_model_prices;
DROP TABLE IF EXISTS inference_models;
DROP TABLE IF EXISTS inference_providers;
DROP TABLE IF EXISTS inference_tasks;

DELETE FROM service_principal_scopes
WHERE principal_id = (
    SELECT id FROM service_principals WHERE principal_name = 'u0-inference'
);
DELETE FROM service_principals
WHERE principal_name = 'u0-inference'
  AND NOT EXISTS (
      SELECT 1 FROM service_tokens
      WHERE principal_id = service_principals.id
  );

ALTER TABLE service_principal_scopes
    DROP CONSTRAINT service_principal_scopes_scope_check;
ALTER TABLE service_principal_scopes
    ADD CONSTRAINT service_principal_scopes_scope_check CHECK (scope IN (
        'collab:draft:write',
        'rag-eval:read',
        'enrichment:read',
        'prompt-registry:manage',
        'inference-budget:manage'
    )) NOT VALID;
ALTER TABLE service_principal_scopes
    VALIDATE CONSTRAINT service_principal_scopes_scope_check;

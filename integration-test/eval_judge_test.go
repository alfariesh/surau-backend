//nolint:wsl_v5 // SQL-backed service identity and budget fixtures stay grouped by contract phase.
package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	evalJudgePrincipalID      = "00000000-0000-4000-8000-00000000e601"
	evalJudgeTokenID          = "00000000-0000-4000-8000-00000000e602"
	evalJudgeWrongPrincipalID = "00000000-0000-4000-8000-00000000e603"
	evalJudgeWrongTokenID     = "00000000-0000-4000-8000-00000000e604"
	evalJudgeBudgetCallID     = "00000000-0000-4000-8000-00000000e605"
)

func TestEvalJudgeServiceContract(t *testing.T) {
	allowedToken, wrongScopeToken := seedEvalJudgeCredentials(t)
	t.Cleanup(func() { cleanupEvalJudgeCredentials(t) })
	requestBody := `{"case_id":"kitab-1","rubric_version":"groundedness-v1",` +
		`"question":"Apa dalilnya?","answer":"Jawaban [S1].",` +
		`"evidence":[{"ref":"S1","quote":"Jawaban","anchor":"kitab/1/h/1/u/1"}]}`

	t.Run("missing token is unauthorized", func(t *testing.T) {
		response := postEvalJudge(t, requestBody, "")
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("missing token status = %d, want 401", response.StatusCode)
		}
	})

	t.Run("wrong scope is forbidden", func(t *testing.T) {
		response := postEvalJudge(t, requestBody, wrongScopeToken)
		defer response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("wrong scope status = %d, want 403", response.StatusCode)
		}
	})

	t.Run("allowlisted rubric returns complete attribution", func(t *testing.T) {
		response := postEvalJudge(t, requestBody, allowedToken)
		var body entity.EvalJudgeResponse
		decodeAndClose(t, response, &body)

		if response.StatusCode != http.StatusOK {
			t.Fatalf("judge status = %d, want 200", response.StatusCode)
		}
		if body.RubricVersion != "groundedness-v1" || body.RubricSHA256 == "" {
			t.Fatalf("rubric identity missing: %+v", body)
		}
		if !body.Passed || body.Reason == "" {
			t.Fatalf("deterministic judge result = %+v", body)
		}
		if body.Inference.CallID == "" || body.Inference.Generation.RunID == "" ||
			body.Inference.Model == "" || body.Inference.Prompt != "rag-judge-v1" ||
			body.Inference.Schema != "rag-judge-v1" {
			t.Fatalf("inference attribution incomplete: %+v", body.Inference)
		}
	})

	t.Run("caller cannot invent a rubric or task", func(t *testing.T) {
		unknown := bytes.ReplaceAll(
			[]byte(requestBody),
			[]byte("groundedness-v1"),
			[]byte("attacker-system-prompt"),
		)
		response := postEvalJudge(t, string(unknown), allowedToken)
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown rubric status = %d, want 400", response.StatusCode)
		}

		unknownTask := bytes.Replace(
			[]byte(requestBody),
			[]byte(`]}`),
			[]byte(`],"task_key":"attacker-task"}`),
			1,
		)
		response = postEvalJudge(t, string(unknownTask), allowedToken)
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown task status = %d, want 400", response.StatusCode)
		}
	})

	t.Run("exhausted inference budget is unavailable", func(t *testing.T) {
		restoreBudget := exhaustEvalJudgeBudget(t)
		defer restoreBudget()

		response := postEvalJudge(t, requestBody, allowedToken)
		defer response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("budget failure status = %d, want 503", response.StatusCode)
		}
	})
}

func seedEvalJudgeCredentials(t *testing.T) (allowedToken, wrongScopeToken string) {
	t.Helper()

	allowedToken = structuredEvalToken(evalJudgeTokenID, 'a')
	wrongScopeToken = structuredEvalToken(evalJudgeWrongTokenID, 'b')
	allowedHash := sha256.Sum256([]byte(allowedToken))
	wrongHash := sha256.Sum256([]byte(wrongScopeToken))

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	cleanupEvalJudgeCredentialsWithContext(ctx, t, pool)
	_, err := pool.Exec(
		ctx, `
INSERT INTO service_principals (id, principal_name, description)
VALUES
    ($1::uuid, 'u6-eval-integration', 'U-6 integration fixture'),
    ($2::uuid, 'u6-eval-wrong-scope', 'U-6 scope denial fixture')`,
		evalJudgePrincipalID,
		evalJudgeWrongPrincipalID,
	)
	if err != nil {
		t.Fatalf("seed eval judge principals: %v", err)
	}
	_, err = pool.Exec(
		ctx, `
INSERT INTO service_principal_scopes (principal_id, scope)
VALUES
    ($1::uuid, 'rag-eval:read'),
    ($2::uuid, 'enrichment:read')`,
		evalJudgePrincipalID,
		evalJudgeWrongPrincipalID,
	)
	if err != nil {
		t.Fatalf("seed eval judge scopes: %v", err)
	}
	_, err = pool.Exec(
		ctx, `
INSERT INTO service_tokens (id, principal_id, secret_hash, token_kind, expires_at)
VALUES
    ($3::uuid, $1::uuid, $5, 'structured', now() + INTERVAL '1 day'),
    ($4::uuid, $2::uuid, $6, 'structured', now() + INTERVAL '1 day')`,
		evalJudgePrincipalID,
		evalJudgeWrongPrincipalID,
		evalJudgeTokenID,
		evalJudgeWrongTokenID,
		allowedHash[:],
		wrongHash[:],
	)
	if err != nil {
		t.Fatalf("seed eval judge tokens: %v", err)
	}

	return allowedToken, wrongScopeToken
}

func cleanupEvalJudgeCredentials(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	cleanupEvalJudgeCredentialsWithContext(ctx, t, pool)
}

func cleanupEvalJudgeCredentialsWithContext(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	_, err := pool.Exec(
		ctx, `
DELETE FROM service_principals
WHERE id IN ($1::uuid, $2::uuid)`,
		evalJudgePrincipalID,
		evalJudgeWrongPrincipalID,
	)
	if err != nil {
		t.Fatalf("cleanup eval judge credentials: %v", err)
	}
}

func exhaustEvalJudgeBudget(t *testing.T) func() {
	t.Helper()

	pool := integrationDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	_, err := pool.Exec(
		ctx, `DELETE FROM inference_calls WHERE id=$1::uuid`,
		evalJudgeBudgetCallID,
	)
	if err != nil {
		pool.Close()
		t.Fatalf("cleanup stale eval judge budget call: %v", err)
	}
	_, err = pool.Exec(ctx, `
DELETE FROM inference_budget_policies WHERE reason='U-6 integration exhausted budget'`)
	if err != nil {
		pool.Close()
		t.Fatalf("cleanup stale eval judge budget policy: %v", err)
	}
	_, err = pool.Exec(
		ctx, `
INSERT INTO inference_calls (
    id, task_key, task_class, status, cost_nano_usd, completed_at
) VALUES ($1::uuid, 'rag-judge', 'judge', 'succeeded', 2, now())`,
		evalJudgeBudgetCallID,
	)
	if err != nil {
		pool.Close()
		t.Fatalf("seed eval judge budget usage: %v", err)
	}
	_, err = pool.Exec(ctx, `
INSERT INTO inference_budget_policies (
    mode, monthly_cap_nano_usd, daily_cap_nano_usd, reason
) VALUES ('enforce', 1, 1, 'U-6 integration exhausted budget')`)
	if err != nil {
		pool.Close()
		t.Fatalf("enforce eval judge budget: %v", err)
	}

	return func() {
		defer pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cleanupCancel()
		_, cleanupErr := pool.Exec(
			cleanupCtx, `DELETE FROM inference_calls WHERE id=$1::uuid`,
			evalJudgeBudgetCallID,
		)
		if cleanupErr != nil {
			t.Errorf("restore eval judge budget call: %v", cleanupErr)
		}
		_, cleanupErr = pool.Exec(cleanupCtx, `
DELETE FROM inference_budget_policies WHERE reason='U-6 integration exhausted budget'`)
		if cleanupErr != nil {
			t.Errorf("restore eval judge budget policy: %v", cleanupErr)
		}
	}
}

func structuredEvalToken(tokenID string, fill byte) string {
	secret := bytes.Repeat([]byte{fill}, 32)

	return "surau_st_" + tokenID + "." + base64.RawURLEncoding.EncodeToString(secret)
}

func postEvalJudge(t *testing.T, body, token string) *http.Response {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL()+"/v1/eval/judge",
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("new judge request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("X-Internal-Token", token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST eval judge: %v", err)
	}

	return response
}

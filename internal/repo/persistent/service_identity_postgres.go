package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const servicePrincipalColumns = `
id::text, principal_name, description, created_by::text, revoked_at,
created_at, updated_at`

var errServiceRequestAuditNotFound = errors.New("service request audit row not found")

// ServiceIdentityRepo persists named machine identities and their audit trail.
type ServiceIdentityRepo struct {
	*postgres.Postgres
}

// NewServiceIdentityRepo constructs the A-2 persistence adapter.
func NewServiceIdentityRepo(pg *postgres.Postgres) *ServiceIdentityRepo {
	return &ServiceIdentityRepo{Postgres: pg}
}

//nolint:gocritic // value parameter follows the repository contract and is not retained
func (r *ServiceIdentityRepo) CreateServicePrincipal(
	ctx context.Context,
	principal entity.ServicePrincipal,
	actorID string,
) (entity.ServicePrincipal, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Create begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	actor := nullableServiceString(actorID)
	row := tx.QueryRow(
		ctx, `
INSERT INTO service_principals (
    id, principal_name, description, created_by, created_at, updated_at
) VALUES ($1::uuid, $2, $3, $4::uuid, $5, $5)
RETURNING `+servicePrincipalColumns,
		principal.ID, principal.PrincipalName, principal.Description, actor,
		principal.CreatedAt,
	)

	saved, err := scanServicePrincipal(row)
	if err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if scopeErr := replaceServiceScopes(ctx, tx, saved.ID, principal.Scopes); scopeErr != nil {
		return entity.ServicePrincipal{}, scopeErr
	}

	if eventErr := insertServiceIdentityEvent(ctx, tx, entity.ServiceIdentityEvent{
		ID:            principal.ID,
		PrincipalID:   &saved.ID,
		PrincipalName: saved.PrincipalName,
		ActorUserID:   stringPointer(actorID),
		Action:        "principal_created",
		Metadata:      map[string]any{"scopes": principal.Scopes},
		CreatedAt:     principal.CreatedAt,
	}); eventErr != nil {
		return entity.ServicePrincipal{}, eventErr
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Create commit: %w", err)
	}

	return r.GetServicePrincipal(ctx, saved.ID)
}

func (r *ServiceIdentityRepo) ListServicePrincipals(
	ctx context.Context,
	filter repo.ServicePrincipalFilter,
) ([]entity.ServicePrincipal, int, error) {
	var total int
	if err := r.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM service_principals`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("ServiceIdentityRepo.List count: %w", err)
	}

	rows, err := r.Pool.Query(ctx, `
SELECT id::text
FROM service_principals
ORDER BY principal_name, id
LIMIT $1 OFFSET $2`, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("ServiceIdentityRepo.List query: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, filter.Limit)

	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, 0, fmt.Errorf("ServiceIdentityRepo.List scan: %w", err)
		}

		ids = append(ids, id)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ServiceIdentityRepo.List rows: %w", err)
	}

	items := make([]entity.ServicePrincipal, 0, len(ids))
	for _, id := range ids {
		item, getErr := r.GetServicePrincipal(ctx, id)
		if getErr != nil {
			return nil, 0, getErr
		}

		items = append(items, item)
	}

	return items, total, nil
}

func (r *ServiceIdentityRepo) GetServicePrincipal(
	ctx context.Context,
	id string,
) (entity.ServicePrincipal, error) {
	row := r.Pool.QueryRow(ctx, `
SELECT `+servicePrincipalColumns+`
FROM service_principals
WHERE id = $1::uuid`, id)

	principal, err := scanServicePrincipal(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
		}

		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Get principal: %w", err)
	}

	principal.Scopes, err = r.serviceScopes(ctx, id)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	principal.Tokens, err = r.serviceTokens(ctx, id)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	return principal, nil
}

func (r *ServiceIdentityRepo) UpdateServicePrincipal(
	ctx context.Context,
	id, description string,
	scopes []string,
	actorID string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Update begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	principal, err := lockServicePrincipal(ctx, tx, id, expected, force)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	if principal.RevokedAt != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalRevoked
	}

	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `
UPDATE service_principals
SET description = $2, updated_at = $3
WHERE id = $1::uuid`, id, description, now); err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if scopeErr := replaceServiceScopes(ctx, tx, id, scopes); scopeErr != nil {
		return entity.ServicePrincipal{}, scopeErr
	}

	if eventErr := insertServiceIdentityEvent(ctx, tx, entity.ServiceIdentityEvent{
		ID:            uuid.NewString(),
		PrincipalID:   &id,
		PrincipalName: principal.PrincipalName,
		ActorUserID:   stringPointer(actorID),
		Action:        "principal_updated",
		Metadata:      map[string]any{"scopes": scopes},
		CreatedAt:     now,
	}); eventErr != nil {
		return entity.ServicePrincipal{}, eventErr
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Update commit: %w", err)
	}

	return r.GetServicePrincipal(ctx, id)
}

//nolint:gocritic // value parameter follows the repository contract and is not retained
func (r *ServiceIdentityRepo) IssueServiceToken(
	ctx context.Context,
	token entity.ServiceTokenRecord,
	actorID string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.IssueToken begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	principal, err := lockServicePrincipal(ctx, tx, token.PrincipalID, expected, force)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	if principal.RevokedAt != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalRevoked
	}

	_, err = tx.Exec(
		ctx, `
INSERT INTO service_tokens (
    id, principal_id, secret_hash, token_kind, expires_at, created_by, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7)`,
		token.ID, token.PrincipalID, token.SecretHash, token.TokenKind,
		token.ExpiresAt, nullableServiceString(actorID), token.CreatedAt,
	)
	if err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE service_principals SET updated_at = $2 WHERE id = $1::uuid`,
		token.PrincipalID, token.CreatedAt); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.IssueToken touch principal: %w", err)
	}

	if eventErr := insertServiceIdentityEvent(ctx, tx, entity.ServiceIdentityEvent{
		ID:            uuid.NewString(),
		PrincipalID:   &token.PrincipalID,
		PrincipalName: principal.PrincipalName,
		TokenID:       &token.ID,
		ActorUserID:   stringPointer(actorID),
		Action:        "token_issued",
		Metadata:      map[string]any{"expires_at": token.ExpiresAt},
		CreatedAt:     token.CreatedAt,
	}); eventErr != nil {
		return entity.ServicePrincipal{}, eventErr
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.IssueToken commit: %w", err)
	}

	return r.GetServicePrincipal(ctx, token.PrincipalID)
}

//nolint:cyclop,funlen,gocyclo // transactional idempotency distinguishes missing and already-revoked credentials
func (r *ServiceIdentityRepo) RevokeServiceToken(
	ctx context.Context,
	principalID, tokenID, actorID string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokeToken begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	principal, err := lockServicePrincipal(ctx, tx, principalID, expected, force)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	now := time.Now().UTC()

	tag, err := tx.Exec(ctx, `
UPDATE service_tokens
SET revoked_at = $3
WHERE id = $1::uuid AND principal_id = $2::uuid AND revoked_at IS NULL`,
		tokenID, principalID, now)
	if err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if tag.RowsAffected() == 0 {
		var exists bool
		if scanErr := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM service_tokens WHERE id = $1::uuid AND principal_id = $2::uuid
)`, tokenID, principalID).Scan(&exists); scanErr != nil {
			return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokeToken exists: %w", scanErr)
		}

		if !exists {
			return entity.ServicePrincipal{}, entity.ErrServiceTokenNotFound
		}
		// Already revoked is idempotent and does not create a duplicate event.
		if err = tx.Commit(ctx); err != nil {
			return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokeToken idempotent commit: %w", err)
		}

		return r.GetServicePrincipal(ctx, principalID)
	}

	if _, err = tx.Exec(ctx, `UPDATE service_principals SET updated_at = $2 WHERE id = $1::uuid`,
		principalID, now); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokeToken touch principal: %w", err)
	}

	if eventErr := insertServiceIdentityEvent(ctx, tx, entity.ServiceIdentityEvent{
		ID:            uuid.NewString(),
		PrincipalID:   &principalID,
		PrincipalName: principal.PrincipalName,
		TokenID:       &tokenID,
		ActorUserID:   stringPointer(actorID),
		Action:        "token_revoked",
		CreatedAt:     now,
	}); eventErr != nil {
		return entity.ServicePrincipal{}, eventErr
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokeToken commit: %w", err)
	}

	return r.GetServicePrincipal(ctx, principalID)
}

func (r *ServiceIdentityRepo) RevokeServicePrincipal(
	ctx context.Context,
	principalID, actorID string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokePrincipal begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	principal, err := lockServicePrincipal(ctx, tx, principalID, expected, force)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	if principal.RevokedAt != nil {
		if err = tx.Commit(ctx); err != nil {
			return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokePrincipal idempotent commit: %w", err)
		}

		return r.GetServicePrincipal(ctx, principalID)
	}

	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `
UPDATE service_principals SET revoked_at = $2, updated_at = $2 WHERE id = $1::uuid`,
		principalID, now); err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if eventErr := insertServiceIdentityEvent(ctx, tx, entity.ServiceIdentityEvent{
		ID:            uuid.NewString(),
		PrincipalID:   &principalID,
		PrincipalName: principal.PrincipalName,
		ActorUserID:   stringPointer(actorID),
		Action:        "principal_revoked",
		CreatedAt:     now,
	}); eventErr != nil {
		return entity.ServicePrincipal{}, eventErr
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.RevokePrincipal commit: %w", err)
	}

	return r.GetServicePrincipal(ctx, principalID)
}

func (r *ServiceIdentityRepo) GetServiceCredentialByID(
	ctx context.Context,
	tokenID string,
) (entity.ServiceCredential, error) {
	return scanServiceCredential(r.Pool.QueryRow(ctx, serviceCredentialQuery+`
WHERE st.id = $1::uuid
GROUP BY st.id, sp.id`, tokenID))
}

func (r *ServiceIdentityRepo) GetServiceCredentialByHash(
	ctx context.Context,
	hash []byte,
) (entity.ServiceCredential, error) {
	return scanServiceCredential(r.Pool.QueryRow(ctx, serviceCredentialQuery+`
WHERE st.secret_hash = $1
GROUP BY st.id, sp.id`, hash))
}

//nolint:cyclop,funlen,gocritic,gocyclo // one-time bootstrap keeps both input records and idempotent transaction branches explicit
func (r *ServiceIdentityRepo) BootstrapLegacyServiceToken(
	ctx context.Context,
	principal entity.ServicePrincipal,
	token entity.ServiceTokenRecord,
) (entity.ServicePrincipal, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Bootstrap begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var existing entity.ServicePrincipal

	row := tx.QueryRow(ctx, `
SELECT `+servicePrincipalColumns+`
FROM service_principals
WHERE principal_name = $1
FOR UPDATE`, principal.PrincipalName)

	existing, err = scanServicePrincipal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		row = tx.QueryRow(ctx, `
INSERT INTO service_principals (
    id, principal_name, description, created_at, updated_at
) VALUES ($1::uuid, $2, $3, $4, $4)
RETURNING `+servicePrincipalColumns,
			principal.ID, principal.PrincipalName, principal.Description, principal.CreatedAt)
		existing, err = scanServicePrincipal(row)
	}

	if err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if existing.RevokedAt != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalRevoked
	}

	for _, scope := range principal.Scopes {
		if _, err = tx.Exec(ctx, `
INSERT INTO service_principal_scopes (principal_id, scope)
VALUES ($1::uuid, $2)
ON CONFLICT DO NOTHING`, existing.ID, scope); err != nil {
			return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
		}
	}

	var existingTokenID string

	err = tx.QueryRow(ctx, `SELECT id::text FROM service_tokens WHERE secret_hash = $1`, token.SecretHash).
		Scan(&existingTokenID)
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Bootstrap existing commit: %w", commitErr)
		}

		return r.GetServicePrincipal(ctx, existing.ID)
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Bootstrap token lookup: %w", err)
	}

	if _, err = tx.Exec(ctx, `
INSERT INTO service_tokens (
    id, principal_id, secret_hash, token_kind, expires_at, created_at
) VALUES ($1::uuid, $2::uuid, $3, 'legacy', $4, $5)`,
		token.ID, existing.ID, token.SecretHash, token.ExpiresAt, token.CreatedAt); err != nil {
		return entity.ServicePrincipal{}, mapServiceIdentityWriteError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE service_principals SET updated_at = $2 WHERE id = $1::uuid`,
		existing.ID, token.CreatedAt); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Bootstrap touch principal: %w", err)
	}

	if eventErr := insertServiceIdentityEvent(ctx, tx, entity.ServiceIdentityEvent{
		ID:            uuid.NewString(),
		PrincipalID:   &existing.ID,
		PrincipalName: existing.PrincipalName,
		TokenID:       &token.ID,
		Action:        "legacy_token_bootstrapped",
		Metadata:      map[string]any{"expires_at": token.ExpiresAt},
		CreatedAt:     token.CreatedAt,
	}); eventErr != nil {
		return entity.ServicePrincipal{}, eventErr
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo.Bootstrap commit: %w", err)
	}

	return r.GetServicePrincipal(ctx, existing.ID)
}

//nolint:gocritic // value parameter follows the repository contract and is not retained
func (r *ServiceIdentityRepo) CreateServiceRequestAudit(
	ctx context.Context,
	audit entity.ServiceRequestAudit,
) error {
	_, err := r.Pool.Exec(ctx, `
INSERT INTO service_request_audit_logs (
    id, principal_id, principal_name, token_id, required_scope, method,
    route_template, request_id, trace_id, client_ip, auth_outcome,
    response_status, started_at, finished_at
) VALUES (
    $1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9,
    NULLIF($10, '')::inet, $11, $12, $13, $14
)`, audit.ID, nullablePointer(audit.PrincipalID), audit.PrincipalName,
		nullablePointer(audit.TokenID), nullablePointer(audit.RequiredScope), audit.Method,
		audit.RouteTemplate, nullablePointer(audit.RequestID), nullablePointer(audit.TraceID),
		nullablePointer(audit.ClientIP), audit.AuthOutcome, audit.ResponseStatus,
		audit.StartedAt, audit.FinishedAt)
	if err != nil {
		return fmt.Errorf("ServiceIdentityRepo.CreateRequestAudit: %w", err)
	}

	return nil
}

func (r *ServiceIdentityRepo) FinishServiceRequestAudit(
	ctx context.Context,
	id, outcome string,
	status int,
	finishedAt time.Time,
) error {
	tag, err := r.Pool.Exec(ctx, `
UPDATE service_request_audit_logs
SET auth_outcome = $2, response_status = $3, finished_at = $4
WHERE id = $1::uuid`, id, outcome, status, finishedAt)
	if err != nil {
		return fmt.Errorf("ServiceIdentityRepo.FinishRequestAudit: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("ServiceIdentityRepo.FinishRequestAudit: %w", errServiceRequestAuditNotFound)
	}

	return nil
}

func (r *ServiceIdentityRepo) CleanupServiceRequestAudits(
	ctx context.Context,
	before time.Time,
) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM service_request_audit_logs WHERE started_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("ServiceIdentityRepo.CleanupRequestAudits: %w", err)
	}

	return tag.RowsAffected(), nil
}

// #nosec G101 -- SQL query name describes credential metadata, not a hardcoded secret.
const serviceCredentialQuery = `
SELECT st.id::text, st.principal_id::text, st.token_kind, st.expires_at,
       st.revoked_at, st.created_by::text, st.created_at, st.secret_hash,
       sp.id::text, sp.principal_name, sp.revoked_at,
       COALESCE(array_agg(sps.scope ORDER BY sps.scope)
           FILTER (WHERE sps.scope IS NOT NULL), ARRAY[]::text[])
FROM service_tokens st
JOIN service_principals sp ON sp.id = st.principal_id
LEFT JOIN service_principal_scopes sps ON sps.principal_id = sp.id
`

func scanServiceCredential(row pgx.Row) (entity.ServiceCredential, error) {
	var credential entity.ServiceCredential

	err := row.Scan(
		&credential.Token.ID,
		&credential.Token.PrincipalID,
		&credential.Token.TokenKind,
		&credential.Token.ExpiresAt,
		&credential.Token.RevokedAt,
		&credential.Token.CreatedBy,
		&credential.Token.CreatedAt,
		&credential.Token.SecretHash,
		&credential.PrincipalID,
		&credential.PrincipalName,
		&credential.RevokedAt,
		&credential.Scopes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ServiceCredential{}, entity.ErrServiceTokenNotFound
	}

	if err != nil {
		return entity.ServiceCredential{}, fmt.Errorf("ServiceIdentityRepo credential scan: %w", err)
	}

	return credential, nil
}

func (r *ServiceIdentityRepo) serviceScopes(ctx context.Context, id string) ([]string, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT scope FROM service_principal_scopes WHERE principal_id = $1::uuid ORDER BY scope`, id)
	if err != nil {
		return nil, fmt.Errorf("ServiceIdentityRepo scopes query: %w", err)
	}
	defer rows.Close()

	scopes := make([]string, 0)

	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("ServiceIdentityRepo scopes scan: %w", err)
		}

		scopes = append(scopes, scope)
	}

	return scopes, rows.Err()
}

func (r *ServiceIdentityRepo) serviceTokens(ctx context.Context, id string) ([]entity.ServiceToken, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT id::text, principal_id::text, token_kind, expires_at, revoked_at,
       created_by::text, created_at
FROM service_tokens
WHERE principal_id = $1::uuid
ORDER BY created_at DESC, id`, id)
	if err != nil {
		return nil, fmt.Errorf("ServiceIdentityRepo tokens query: %w", err)
	}
	defer rows.Close()

	tokens := make([]entity.ServiceToken, 0)

	for rows.Next() {
		var token entity.ServiceToken
		if err = rows.Scan(&token.ID, &token.PrincipalID, &token.TokenKind,
			&token.ExpiresAt, &token.RevokedAt, &token.CreatedBy, &token.CreatedAt); err != nil {
			return nil, fmt.Errorf("ServiceIdentityRepo tokens scan: %w", err)
		}

		tokens = append(tokens, token)
	}

	return tokens, rows.Err()
}

func scanServicePrincipal(row pgx.Row) (entity.ServicePrincipal, error) {
	var principal entity.ServicePrincipal

	err := row.Scan(
		&principal.ID,
		&principal.PrincipalName,
		&principal.Description,
		&principal.CreatedBy,
		&principal.RevokedAt,
		&principal.CreatedAt,
		&principal.UpdatedAt,
	)

	return principal, err
}

func lockServicePrincipal(
	ctx context.Context,
	tx pgx.Tx,
	id string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	principal, err := scanServicePrincipal(tx.QueryRow(ctx, `
SELECT `+servicePrincipalColumns+`
FROM service_principals
WHERE id = $1::uuid
FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	if err != nil {
		return entity.ServicePrincipal{}, fmt.Errorf("ServiceIdentityRepo lock principal: %w", err)
	}

	if !force {
		if expected == nil {
			return entity.ServicePrincipal{}, entity.ErrPreconditionRequired
		}

		if !principal.UpdatedAt.Equal(expected.UTC()) {
			return entity.ServicePrincipal{}, entity.ErrPreconditionFailed
		}
	}

	return principal, nil
}

func replaceServiceScopes(ctx context.Context, tx pgx.Tx, id string, scopes []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM service_principal_scopes WHERE principal_id = $1::uuid`, id); err != nil {
		return fmt.Errorf("ServiceIdentityRepo replace scopes delete: %w", err)
	}

	for _, scope := range scopes {
		if _, err := tx.Exec(ctx, `
INSERT INTO service_principal_scopes (principal_id, scope) VALUES ($1::uuid, $2)`, id, scope); err != nil {
			return mapServiceIdentityWriteError(err)
		}
	}

	return nil
}

//nolint:gocritic // event is short-lived and mirrors the append-only persistence record
func insertServiceIdentityEvent(ctx context.Context, tx pgx.Tx, event entity.ServiceIdentityEvent) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("ServiceIdentityRepo event metadata: %w", err)
	}

	if event.Metadata == nil {
		metadata = []byte(`{}`)
	}

	_, err = tx.Exec(ctx, `
INSERT INTO service_identity_events (
    id, principal_id, principal_name, token_id, actor_user_id,
    action, metadata, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6, $7::jsonb, $8)`,
		event.ID, nullablePointer(event.PrincipalID), event.PrincipalName,
		nullablePointer(event.TokenID), nullablePointer(event.ActorUserID),
		event.Action, metadata, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("ServiceIdentityRepo insert event: %w", err)
	}

	return nil
}

func mapServiceIdentityWriteError(err error) error {
	var pgErr *pgconn.PgError
	if isUniqueViolation(err) || (errors.As(err, &pgErr) && pgErr.Code == "23514") {
		return fmt.Errorf("%w: %w", entity.ErrInvalidServicePrincipal, err)
	}

	return fmt.Errorf("ServiceIdentityRepo write: %w", err)
}

func nullableServiceString(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func nullablePointer(value *string) any {
	if value == nil || *value == "" {
		return nil
	}

	return *value
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

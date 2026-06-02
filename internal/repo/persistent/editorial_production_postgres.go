package persistent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type productionQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateProductionProject creates an active book+language production project.
func (r *EditorialRepo) CreateProductionProject(
	ctx context.Context,
	actorID string,
	project entity.BookProductionProject,
) (entity.BookProductionProject, error) {
	if err := r.ensureProductionSourceReady(ctx, project.BookID); err != nil {
		return entity.BookProductionProject{}, err
	}

	sqlText := `
INSERT INTO book_production_projects (
    id, book_id, lang, workflow_status, publication_status, requires_review,
    requires_audio, priority, owner_id, notes, created_by, updated_by, created_at, updated_at
)
VALUES ($1, $2, $3, $4, 'hidden', $5, $6, $7, $8, $9, $10, $10, now(), now())
ON CONFLICT DO NOTHING
RETURNING id, book_id, lang, workflow_status, publication_status, requires_review,
          requires_audio, priority, owner_id, notes, created_by, updated_by, published_by,
          created_at, updated_at, published_at, archived_at`

	saved, err := scanProductionProject(r.Pool.QueryRow(
		ctx,
		sqlText,
		uuid.New().String(),
		project.BookID,
		project.Lang,
		project.WorkflowStatus,
		project.RequiresReview,
		project.RequiresAudio,
		project.Priority,
		project.OwnerID,
		project.Notes,
		actorID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionProject{}, r.productionProjectExistsError(ctx, project.BookID, project.Lang)
		}

		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - CreateProductionProject - scan: %w", err)
	}

	_ = r.audit(ctx, actorID, "production_project.create", saved.BookID, nil, nil, saved.Lang, saved)
	_ = r.recordProductionEvent(ctx, actorID, saved.ID, entity.ProductionEventProjectCreate, nil, nil, nil, saved)

	return saved, nil
}

func (r *EditorialRepo) productionProjectExistsError(ctx context.Context, bookID int, lang string) error {
	var existingProjectID string
	err := r.Pool.QueryRow(ctx, `
SELECT id
FROM book_production_projects
WHERE book_id = $1 AND lang = $2 AND workflow_status <> 'archived'
ORDER BY updated_at DESC
LIMIT 1`, bookID, lang).Scan(&existingProjectID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("EditorialRepo - productionProjectExistsError - lookup: %w", err)
		}

		return entity.ErrProductionProjectExists
	}

	return entity.NewProductionProjectExistsError(existingProjectID)
}

// ListProductionProjects returns paginated production workflows.
func (r *EditorialRepo) ListProductionProjects(
	ctx context.Context,
	filter repo.ProductionProjectFilter,
) ([]entity.BookProductionProject, int, error) {
	countBuilder := r.Builder.Select("COUNT(*)").From("book_production_projects p")
	dataBuilder := productionProjectSelectBuilder(r.Builder).
		OrderBy("p.priority DESC", "p.updated_at DESC", "p.created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	countBuilder, dataBuilder = applyProductionProjectFilter(countBuilder, dataBuilder, filter)

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionProjects - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionProjects - builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionProjects - query: %w", err)
	}
	defer rows.Close()

	projects := make([]entity.BookProductionProject, 0, filter.Limit)
	for rows.Next() {
		project, err := scanProductionProject(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("EditorialRepo - ListProductionProjects - scan: %w", err)
		}

		projects = append(projects, project)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionProjects - rows: %w", err)
	}

	return projects, total, nil
}

// GetProductionProject returns one production project.
func (r *EditorialRepo) GetProductionProject(ctx context.Context, projectID string) (entity.BookProductionProject, error) {
	sqlText, args, err := productionProjectSelectBuilder(r.Builder).
		Where(sq.Eq{"p.id": projectID}).
		ToSql()
	if err != nil {
		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - GetProductionProject - builder: %w", err)
	}

	project, err := scanProductionProject(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
		}

		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - GetProductionProject - scan: %w", err)
	}

	return project, nil
}

// UpdateProductionProject updates mutable workflow fields.
func (r *EditorialRepo) UpdateProductionProject(
	ctx context.Context,
	actorID,
	projectID string,
	patch entity.BookProductionProjectPatch,
) (entity.BookProductionProject, error) {
	builder := r.Builder.
		Update("book_production_projects").
		Set("updated_by", actorID).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": projectID}).
		Suffix(`RETURNING id, book_id, lang, workflow_status, publication_status, requires_review,
          requires_audio, priority, owner_id, notes, created_by, updated_by, published_by,
          created_at, updated_at, published_at, archived_at`)

	if patch.WorkflowStatus != nil {
		builder = builder.Set("workflow_status", *patch.WorkflowStatus)
		if *patch.WorkflowStatus == entity.ProductionWorkflowArchived {
			builder = builder.Set("archived_at", sq.Expr("COALESCE(archived_at, now())"))
			builder = builder.Set("publication_status", entity.ProductionPublicationArchived)
		}
	}
	if patch.RequiresReview != nil {
		builder = builder.Set("requires_review", *patch.RequiresReview)
	}
	if patch.RequiresAudio != nil {
		builder = builder.Set("requires_audio", *patch.RequiresAudio)
	}
	if patch.Priority != nil {
		builder = builder.Set("priority", *patch.Priority)
	}
	if patch.OwnerID != nil {
		builder = builder.Set("owner_id", *patch.OwnerID)
	}
	if patch.Notes != nil {
		builder = builder.Set("notes", *patch.Notes)
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - UpdateProductionProject - builder: %w", err)
	}

	project, err := scanProductionProject(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
		}

		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - UpdateProductionProject - scan: %w", err)
	}

	_ = r.audit(ctx, actorID, "production_project.update", project.BookID, nil, nil, project.Lang, project)
	_ = r.recordProductionEvent(ctx, actorID, project.ID, entity.ProductionEventProjectUpdate, nil, nil, nil, project)

	return project, nil
}

// ProductionCompleteness calculates publish readiness for a project.
func (r *EditorialRepo) ProductionCompleteness(
	ctx context.Context,
	projectID string,
) (entity.BookProductionCompleteness, error) {
	project, err := r.GetProductionProject(ctx, projectID)
	if err != nil {
		return entity.BookProductionCompleteness{}, err
	}

	return r.productionCompleteness(ctx, r.Pool, project)
}

// ProductionWorkspace returns a compact production dashboard for one project.
func (r *EditorialRepo) ProductionWorkspace(ctx context.Context, projectID string) (entity.BookProductionWorkspace, error) {
	project, err := r.GetProductionProject(ctx, projectID)
	if err != nil {
		return entity.BookProductionWorkspace{}, err
	}

	completeness, err := r.productionCompleteness(ctx, r.Pool, project)
	if err != nil {
		return entity.BookProductionWorkspace{}, err
	}

	book, err := r.productionWorkspaceBook(ctx, project.BookID)
	if err != nil {
		return entity.BookProductionWorkspace{}, err
	}

	metadataFinal := productionFinalExists(ctx, r.Pool, `
SELECT EXISTS (
    SELECT 1
    FROM book_metadata_translations
    WHERE book_id = $1 AND lang = $2 AND is_deleted = false
)`, project.BookID, project.Lang)
	metadata, err := productionScalarAssetStatus(
		ctx,
		r.Pool,
		project,
		entity.ProductionAssetBookMetadata,
		true,
		"book_metadata_translation_edits",
		"NULLIF(BTRIM(display_title), '') IS NOT NULL",
		metadataFinal,
	)
	if err != nil {
		return entity.BookProductionWorkspace{}, err
	}

	var author *entity.ProductionAssetStatus
	if book.AuthorID != nil {
		authorFinal := productionFinalExists(ctx, r.Pool, `
SELECT EXISTS (
    SELECT 1
    FROM author_translations
    WHERE author_id = $1 AND lang = $2 AND is_deleted = false
)`, *book.AuthorID, project.Lang)
		authorStatus, statusErr := productionScalarAssetStatus(
			ctx,
			r.Pool,
			project,
			entity.ProductionAssetAuthorMetadata,
			true,
			"author_translation_edits",
			"NULLIF(BTRIM(name), '') IS NOT NULL",
			authorFinal,
		)
		if statusErr != nil {
			return entity.BookProductionWorkspace{}, statusErr
		}
		author = &authorStatus
	}

	var category *entity.ProductionAssetStatus
	if book.CategoryID != nil {
		categoryFinal := productionFinalExists(ctx, r.Pool, `
SELECT EXISTS (
    SELECT 1
    FROM category_translations
    WHERE category_id = $1 AND lang = $2 AND is_deleted = false
)`, *book.CategoryID, project.Lang)
		categoryStatus, statusErr := productionScalarAssetStatus(
			ctx,
			r.Pool,
			project,
			entity.ProductionAssetCategoryMetadata,
			true,
			"category_translation_edits",
			"NULLIF(BTRIM(name), '') IS NOT NULL",
			categoryFinal,
		)
		if statusErr != nil {
			return entity.BookProductionWorkspace{}, statusErr
		}
		category = &categoryStatus
	}

	headings, err := productionWorkspaceHeadings(ctx, r.Pool, project)
	if err != nil {
		return entity.BookProductionWorkspace{}, err
	}

	return entity.BookProductionWorkspace{
		Project:      project,
		Book:         book,
		Completeness: completeness,
		Metadata:     metadata,
		Author:       author,
		Category:     category,
		Headings:     headings,
	}, nil
}

// ListProductionEvents returns timeline events for one production project.
func (r *EditorialRepo) ListProductionEvents(
	ctx context.Context,
	projectID string,
	limit,
	offset uint64,
) ([]entity.BookProductionEvent, int, error) {
	if _, err := r.GetProductionProject(ctx, projectID); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.Pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM book_production_events
WHERE project_id = $1`, projectID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEvents - count: %w", err)
	}

	rows, err := r.Pool.Query(ctx, `
SELECT id, project_id, actor_id, event_type, asset_type, heading_id, note, payload, created_at
FROM book_production_events
WHERE project_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`, projectID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEvents - query: %w", err)
	}
	defer rows.Close()

	events := make([]entity.BookProductionEvent, 0, limit)
	for rows.Next() {
		event, err := scanProductionEvent(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEvents - scan: %w", err)
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEvents - rows: %w", err)
	}

	return events, total, nil
}

// ListProductionEventsGlobal returns latest production timeline events across projects.
func (r *EditorialRepo) ListProductionEventsGlobal(
	ctx context.Context,
	lang string,
	limit,
	offset uint64,
) ([]entity.BookProductionEvent, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("book_production_events e").
		Join("book_production_projects p ON p.id = e.project_id")
	dataBuilder := r.Builder.
		Select(
			"e.id",
			"e.project_id",
			"e.actor_id",
			"e.event_type",
			"e.asset_type",
			"e.heading_id",
			"e.note",
			"e.payload",
			"e.created_at",
		).
		From("book_production_events e").
		Join("book_production_projects p ON p.id = e.project_id").
		OrderBy("e.created_at DESC", "e.id DESC").
		Limit(limit).
		Offset(offset)

	if lang != "" {
		countBuilder = countBuilder.Where(sq.Eq{"p.lang": lang})
		dataBuilder = dataBuilder.Where(sq.Eq{"p.lang": lang})
	}

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEventsGlobal - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEventsGlobal - builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEventsGlobal - query: %w", err)
	}
	defer rows.Close()

	events := make([]entity.BookProductionEvent, 0, limit)
	for rows.Next() {
		event, scanErr := scanProductionEvent(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEventsGlobal - scan: %w", scanErr)
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionEventsGlobal - rows: %w", err)
	}

	return events, total, nil
}

// ListProductionDraftRevisions returns immutable draft snapshots for one asset.
func (r *EditorialRepo) ListProductionDraftRevisions(
	ctx context.Context,
	filter repo.ProductionDraftRevisionFilter,
) ([]entity.BookProductionDraftRevision, int, error) {
	if _, err := r.GetProductionProject(ctx, filter.ProjectID); err != nil {
		return nil, 0, err
	}

	whereHeading := "heading_id IS NULL"
	args := []any{filter.ProjectID, filter.AssetType}
	if filter.HeadingID != nil {
		whereHeading = "heading_id = $3"
		args = append(args, *filter.HeadingID)
	}

	countSQL := fmt.Sprintf(`
SELECT COUNT(*)
FROM book_production_draft_revisions
WHERE project_id = $1
  AND asset_type = $2
  AND %s`, whereHeading)
	var total int
	if err := r.Pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionDraftRevisions - count: %w", err)
	}

	queryArgs := append(args, filter.Limit, filter.Offset)
	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	dataSQL := fmt.Sprintf(`
SELECT id, project_id, asset_type, heading_id, version, actor_id, snapshot, created_at
FROM book_production_draft_revisions
WHERE project_id = $1
  AND asset_type = $2
  AND %s
ORDER BY version DESC, created_at DESC
LIMIT $%d OFFSET $%d`, whereHeading, limitIndex, offsetIndex)

	rows, err := r.Pool.Query(ctx, dataSQL, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionDraftRevisions - query: %w", err)
	}
	defer rows.Close()

	revisions := make([]entity.BookProductionDraftRevision, 0, filter.Limit)
	for rows.Next() {
		revision, scanErr := scanProductionDraftRevision(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("EditorialRepo - ListProductionDraftRevisions - scan: %w", scanErr)
		}
		revisions = append(revisions, revision)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListProductionDraftRevisions - rows: %w", err)
	}

	return revisions, total, nil
}

// RestoreProductionDraftRevision restores one revision snapshot into the current draft.
func (r *EditorialRepo) RestoreProductionDraftRevision(
	ctx context.Context,
	actorID,
	projectID,
	revisionID string,
) (entity.BookProductionDraftRevision, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.BookProductionDraftRevision{}, fmt.Errorf("EditorialRepo - RestoreProductionDraftRevision - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.BookProductionDraftRevision{}, err
	}

	revision, err := scanProductionDraftRevision(tx.QueryRow(ctx, `
SELECT id, project_id, asset_type, heading_id, version, actor_id, snapshot, created_at
FROM book_production_draft_revisions
WHERE id = $1 AND project_id = $2`, revisionID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionDraftRevision{}, entity.ErrDraftNotFound
		}

		return entity.BookProductionDraftRevision{}, fmt.Errorf("EditorialRepo - RestoreProductionDraftRevision - revision: %w", err)
	}

	if _, _, err = entity.NormalizeProductionDraftTarget(revision.AssetType, revision.HeadingID); err != nil {
		return entity.BookProductionDraftRevision{}, err
	}

	if revision.HeadingID != nil {
		if err = ensureProjectHeadingWithQuerier(ctx, tx, projectID, *revision.HeadingID); err != nil {
			return entity.BookProductionDraftRevision{}, err
		}
	}

	restoredSnapshot, err := restoreProductionDraftSnapshot(ctx, tx, actorID, projectID, revision)
	if err != nil {
		return entity.BookProductionDraftRevision{}, err
	}

	created, err := insertProductionDraftRevision(
		ctx,
		tx,
		actorID,
		projectID,
		revision.AssetType,
		revision.HeadingID,
		restoredSnapshot,
	)
	if err != nil {
		return entity.BookProductionDraftRevision{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.BookProductionDraftRevision{}, fmt.Errorf("EditorialRepo - RestoreProductionDraftRevision - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.BookProductionDraftRevision{}, fmt.Errorf("EditorialRepo - RestoreProductionDraftRevision - commit: %w", err)
	}

	assetType := revision.AssetType
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftRestore, &assetType, revision.HeadingID, nil, map[string]any{
		"revision_id":      revision.ID,
		"restored_version": revision.Version,
		"new_version":      created.Version,
	})

	return created, nil
}

// ProductionPublishCheck explains whether a project can be published.
func (r *EditorialRepo) ProductionPublishCheck(
	ctx context.Context,
	projectID string,
) (entity.BookProductionPublishCheck, error) {
	project, err := r.GetProductionProject(ctx, projectID)
	if err != nil {
		return entity.BookProductionPublishCheck{}, err
	}

	completeness, err := r.productionCompleteness(ctx, r.Pool, project)
	if err != nil {
		return entity.BookProductionPublishCheck{}, err
	}

	return productionPublishCheckFromCompleteness(completeness), nil
}

func (r *EditorialRepo) GetMetadataTranslationDraft(ctx context.Context, projectID string) (entity.BookMetadataTranslationEdit, error) {
	return scanMetadataTranslationEditOrNotFound(r.Pool.QueryRow(ctx, `
SELECT project_id, display_title, bibliography, hint, description, source, metadata, review_status,
       review_note, updated_by, reviewed_by, updated_at, reviewed_at
FROM book_metadata_translation_edits
WHERE project_id = $1`, projectID))
}

func (r *EditorialRepo) SaveMetadataTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.BookMetadataTranslationEdit,
) (entity.BookMetadataTranslationEdit, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.BookMetadataTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveMetadataTranslationDraft - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.BookMetadataTranslationEdit{}, err
	}

	sqlText := `
INSERT INTO book_metadata_translation_edits (
    project_id, display_title, bibliography, hint, description, source, metadata,
    review_status, review_note, updated_by, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::jsonb, 'draft', NULL, $8, now())
ON CONFLICT (project_id) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    description = EXCLUDED.description,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, display_title, bibliography, hint, description, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`

	saved, err := scanMetadataTranslationEditOrNotFound(tx.QueryRow(
		ctx,
		sqlText,
		projectID,
		edit.DisplayTitle,
		edit.Bibliography,
		edit.Hint,
		edit.Description,
		edit.Source,
		jsonString(edit.Metadata),
		actorID,
	))
	if err != nil {
		return entity.BookMetadataTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveMetadataTranslationDraft - scan: %w", err)
	}

	if _, err = insertProductionDraftRevision(ctx, tx, actorID, projectID, entity.ProductionAssetBookMetadata, nil, saved); err != nil {
		return entity.BookMetadataTranslationEdit{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.BookMetadataTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveMetadataTranslationDraft - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.BookMetadataTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveMetadataTranslationDraft - commit: %w", err)
	}

	assetType := entity.ProductionAssetBookMetadata
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftSave, &assetType, nil, nil, map[string]any{
		"review_status": saved.ReviewStatus,
	})

	return saved, nil
}

func (r *EditorialRepo) DeleteMetadataTranslationDraft(ctx context.Context, actorID, projectID string) error {
	return r.deleteProductionDraft(ctx, actorID, projectID, entity.ProductionAssetBookMetadata, nil, "book_metadata_translation_edits", "project_id = $1", projectID)
}

func (r *EditorialRepo) GetAuthorTranslationDraft(ctx context.Context, projectID string) (entity.AuthorTranslationEdit, error) {
	return scanAuthorTranslationEditOrNotFound(r.Pool.QueryRow(ctx, `
SELECT project_id, name, biography, death_text, source, metadata, review_status,
       review_note, updated_by, reviewed_by, updated_at, reviewed_at
FROM author_translation_edits
WHERE project_id = $1`, projectID))
}

func (r *EditorialRepo) SaveAuthorTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.AuthorTranslationEdit,
) (entity.AuthorTranslationEdit, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.AuthorTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveAuthorTranslationDraft - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.AuthorTranslationEdit{}, err
	}

	sqlText := `
INSERT INTO author_translation_edits (
    project_id, name, biography, death_text, source, metadata, review_status, review_note, updated_by, updated_at
)
VALUES ($1, $2, $3, $4, $5, nullif($6, '')::jsonb, 'draft', NULL, $7, now())
ON CONFLICT (project_id) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, name, biography, death_text, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`

	saved, err := scanAuthorTranslationEditOrNotFound(tx.QueryRow(
		ctx,
		sqlText,
		projectID,
		edit.Name,
		edit.Biography,
		edit.DeathText,
		edit.Source,
		jsonString(edit.Metadata),
		actorID,
	))
	if err != nil {
		return entity.AuthorTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveAuthorTranslationDraft - scan: %w", err)
	}

	if _, err = insertProductionDraftRevision(ctx, tx, actorID, projectID, entity.ProductionAssetAuthorMetadata, nil, saved); err != nil {
		return entity.AuthorTranslationEdit{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.AuthorTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveAuthorTranslationDraft - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.AuthorTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveAuthorTranslationDraft - commit: %w", err)
	}

	assetType := entity.ProductionAssetAuthorMetadata
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftSave, &assetType, nil, nil, map[string]any{
		"review_status": saved.ReviewStatus,
	})

	return saved, nil
}

func (r *EditorialRepo) DeleteAuthorTranslationDraft(ctx context.Context, actorID, projectID string) error {
	return r.deleteProductionDraft(ctx, actorID, projectID, entity.ProductionAssetAuthorMetadata, nil, "author_translation_edits", "project_id = $1", projectID)
}

func (r *EditorialRepo) GetCategoryTranslationDraft(ctx context.Context, projectID string) (entity.CategoryTranslationEdit, error) {
	return scanCategoryTranslationEditOrNotFound(r.Pool.QueryRow(ctx, `
SELECT project_id, name, source, metadata, review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
FROM category_translation_edits
WHERE project_id = $1`, projectID))
}

func (r *EditorialRepo) SaveCategoryTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.CategoryTranslationEdit,
) (entity.CategoryTranslationEdit, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.CategoryTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveCategoryTranslationDraft - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.CategoryTranslationEdit{}, err
	}

	sqlText := `
INSERT INTO category_translation_edits (
    project_id, name, source, metadata, review_status, review_note, updated_by, updated_at
)
VALUES ($1, $2, $3, nullif($4, '')::jsonb, 'draft', NULL, $5, now())
ON CONFLICT (project_id) DO UPDATE SET
    name = EXCLUDED.name,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, name, source, metadata, review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at`

	saved, err := scanCategoryTranslationEditOrNotFound(tx.QueryRow(
		ctx,
		sqlText,
		projectID,
		edit.Name,
		edit.Source,
		jsonString(edit.Metadata),
		actorID,
	))
	if err != nil {
		return entity.CategoryTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveCategoryTranslationDraft - scan: %w", err)
	}

	if _, err = insertProductionDraftRevision(ctx, tx, actorID, projectID, entity.ProductionAssetCategoryMetadata, nil, saved); err != nil {
		return entity.CategoryTranslationEdit{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.CategoryTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveCategoryTranslationDraft - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.CategoryTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveCategoryTranslationDraft - commit: %w", err)
	}

	assetType := entity.ProductionAssetCategoryMetadata
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftSave, &assetType, nil, nil, map[string]any{
		"review_status": saved.ReviewStatus,
	})

	return saved, nil
}

func (r *EditorialRepo) DeleteCategoryTranslationDraft(ctx context.Context, actorID, projectID string) error {
	return r.deleteProductionDraft(ctx, actorID, projectID, entity.ProductionAssetCategoryMetadata, nil, "category_translation_edits", "project_id = $1", projectID)
}

func (r *EditorialRepo) GetSectionTranslationDraft(
	ctx context.Context,
	projectID string,
	headingID int,
) (entity.SectionTranslationEdit, error) {
	return scanSectionTranslationEditOrNotFound(r.Pool.QueryRow(ctx, `
SELECT project_id, heading_id, title, content, source, metadata, review_status,
       review_note, updated_by, reviewed_by, updated_at, reviewed_at
FROM section_translation_edits
WHERE project_id = $1 AND heading_id = $2`, projectID, headingID))
}

func (r *EditorialRepo) SaveSectionTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.SectionTranslationEdit,
) (entity.SectionTranslationEdit, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.SectionTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveSectionTranslationDraft - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.SectionTranslationEdit{}, err
	}
	if err = ensureProjectHeadingWithQuerier(ctx, tx, projectID, edit.HeadingID); err != nil {
		return entity.SectionTranslationEdit{}, err
	}

	sqlText := `
INSERT INTO section_translation_edits (
    project_id, heading_id, title, content, source, metadata, review_status, review_note, updated_by, updated_at
)
VALUES ($1, $2, $3, $4, $5, nullif($6, '')::jsonb, 'draft', NULL, $7, now())
ON CONFLICT (project_id, heading_id) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, heading_id, title, content, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`

	saved, err := scanSectionTranslationEditOrNotFound(tx.QueryRow(
		ctx,
		sqlText,
		projectID,
		edit.HeadingID,
		edit.Title,
		edit.Content,
		edit.Source,
		jsonString(edit.Metadata),
		actorID,
	))
	if err != nil {
		return entity.SectionTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveSectionTranslationDraft - scan: %w", err)
	}

	headingID := saved.HeadingID
	if _, err = insertProductionDraftRevision(ctx, tx, actorID, projectID, entity.ProductionAssetSectionTranslation, &headingID, saved); err != nil {
		return entity.SectionTranslationEdit{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.SectionTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveSectionTranslationDraft - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.SectionTranslationEdit{}, fmt.Errorf("EditorialRepo - SaveSectionTranslationDraft - commit: %w", err)
	}

	assetType := entity.ProductionAssetSectionTranslation
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftSave, &assetType, &headingID, nil, map[string]any{
		"review_status": saved.ReviewStatus,
	})

	return saved, nil
}

func (r *EditorialRepo) DeleteSectionTranslationDraft(ctx context.Context, actorID, projectID string, headingID int) error {
	return r.deleteProductionDraft(ctx, actorID, projectID, entity.ProductionAssetSectionTranslation, &headingID, "section_translation_edits", "project_id = $1 AND heading_id = $2", projectID, headingID)
}

func (r *EditorialRepo) GetHeadingSummaryDraft(
	ctx context.Context,
	projectID string,
	headingID int,
) (entity.HeadingSummaryEdit, error) {
	return scanHeadingSummaryEditOrNotFound(r.Pool.QueryRow(ctx, `
SELECT project_id, heading_id, summary, source, metadata, review_status,
       review_note, updated_by, reviewed_by, updated_at, reviewed_at
FROM heading_summary_edits
WHERE project_id = $1 AND heading_id = $2`, projectID, headingID))
}

func (r *EditorialRepo) SaveHeadingSummaryDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.HeadingSummaryEdit,
) (entity.HeadingSummaryEdit, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.HeadingSummaryEdit{}, fmt.Errorf("EditorialRepo - SaveHeadingSummaryDraft - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.HeadingSummaryEdit{}, err
	}
	if err = ensureProjectHeadingWithQuerier(ctx, tx, projectID, edit.HeadingID); err != nil {
		return entity.HeadingSummaryEdit{}, err
	}

	sqlText := `
INSERT INTO heading_summary_edits (
    project_id, heading_id, summary, source, metadata, review_status, review_note, updated_by, updated_at
)
VALUES ($1, $2, $3, $4, nullif($5, '')::jsonb, 'draft', NULL, $6, now())
ON CONFLICT (project_id, heading_id) DO UPDATE SET
    summary = EXCLUDED.summary,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, heading_id, summary, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`

	saved, err := scanHeadingSummaryEditOrNotFound(tx.QueryRow(
		ctx,
		sqlText,
		projectID,
		edit.HeadingID,
		edit.Summary,
		edit.Source,
		jsonString(edit.Metadata),
		actorID,
	))
	if err != nil {
		return entity.HeadingSummaryEdit{}, fmt.Errorf("EditorialRepo - SaveHeadingSummaryDraft - scan: %w", err)
	}

	headingID := saved.HeadingID
	if _, err = insertProductionDraftRevision(ctx, tx, actorID, projectID, entity.ProductionAssetHeadingSummary, &headingID, saved); err != nil {
		return entity.HeadingSummaryEdit{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.HeadingSummaryEdit{}, fmt.Errorf("EditorialRepo - SaveHeadingSummaryDraft - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.HeadingSummaryEdit{}, fmt.Errorf("EditorialRepo - SaveHeadingSummaryDraft - commit: %w", err)
	}

	assetType := entity.ProductionAssetHeadingSummary
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftSave, &assetType, &headingID, nil, map[string]any{
		"review_status": saved.ReviewStatus,
	})

	return saved, nil
}

func (r *EditorialRepo) DeleteHeadingSummaryDraft(ctx context.Context, actorID, projectID string, headingID int) error {
	return r.deleteProductionDraft(ctx, actorID, projectID, entity.ProductionAssetHeadingSummary, &headingID, "heading_summary_edits", "project_id = $1 AND heading_id = $2", projectID, headingID)
}

func (r *EditorialRepo) GetSectionAudioDraft(
	ctx context.Context,
	projectID string,
	headingID int,
) (entity.SectionAudioEdit, error) {
	return scanSectionAudioEditOrNotFound(r.Pool.QueryRow(ctx, `
SELECT project_id, heading_id, url, narrator, duration_seconds, mime_type, metadata, review_status,
       review_note, updated_by, reviewed_by, updated_at, reviewed_at
FROM section_audio_edits
WHERE project_id = $1 AND heading_id = $2`, projectID, headingID))
}

func (r *EditorialRepo) SaveSectionAudioDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.SectionAudioEdit,
) (entity.SectionAudioEdit, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.SectionAudioEdit{}, fmt.Errorf("EditorialRepo - SaveSectionAudioDraft - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err = lockProductionProject(ctx, tx, projectID); err != nil {
		return entity.SectionAudioEdit{}, err
	}
	if err = ensureProjectHeadingWithQuerier(ctx, tx, projectID, edit.HeadingID); err != nil {
		return entity.SectionAudioEdit{}, err
	}

	sqlText := `
INSERT INTO section_audio_edits (
    project_id, heading_id, url, narrator, duration_seconds, mime_type, metadata,
    review_status, review_note, updated_by, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::jsonb, 'draft', NULL, $8, now())
ON CONFLICT (project_id, heading_id) DO UPDATE SET
    url = EXCLUDED.url,
    narrator = EXCLUDED.narrator,
    duration_seconds = EXCLUDED.duration_seconds,
    mime_type = EXCLUDED.mime_type,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, heading_id, url, narrator, duration_seconds, mime_type, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`

	saved, err := scanSectionAudioEditOrNotFound(tx.QueryRow(
		ctx,
		sqlText,
		projectID,
		edit.HeadingID,
		edit.URL,
		edit.Narrator,
		edit.DurationSeconds,
		edit.MIMEType,
		jsonString(edit.Metadata),
		actorID,
	))
	if err != nil {
		return entity.SectionAudioEdit{}, fmt.Errorf("EditorialRepo - SaveSectionAudioDraft - scan: %w", err)
	}

	headingID := saved.HeadingID
	if _, err = insertProductionDraftRevision(ctx, tx, actorID, projectID, entity.ProductionAssetSectionAudio, &headingID, saved); err != nil {
		return entity.SectionAudioEdit{}, err
	}

	if err = touchProductionProjectTx(ctx, tx, actorID, projectID, entity.ProductionWorkflowDrafting); err != nil {
		return entity.SectionAudioEdit{}, fmt.Errorf("EditorialRepo - SaveSectionAudioDraft - touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.SectionAudioEdit{}, fmt.Errorf("EditorialRepo - SaveSectionAudioDraft - commit: %w", err)
	}

	assetType := entity.ProductionAssetSectionAudio
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftSave, &assetType, &headingID, nil, map[string]any{
		"review_status": saved.ReviewStatus,
	})

	return saved, nil
}

func (r *EditorialRepo) DeleteSectionAudioDraft(ctx context.Context, actorID, projectID string, headingID int) error {
	return r.deleteProductionDraft(ctx, actorID, projectID, entity.ProductionAssetSectionAudio, &headingID, "section_audio_edits", "project_id = $1 AND heading_id = $2", projectID, headingID)
}

// ReviewProductionAsset changes review status for one draft asset.
func (r *EditorialRepo) ReviewProductionAsset(
	ctx context.Context,
	actorID,
	projectID,
	assetType string,
	headingID *int,
	decision string,
	note *string,
) error {
	table, where, args := productionDraftReviewTarget(projectID, assetType, headingID)
	status := reviewStatusForDecision(decision)
	if table == "" {
		return entity.ErrInvalidAssetType
	}

	sqlText := fmt.Sprintf(`
UPDATE %s
SET review_status = $%d,
    review_note = $%d,
    reviewed_by = CASE WHEN $%d IN ('approved', 'rejected') THEN $%d::uuid ELSE NULL::uuid END,
    reviewed_at = CASE WHEN $%d IN ('approved', 'rejected') THEN now() ELSE NULL END,
    updated_at = now()
WHERE %s`, table, len(args)+1, len(args)+2, len(args)+1, len(args)+3, len(args)+1, where)
	args = append(args, status, note, actorID)

	result, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("EditorialRepo - ReviewProductionAsset - exec: %w", err)
	}
	if result.RowsAffected() == 0 {
		return entity.ErrDraftNotFound
	}

	if decision == entity.ProductionReviewDecisionSubmit {
		_ = r.touchProductionProject(ctx, actorID, projectID, entity.ProductionWorkflowInReview)
	} else {
		_ = r.touchProductionProject(ctx, actorID, projectID, "")
	}

	_ = r.audit(ctx, actorID, "production_asset.review", 0, nil, headingID, assetType, map[string]any{
		"project_id": projectID,
		"decision":   decision,
	})
	assetTypeCopy := assetType
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventReview, &assetTypeCopy, headingID, note, map[string]any{
		"decision":      decision,
		"review_status": status,
	})

	return nil
}

// PublishProductionProject promotes required drafts into final reader tables.
func (r *EditorialRepo) PublishProductionProject(
	ctx context.Context,
	actorID,
	projectID string,
) (entity.BookProductionProject, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - PublishProductionProject - begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	project, err := scanProductionProject(tx.QueryRow(ctx, `
SELECT id, book_id, lang, workflow_status, publication_status, requires_review,
       requires_audio, priority, owner_id, notes, created_by, updated_by, published_by,
       created_at, updated_at, published_at, archived_at
FROM book_production_projects
WHERE id = $1
FOR UPDATE`, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
		}

		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - PublishProductionProject - lock: %w", err)
	}

	completeness, err := r.productionCompleteness(ctx, tx, project)
	if err != nil {
		return entity.BookProductionProject{}, err
	}
	if !completeness.Ready {
		return entity.BookProductionProject{}, entity.ErrProductionNotReady
	}

	if err = publishProductionAssets(ctx, tx, actorID, projectID); err != nil {
		return entity.BookProductionProject{}, err
	}

	published, err := scanProductionProject(tx.QueryRow(ctx, `
UPDATE book_production_projects
SET workflow_status = 'published',
    publication_status = 'published',
    published_by = $2,
    published_at = now(),
    updated_by = $2,
    updated_at = now()
WHERE id = $1
RETURNING id, book_id, lang, workflow_status, publication_status, requires_review,
          requires_audio, priority, owner_id, notes, created_by, updated_by, published_by,
          created_at, updated_at, published_at, archived_at`, projectID, actorID))
	if err != nil {
		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - PublishProductionProject - update project: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - PublishProductionProject - commit: %w", err)
	}

	_ = r.audit(ctx, actorID, "production_project.publish", published.BookID, nil, nil, published.Lang, published)
	_ = r.recordProductionEvent(ctx, actorID, published.ID, entity.ProductionEventProjectPublish, nil, nil, nil, published)

	return published, nil
}

// UnpublishProductionProject hides a project language without deleting final assets.
func (r *EditorialRepo) UnpublishProductionProject(
	ctx context.Context,
	actorID,
	projectID string,
) (entity.BookProductionProject, error) {
	project, err := scanProductionProject(r.Pool.QueryRow(ctx, `
UPDATE book_production_projects
SET publication_status = 'hidden',
    workflow_status = CASE WHEN workflow_status = 'published' THEN 'ready' ELSE workflow_status END,
    updated_by = $2,
    updated_at = now()
WHERE id = $1
RETURNING id, book_id, lang, workflow_status, publication_status, requires_review,
          requires_audio, priority, owner_id, notes, created_by, updated_by, published_by,
          created_at, updated_at, published_at, archived_at`, projectID, actorID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
		}

		return entity.BookProductionProject{}, fmt.Errorf("EditorialRepo - UnpublishProductionProject - scan: %w", err)
	}

	_ = r.audit(ctx, actorID, "production_project.unpublish", project.BookID, nil, nil, project.Lang, project)
	_ = r.recordProductionEvent(ctx, actorID, project.ID, entity.ProductionEventProjectUnpublish, nil, nil, nil, project)

	return project, nil
}

// DeleteFinalProductionAsset soft-deletes one final published asset and hides the project language.
func (r *EditorialRepo) DeleteFinalProductionAsset(
	ctx context.Context,
	actorID,
	projectID,
	assetType string,
	headingID *int,
	reason *string,
) error {
	project, err := r.GetProductionProject(ctx, projectID)
	if err != nil {
		return err
	}

	sqlText, args, err := finalAssetDeleteSQL(project, actorID, assetType, headingID, reason)
	if err != nil {
		return err
	}

	result, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("EditorialRepo - DeleteFinalProductionAsset - exec: %w", err)
	}
	if result.RowsAffected() == 0 {
		return entity.ErrTranslationNotFound
	}

	_, _ = r.Pool.Exec(ctx, `
UPDATE book_production_projects
SET publication_status = 'hidden',
    workflow_status = CASE WHEN workflow_status = 'published' THEN 'drafting' ELSE workflow_status END,
    updated_by = $2,
    updated_at = now()
WHERE id = $1`, projectID, actorID)

	_ = r.audit(ctx, actorID, "production_asset.final_delete", project.BookID, nil, headingID, assetType, map[string]any{
		"project_id": projectID,
		"reason":     reason,
	})
	assetTypeCopy := assetType
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventFinalDelete, &assetTypeCopy, headingID, reason, nil)

	return nil
}

func (r *EditorialRepo) ensureProductionSourceReady(ctx context.Context, bookID int) error {
	var hasContent bool
	var headingCount int
	err := r.Pool.QueryRow(ctx, `
SELECT b.has_content, COUNT(h.heading_id)
FROM books b
LEFT JOIN book_headings h ON h.book_id = b.id AND h.is_deleted = false
WHERE b.id = $1 AND b.is_deleted = false
GROUP BY b.id, b.has_content`, bookID).Scan(&hasContent, &headingCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ErrBookNotFound
		}

		return fmt.Errorf("EditorialRepo - ensureProductionSourceReady - query: %w", err)
	}
	if !hasContent || headingCount == 0 {
		return entity.ErrProductionNotReady
	}

	return nil
}

func (r *EditorialRepo) productionCompleteness(
	ctx context.Context,
	q productionQuerier,
	project entity.BookProductionProject,
) (entity.BookProductionCompleteness, error) {
	missing, required, err := productionMissingAssets(ctx, q, project)
	if err != nil {
		return entity.BookProductionCompleteness{}, err
	}

	return entity.BookProductionCompleteness{
		Project:       project,
		Ready:         len(missing) == 0,
		RequiredCount: required,
		CompleteCount: required - len(missing),
		MissingCount:  len(missing),
		Missing:       missing,
	}, nil
}

func productionPublishCheckFromCompleteness(
	completeness entity.BookProductionCompleteness,
) entity.BookProductionPublishCheck {
	blocking := make([]entity.BookProductionBlocking, 0, len(completeness.Missing))
	for _, missing := range completeness.Missing {
		blocking = append(blocking, entity.BookProductionBlocking{
			Code:      "missing_required_asset",
			AssetType: missing.AssetType,
			HeadingID: missing.HeadingID,
			Message:   missing.Message,
		})
	}

	return entity.BookProductionPublishCheck{
		Project:        completeness.Project,
		Ready:          completeness.Ready,
		CanPublish:     completeness.Ready,
		RequiredCount:  completeness.RequiredCount,
		CompleteCount:  completeness.CompleteCount,
		MissingCount:   completeness.MissingCount,
		Missing:        completeness.Missing,
		BlockingErrors: blocking,
	}
}

func productionMissingAssets(
	ctx context.Context,
	q productionQuerier,
	project entity.BookProductionProject,
) ([]entity.BookProductionMissingAsset, int, error) {
	var hasContent bool
	var hasAuthor bool
	var hasCategory bool
	var headingCount int
	err := q.QueryRow(ctx, `
SELECT b.has_content,
       b.author_id IS NOT NULL,
       COALESCE(me.category_id, b.category_id) IS NOT NULL,
       COUNT(h.heading_id)
FROM books b
LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN book_headings h ON h.book_id = b.id AND h.is_deleted = false
WHERE b.id = $1 AND b.is_deleted = false
GROUP BY b.id, b.has_content, b.author_id, COALESCE(me.category_id, b.category_id)`, project.BookID).
		Scan(&hasContent, &hasAuthor, &hasCategory, &headingCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, entity.ErrBookNotFound
		}

		return nil, 0, fmt.Errorf("production completeness source query: %w", err)
	}

	missing := make([]entity.BookProductionMissingAsset, 0)
	required := 0
	if !hasContent || headingCount == 0 {
		return []entity.BookProductionMissingAsset{{
			AssetType: entity.ProductionAssetSectionTranslation,
			Message:   "book content/headings are not imported",
		}}, 1, nil
	}

	statusSQL := "review_status <> 'rejected'"
	if project.RequiresReview {
		statusSQL = "review_status = 'approved'"
	}

	required++
	if !productionScalarExists(ctx, q, "book_metadata_translation_edits", project.ID, "NULLIF(BTRIM(display_title), '') IS NOT NULL", statusSQL) {
		missing = append(missing, entity.BookProductionMissingAsset{
			AssetType: entity.ProductionAssetBookMetadata,
			Message:   "metadata translation draft is missing",
		})
	}
	if hasAuthor {
		required++
		if !productionScalarExists(ctx, q, "author_translation_edits", project.ID, "NULLIF(BTRIM(name), '') IS NOT NULL", statusSQL) {
			missing = append(missing, entity.BookProductionMissingAsset{
				AssetType: entity.ProductionAssetAuthorMetadata,
				Message:   "author translation draft is missing",
			})
		}
	}
	if hasCategory {
		required++
		if !productionScalarExists(ctx, q, "category_translation_edits", project.ID, "NULLIF(BTRIM(name), '') IS NOT NULL", statusSQL) {
			missing = append(missing, entity.BookProductionMissingAsset{
				AssetType: entity.ProductionAssetCategoryMetadata,
				Message:   "category translation draft is missing",
			})
		}
	}

	headingRequirements := []struct {
		assetType string
		table     string
		field     string
		message   string
	}{
		{entity.ProductionAssetSectionTranslation, "section_translation_edits", "content", "section translation draft is missing"},
		{entity.ProductionAssetHeadingSummary, "heading_summary_edits", "summary", "heading summary draft is missing"},
	}
	if project.RequiresAudio {
		headingRequirements = append(headingRequirements, struct {
			assetType string
			table     string
			field     string
			message   string
		}{entity.ProductionAssetSectionAudio, "section_audio_edits", "url", "section audio draft is missing"})
	}

	for _, req := range headingRequirements {
		required += headingCount
		missingHeadings, err := productionMissingHeadings(ctx, q, project, req.table, req.field, statusSQL)
		if err != nil {
			return nil, 0, err
		}
		for _, headingID := range missingHeadings {
			id := headingID
			missing = append(missing, entity.BookProductionMissingAsset{
				AssetType: req.assetType,
				HeadingID: &id,
				Message:   req.message,
			})
		}
	}

	return missing, required, nil
}

func productionScalarExists(
	ctx context.Context,
	q productionQuerier,
	table,
	projectID,
	fieldCondition,
	statusCondition string,
) bool {
	sqlText := fmt.Sprintf(
		"SELECT EXISTS (SELECT 1 FROM %s WHERE project_id = $1 AND %s AND %s)",
		table,
		fieldCondition,
		statusCondition,
	)

	var exists bool
	if err := q.QueryRow(ctx, sqlText, projectID).Scan(&exists); err != nil {
		return false
	}

	return exists
}

func productionMissingHeadings(
	ctx context.Context,
	q productionQuerier,
	project entity.BookProductionProject,
	table,
	field,
	statusCondition string,
) ([]int, error) {
	sqlText := fmt.Sprintf(`
SELECT h.heading_id
FROM book_headings h
WHERE h.book_id = $1
  AND h.is_deleted = false
  AND NOT EXISTS (
      SELECT 1
      FROM %s e
      WHERE e.project_id = $2
        AND e.heading_id = h.heading_id
        AND NULLIF(BTRIM(e.%s), '') IS NOT NULL
        AND %s
  )
ORDER BY h.ordinal ASC, h.heading_id ASC`, table, field, statusCondition)

	rows, err := q.Query(ctx, sqlText, project.BookID, project.ID)
	if err != nil {
		return nil, fmt.Errorf("production missing headings query: %w", err)
	}
	defer rows.Close()

	ids := make([]int, 0)
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("production missing headings scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("production missing headings rows: %w", err)
	}

	return ids, nil
}

func publishProductionAssets(ctx context.Context, tx pgx.Tx, actorID, projectID string) error {
	statements := []string{
		`
INSERT INTO book_metadata_translations (
    book_id, lang, display_title, bibliography, hint, description, source, metadata,
    translation_status, reviewed_by, reviewed_at, is_deleted, deleted_by, deleted_at, delete_reason, updated_at
)
SELECT p.book_id, p.lang, e.display_title, e.bibliography, e.hint, e.description, e.source, e.metadata,
       CASE WHEN e.review_status = 'approved' THEN 'reviewed' ELSE 'generated' END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_by::TEXT, $2) ELSE NULL END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_at, now()) ELSE NULL END,
       false, NULL, NULL, NULL, now()
FROM book_production_projects p
JOIN book_metadata_translation_edits e ON e.project_id = p.id
WHERE p.id = $1
ON CONFLICT (book_id, lang) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    description = EXCLUDED.description,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
		`
INSERT INTO author_translations (
    author_id, lang, name, biography, death_text, source, metadata,
    translation_status, reviewed_by, reviewed_at, is_deleted, deleted_by, deleted_at, delete_reason, updated_at
)
SELECT b.author_id, p.lang, e.name, e.biography, e.death_text, e.source, e.metadata,
       CASE WHEN e.review_status = 'approved' THEN 'reviewed' ELSE 'generated' END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_by::TEXT, $2) ELSE NULL END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_at, now()) ELSE NULL END,
       false, NULL, NULL, NULL, now()
FROM book_production_projects p
JOIN books b ON b.id = p.book_id
JOIN author_translation_edits e ON e.project_id = p.id
WHERE p.id = $1 AND b.author_id IS NOT NULL
ON CONFLICT (author_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
		`
INSERT INTO category_translations (
    category_id, lang, name, source, metadata,
    translation_status, reviewed_by, reviewed_at, is_deleted, deleted_by, deleted_at, delete_reason, updated_at
)
SELECT COALESCE(me.category_id, b.category_id), p.lang, e.name, e.source, e.metadata,
       CASE WHEN e.review_status = 'approved' THEN 'reviewed' ELSE 'generated' END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_by::TEXT, $2) ELSE NULL END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_at, now()) ELSE NULL END,
       false, NULL, NULL, NULL, now()
FROM book_production_projects p
JOIN books b ON b.id = p.book_id
LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
JOIN category_translation_edits e ON e.project_id = p.id
WHERE p.id = $1 AND COALESCE(me.category_id, b.category_id) IS NOT NULL
ON CONFLICT (category_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
		`
INSERT INTO section_translations (
    book_id, heading_id, lang, title, content, source, metadata,
    translation_status, reviewed_by, reviewed_at, is_deleted, deleted_by, deleted_at, delete_reason, updated_at
)
SELECT p.book_id, e.heading_id, p.lang, e.title, e.content, e.source, e.metadata,
       CASE WHEN e.review_status = 'approved' THEN 'reviewed' ELSE 'generated' END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_by::TEXT, $2) ELSE NULL END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_at, now()) ELSE NULL END,
       false, NULL, NULL, NULL, now()
FROM book_production_projects p
JOIN section_translation_edits e ON e.project_id = p.id
WHERE p.id = $1
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
		`
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, source, summary_status, reviewed_by, reviewed_at,
    metadata, is_deleted, deleted_by, deleted_at, delete_reason, updated_at
)
SELECT p.book_id, e.heading_id, p.lang, e.summary, e.source,
       CASE WHEN e.review_status = 'approved' THEN 'reviewed' ELSE 'generated' END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_by::TEXT, $2) ELSE NULL END,
       CASE WHEN e.review_status = 'approved' THEN COALESCE(e.reviewed_at, now()) ELSE NULL END,
       e.metadata, false, NULL, NULL, NULL, now()
FROM book_production_projects p
JOIN heading_summary_edits e ON e.project_id = p.id
WHERE p.id = $1
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    summary = EXCLUDED.summary,
    source = EXCLUDED.source,
    summary_status = EXCLUDED.summary_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
		`
INSERT INTO section_audio (
    book_id, heading_id, lang, url, narrator, duration_seconds, mime_type, metadata,
    is_deleted, deleted_by, deleted_at, delete_reason, updated_at
)
SELECT p.book_id, e.heading_id, p.lang, e.url, e.narrator, e.duration_seconds, e.mime_type, e.metadata,
       false, NULL, NULL, NULL, now()
FROM book_production_projects p
JOIN section_audio_edits e ON e.project_id = p.id
WHERE p.id = $1 AND $2::uuid IS NOT NULL
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    url = EXCLUDED.url,
    narrator = EXCLUDED.narrator,
    duration_seconds = EXCLUDED.duration_seconds,
    mime_type = EXCLUDED.mime_type,
    metadata = EXCLUDED.metadata,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
	}

	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, projectID, actorID); err != nil {
			return fmt.Errorf("publish production asset: %w", err)
		}
	}

	return nil
}

func productionProjectSelectBuilder(builder sq.StatementBuilderType) sq.SelectBuilder {
	return builder.
		Select(
			"p.id",
			"p.book_id",
			"p.lang",
			"p.workflow_status",
			"p.publication_status",
			"p.requires_review",
			"p.requires_audio",
			"p.priority",
			"p.owner_id",
			"p.notes",
			"p.created_by",
			"p.updated_by",
			"p.published_by",
			"p.created_at",
			"p.updated_at",
			"p.published_at",
			"p.archived_at",
		).
		From("book_production_projects p")
}

func applyProductionProjectFilter(
	countBuilder,
	dataBuilder sq.SelectBuilder,
	filter repo.ProductionProjectFilter,
) (sq.SelectBuilder, sq.SelectBuilder) {
	if filter.BookID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"p.book_id": *filter.BookID})
		dataBuilder = dataBuilder.Where(sq.Eq{"p.book_id": *filter.BookID})
	}
	if filter.Lang != "" {
		countBuilder = countBuilder.Where(sq.Eq{"p.lang": filter.Lang})
		dataBuilder = dataBuilder.Where(sq.Eq{"p.lang": filter.Lang})
	}
	if filter.WorkflowStatus != "" {
		countBuilder = countBuilder.Where(sq.Eq{"p.workflow_status": filter.WorkflowStatus})
		dataBuilder = dataBuilder.Where(sq.Eq{"p.workflow_status": filter.WorkflowStatus})
	}
	if filter.PublicationStatus != "" {
		countBuilder = countBuilder.Where(sq.Eq{"p.publication_status": filter.PublicationStatus})
		dataBuilder = dataBuilder.Where(sq.Eq{"p.publication_status": filter.PublicationStatus})
	}
	if filter.ReadyToPublish || filter.NeedsWork {
		baseCondition := "p.workflow_status <> 'archived' AND p.publication_status <> 'published'"
		readyCondition := productionProjectReadySQL()
		if filter.ReadyToPublish {
			countBuilder = countBuilder.Where(baseCondition).Where(readyCondition)
			dataBuilder = dataBuilder.Where(baseCondition).Where(readyCondition)
		}
		if filter.NeedsWork {
			countBuilder = countBuilder.Where(baseCondition).Where("NOT (" + readyCondition + ")")
			dataBuilder = dataBuilder.Where(baseCondition).Where("NOT (" + readyCondition + ")")
		}
	}

	return countBuilder, dataBuilder
}

func productionProjectReadySQL() string {
	return `
EXISTS (
    SELECT 1
    FROM books b
    WHERE b.id = p.book_id
      AND b.is_deleted = false
      AND b.has_content = true
)
AND EXISTS (
    SELECT 1
    FROM book_headings h
    WHERE h.book_id = p.book_id
      AND h.is_deleted = false
)
AND EXISTS (
    SELECT 1
    FROM book_metadata_translation_edits e
    WHERE e.project_id = p.id
      AND NULLIF(BTRIM(e.display_title), '') IS NOT NULL
      AND (
          (p.requires_review = true AND e.review_status = 'approved')
          OR (p.requires_review = false AND e.review_status <> 'rejected')
      )
)
AND (
    NOT EXISTS (
        SELECT 1
        FROM books b
        WHERE b.id = p.book_id
          AND b.author_id IS NOT NULL
    )
    OR EXISTS (
        SELECT 1
        FROM author_translation_edits e
        WHERE e.project_id = p.id
          AND NULLIF(BTRIM(e.name), '') IS NOT NULL
          AND (
              (p.requires_review = true AND e.review_status = 'approved')
              OR (p.requires_review = false AND e.review_status <> 'rejected')
          )
    )
)
AND (
    NOT EXISTS (
        SELECT 1
        FROM books b
        LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
        WHERE b.id = p.book_id
          AND COALESCE(me.category_id, b.category_id) IS NOT NULL
    )
    OR EXISTS (
        SELECT 1
        FROM category_translation_edits e
        WHERE e.project_id = p.id
          AND NULLIF(BTRIM(e.name), '') IS NOT NULL
          AND (
              (p.requires_review = true AND e.review_status = 'approved')
              OR (p.requires_review = false AND e.review_status <> 'rejected')
          )
    )
)
AND NOT EXISTS (
    SELECT 1
    FROM book_headings h
    WHERE h.book_id = p.book_id
      AND h.is_deleted = false
      AND NOT EXISTS (
          SELECT 1
          FROM section_translation_edits e
          WHERE e.project_id = p.id
            AND e.heading_id = h.heading_id
            AND NULLIF(BTRIM(e.content), '') IS NOT NULL
            AND (
                (p.requires_review = true AND e.review_status = 'approved')
                OR (p.requires_review = false AND e.review_status <> 'rejected')
            )
      )
)
AND NOT EXISTS (
    SELECT 1
    FROM book_headings h
    WHERE h.book_id = p.book_id
      AND h.is_deleted = false
      AND NOT EXISTS (
          SELECT 1
          FROM heading_summary_edits e
          WHERE e.project_id = p.id
            AND e.heading_id = h.heading_id
            AND NULLIF(BTRIM(e.summary), '') IS NOT NULL
            AND (
                (p.requires_review = true AND e.review_status = 'approved')
                OR (p.requires_review = false AND e.review_status <> 'rejected')
            )
      )
)
AND (
    p.requires_audio = false
    OR NOT EXISTS (
        SELECT 1
        FROM book_headings h
        WHERE h.book_id = p.book_id
          AND h.is_deleted = false
          AND NOT EXISTS (
              SELECT 1
              FROM section_audio_edits e
              WHERE e.project_id = p.id
                AND e.heading_id = h.heading_id
                AND NULLIF(BTRIM(e.url), '') IS NOT NULL
                AND (
                    (p.requires_review = true AND e.review_status = 'approved')
                    OR (p.requires_review = false AND e.review_status <> 'rejected')
                )
          )
    )
)`
}

func (r *EditorialRepo) ensureProjectHeading(ctx context.Context, projectID string, headingID int) error {
	return ensureProjectHeadingWithQuerier(ctx, r.Pool, projectID, headingID)
}

func ensureProjectHeadingWithQuerier(
	ctx context.Context,
	querier interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	projectID string,
	headingID int,
) error {
	var exists bool
	err := querier.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM book_production_projects p
    JOIN book_headings h ON h.book_id = p.book_id AND h.heading_id = $2 AND h.is_deleted = false
    WHERE p.id = $1
)`, projectID, headingID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("EditorialRepo - ensureProjectHeading - query: %w", err)
	}
	if !exists {
		return entity.ErrHeadingNotFound
	}

	return nil
}

func (r *EditorialRepo) deleteProductionDraft(
	ctx context.Context,
	actorID,
	projectID,
	assetType string,
	headingID *int,
	table,
	where string,
	args ...any,
) error {
	result, err := r.Pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", table, where), args...)
	if err != nil {
		return fmt.Errorf("delete production draft: %w", err)
	}
	if result.RowsAffected() == 0 {
		return entity.ErrDraftNotFound
	}

	_ = r.touchProductionProject(ctx, actorID, projectID, entity.ProductionWorkflowDrafting)
	_ = r.audit(ctx, actorID, "production_asset.draft_delete", 0, nil, headingID, assetType, map[string]any{
		"project_id": projectID,
	})
	assetTypeCopy := assetType
	_ = r.recordProductionEvent(ctx, actorID, projectID, entity.ProductionEventDraftDelete, &assetTypeCopy, headingID, nil, nil)

	return nil
}

func (r *EditorialRepo) touchProductionProject(ctx context.Context, actorID, projectID, workflowStatus string) error {
	if workflowStatus == "" {
		_, err := r.Pool.Exec(ctx, `
UPDATE book_production_projects
SET updated_by = $2, updated_at = now()
WHERE id = $1`, projectID, actorID)
		return err
	}

	_, err := r.Pool.Exec(ctx, `
UPDATE book_production_projects
SET workflow_status = CASE
        WHEN workflow_status IN ('published', 'archived') THEN workflow_status
        ELSE $3
    END,
    publication_status = CASE
        WHEN workflow_status = 'published' THEN publication_status
        ELSE 'hidden'
    END,
    updated_by = $2,
    updated_at = now()
WHERE id = $1`, projectID, actorID, workflowStatus)

	return err
}

func productionDraftReviewTarget(projectID, assetType string, headingID *int) (string, string, []any) {
	switch assetType {
	case entity.ProductionAssetBookMetadata:
		return "book_metadata_translation_edits", "project_id = $1", []any{projectID}
	case entity.ProductionAssetAuthorMetadata:
		return "author_translation_edits", "project_id = $1", []any{projectID}
	case entity.ProductionAssetCategoryMetadata:
		return "category_translation_edits", "project_id = $1", []any{projectID}
	case entity.ProductionAssetSectionTranslation:
		return "section_translation_edits", "project_id = $1 AND heading_id = $2", []any{projectID, *headingID}
	case entity.ProductionAssetHeadingSummary:
		return "heading_summary_edits", "project_id = $1 AND heading_id = $2", []any{projectID, *headingID}
	case entity.ProductionAssetSectionAudio:
		return "section_audio_edits", "project_id = $1 AND heading_id = $2", []any{projectID, *headingID}
	default:
		return "", "", nil
	}
}

func reviewStatusForDecision(decision string) string {
	switch decision {
	case entity.ProductionReviewDecisionSubmit:
		return entity.ProductionReviewPendingReview
	case entity.ProductionReviewDecisionApprove:
		return entity.ProductionReviewApproved
	case entity.ProductionReviewDecisionReject:
		return entity.ProductionReviewRejected
	default:
		return entity.ProductionReviewDraft
	}
}

func finalAssetDeleteSQL(
	project entity.BookProductionProject,
	actorID,
	assetType string,
	headingID *int,
	reason *string,
) (string, []any, error) {
	baseSet := "SET is_deleted = true, deleted_by = $3, deleted_at = now(), delete_reason = $4, updated_at = now()"
	switch assetType {
	case entity.ProductionAssetBookMetadata:
		return "UPDATE book_metadata_translations " + baseSet + " WHERE book_id = $1 AND lang = $2 AND is_deleted = false",
			[]any{project.BookID, project.Lang, actorID, reason}, nil
	case entity.ProductionAssetAuthorMetadata:
		return `UPDATE author_translations ` + baseSet + `
WHERE lang = $2
  AND is_deleted = false
  AND author_id = (SELECT author_id FROM books WHERE id = $1)`,
			[]any{project.BookID, project.Lang, actorID, reason}, nil
	case entity.ProductionAssetCategoryMetadata:
		return `UPDATE category_translations ` + baseSet + `
WHERE lang = $2
  AND is_deleted = false
  AND category_id = (
      SELECT COALESCE(me.category_id, b.category_id)
      FROM books b
      LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
      WHERE b.id = $1
  )`,
			[]any{project.BookID, project.Lang, actorID, reason}, nil
	case entity.ProductionAssetSectionTranslation:
		return "UPDATE section_translations " + baseSet + " WHERE book_id = $1 AND lang = $2 AND heading_id = $5 AND is_deleted = false",
			[]any{project.BookID, project.Lang, actorID, reason, *headingID}, nil
	case entity.ProductionAssetHeadingSummary:
		return "UPDATE book_heading_summaries " + baseSet + " WHERE book_id = $1 AND lang = $2 AND heading_id = $5 AND is_deleted = false",
			[]any{project.BookID, project.Lang, actorID, reason, *headingID}, nil
	case entity.ProductionAssetSectionAudio:
		return "UPDATE section_audio " + baseSet + " WHERE book_id = $1 AND lang = $2 AND heading_id = $5 AND is_deleted = false",
			[]any{project.BookID, project.Lang, actorID, reason, *headingID}, nil
	default:
		return "", nil, entity.ErrInvalidAssetType
	}
}

func (r *EditorialRepo) productionWorkspaceBook(
	ctx context.Context,
	bookID int,
) (entity.BookProductionWorkspaceBook, error) {
	var book entity.BookProductionWorkspaceBook
	var categoryID sql.NullInt64
	var categoryName sql.NullString
	var authorID sql.NullInt64
	var authorName sql.NullString

	err := r.Pool.QueryRow(ctx, `
SELECT b.id,
       b.name,
       COALESCE(me.category_id, b.category_id),
       c.name,
       b.author_id,
       a.name,
       b.has_content
FROM books b
LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN categories c ON c.id = COALESCE(me.category_id, b.category_id) AND c.is_deleted = false
LEFT JOIN authors a ON a.id = b.author_id AND a.is_deleted = false
WHERE b.id = $1 AND b.is_deleted = false`, bookID).
		Scan(&book.ID, &book.Name, &categoryID, &categoryName, &authorID, &authorName, &book.HasContent)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionWorkspaceBook{}, entity.ErrBookNotFound
		}

		return entity.BookProductionWorkspaceBook{}, fmt.Errorf("EditorialRepo - productionWorkspaceBook - query: %w", err)
	}

	book.CategoryID = nullableInt(categoryID)
	book.CategoryName = nullableString(categoryName)
	book.AuthorID = nullableInt(authorID)
	book.AuthorName = nullableString(authorName)

	return book, nil
}

func productionScalarAssetStatus(
	ctx context.Context,
	q productionQuerier,
	project entity.BookProductionProject,
	assetType string,
	required bool,
	table string,
	contentCondition string,
	finalExists bool,
) (entity.ProductionAssetStatus, error) {
	status := entity.ProductionAssetStatus{
		AssetType:   assetType,
		Required:    required,
		Complete:    !required,
		FinalExists: finalExists,
	}

	sqlText := fmt.Sprintf(`
SELECT review_status, updated_at, reviewed_at, %s
FROM %s
WHERE project_id = $1`, contentCondition, table)

	var reviewStatus sql.NullString
	var updatedAt sql.NullTime
	var reviewedAt sql.NullTime
	var contentOK bool
	err := q.QueryRow(ctx, sqlText, project.ID).Scan(&reviewStatus, &updatedAt, &reviewedAt, &contentOK)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return status, nil
		}

		return entity.ProductionAssetStatus{}, fmt.Errorf("production scalar asset status: %w", err)
	}

	return productionAssetStatusFromScan(
		assetType,
		nil,
		required,
		true,
		contentOK,
		reviewStatus,
		updatedAt,
		reviewedAt,
		finalExists,
		project.RequiresReview,
	), nil
}

func productionWorkspaceHeadings(
	ctx context.Context,
	q productionQuerier,
	project entity.BookProductionProject,
) ([]entity.BookProductionWorkspaceHeading, error) {
	rows, err := q.Query(ctx, `
SELECT h.book_id,
       h.heading_id,
       h.parent_id,
       h.page_id,
       h.depth,
       h.ordinal,
       h.content,
       st.review_status,
       st.updated_at,
       st.reviewed_at,
       st.project_id IS NOT NULL,
       NULLIF(BTRIM(st.content), '') IS NOT NULL,
       EXISTS (
           SELECT 1
           FROM section_translations final_st
           WHERE final_st.book_id = h.book_id
             AND final_st.heading_id = h.heading_id
             AND final_st.lang = $2
             AND final_st.is_deleted = false
       ),
       hs.review_status,
       hs.updated_at,
       hs.reviewed_at,
       hs.project_id IS NOT NULL,
       NULLIF(BTRIM(hs.summary), '') IS NOT NULL,
       EXISTS (
           SELECT 1
           FROM book_heading_summaries final_hs
           WHERE final_hs.book_id = h.book_id
             AND final_hs.heading_id = h.heading_id
             AND final_hs.lang = $2
             AND final_hs.is_deleted = false
       ),
       audio.review_status,
       audio.updated_at,
       audio.reviewed_at,
       audio.project_id IS NOT NULL,
       NULLIF(BTRIM(audio.url), '') IS NOT NULL,
       EXISTS (
           SELECT 1
           FROM section_audio final_audio
           WHERE final_audio.book_id = h.book_id
             AND final_audio.heading_id = h.heading_id
             AND final_audio.lang = $2
             AND final_audio.is_deleted = false
       )
FROM book_headings h
LEFT JOIN section_translation_edits st ON st.project_id = $3 AND st.heading_id = h.heading_id
LEFT JOIN heading_summary_edits hs ON hs.project_id = $3 AND hs.heading_id = h.heading_id
LEFT JOIN section_audio_edits audio ON audio.project_id = $3 AND audio.heading_id = h.heading_id
WHERE h.book_id = $1 AND h.is_deleted = false
ORDER BY h.ordinal ASC, h.heading_id ASC`, project.BookID, project.Lang, project.ID)
	if err != nil {
		return nil, fmt.Errorf("production workspace headings query: %w", err)
	}
	defer rows.Close()

	headings := make([]entity.BookProductionWorkspaceHeading, 0)
	for rows.Next() {
		var heading entity.BookProductionWorkspaceHeading
		var parentID sql.NullInt64
		var translationReviewStatus sql.NullString
		var translationUpdatedAt sql.NullTime
		var translationReviewedAt sql.NullTime
		var translationExists bool
		var translationContentOK bool
		var translationFinalExists bool
		var summaryReviewStatus sql.NullString
		var summaryUpdatedAt sql.NullTime
		var summaryReviewedAt sql.NullTime
		var summaryExists bool
		var summaryContentOK bool
		var summaryFinalExists bool
		var audioReviewStatus sql.NullString
		var audioUpdatedAt sql.NullTime
		var audioReviewedAt sql.NullTime
		var audioExists bool
		var audioContentOK bool
		var audioFinalExists bool

		err = rows.Scan(
			&heading.BookID,
			&heading.HeadingID,
			&parentID,
			&heading.PageID,
			&heading.Depth,
			&heading.Ordinal,
			&heading.SourceTitle,
			&translationReviewStatus,
			&translationUpdatedAt,
			&translationReviewedAt,
			&translationExists,
			&translationContentOK,
			&translationFinalExists,
			&summaryReviewStatus,
			&summaryUpdatedAt,
			&summaryReviewedAt,
			&summaryExists,
			&summaryContentOK,
			&summaryFinalExists,
			&audioReviewStatus,
			&audioUpdatedAt,
			&audioReviewedAt,
			&audioExists,
			&audioContentOK,
			&audioFinalExists,
		)
		if err != nil {
			return nil, fmt.Errorf("production workspace headings scan: %w", err)
		}

		heading.ParentID = nullableInt(parentID)
		headingID := heading.HeadingID
		heading.Translation = productionAssetStatusFromScan(
			entity.ProductionAssetSectionTranslation,
			&headingID,
			true,
			translationExists,
			translationContentOK,
			translationReviewStatus,
			translationUpdatedAt,
			translationReviewedAt,
			translationFinalExists,
			project.RequiresReview,
		)
		heading.Summary = productionAssetStatusFromScan(
			entity.ProductionAssetHeadingSummary,
			&headingID,
			true,
			summaryExists,
			summaryContentOK,
			summaryReviewStatus,
			summaryUpdatedAt,
			summaryReviewedAt,
			summaryFinalExists,
			project.RequiresReview,
		)
		heading.Audio = productionAssetStatusFromScan(
			entity.ProductionAssetSectionAudio,
			&headingID,
			project.RequiresAudio,
			audioExists,
			audioContentOK,
			audioReviewStatus,
			audioUpdatedAt,
			audioReviewedAt,
			audioFinalExists,
			project.RequiresReview,
		)

		headings = append(headings, heading)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("production workspace headings rows: %w", err)
	}

	return headings, nil
}

func productionAssetStatusFromScan(
	assetType string,
	headingID *int,
	required bool,
	exists bool,
	contentOK bool,
	reviewStatus sql.NullString,
	updatedAt sql.NullTime,
	reviewedAt sql.NullTime,
	finalExists bool,
	requiresReview bool,
) entity.ProductionAssetStatus {
	status := entity.ProductionAssetStatus{
		AssetType:    assetType,
		HeadingID:    headingID,
		Required:     required,
		Exists:       exists,
		ReviewStatus: nullableString(reviewStatus),
		UpdatedAt:    nullableTime(updatedAt),
		ReviewedAt:   nullableTime(reviewedAt),
		FinalExists:  finalExists,
	}
	status.Complete = productionAssetComplete(required, exists, contentOK, status.ReviewStatus, requiresReview)

	return status
}

func productionAssetComplete(required bool, exists bool, contentOK bool, reviewStatus *string, requiresReview bool) bool {
	if !required {
		return true
	}
	if !exists || !contentOK || reviewStatus == nil {
		return false
	}
	if requiresReview {
		return *reviewStatus == entity.ProductionReviewApproved
	}

	return *reviewStatus != entity.ProductionReviewRejected
}

func productionFinalExists(ctx context.Context, q productionQuerier, sqlText string, args ...any) bool {
	var exists bool
	if err := q.QueryRow(ctx, sqlText, args...).Scan(&exists); err != nil {
		return false
	}

	return exists
}

func jsonString(raw entity.RawJSON) string {
	if len(raw) == 0 {
		return ""
	}

	return string(raw)
}

func lockProductionProject(ctx context.Context, tx pgx.Tx, projectID string) (entity.BookProductionProject, error) {
	project, err := scanProductionProject(tx.QueryRow(ctx, `
SELECT id, book_id, lang, workflow_status, publication_status, requires_review,
       requires_audio, priority, owner_id, notes, created_by, updated_by, published_by,
       created_at, updated_at, published_at, archived_at
FROM book_production_projects
WHERE id = $1
FOR UPDATE`, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
		}

		return entity.BookProductionProject{}, fmt.Errorf("lock production project: %w", err)
	}

	return project, nil
}

func touchProductionProjectTx(ctx context.Context, tx pgx.Tx, actorID, projectID, workflowStatus string) error {
	if workflowStatus == "" {
		_, err := tx.Exec(ctx, `
UPDATE book_production_projects
SET updated_by = $2, updated_at = now()
WHERE id = $1`, projectID, actorID)

		return err
	}

	_, err := tx.Exec(ctx, `
UPDATE book_production_projects
SET workflow_status = CASE
        WHEN workflow_status IN ('published', 'archived') THEN workflow_status
        ELSE $3
    END,
    publication_status = CASE
        WHEN workflow_status = 'published' THEN publication_status
        ELSE 'hidden'
    END,
    updated_by = $2,
    updated_at = now()
WHERE id = $1`, projectID, actorID, workflowStatus)

	return err
}

func insertProductionDraftRevision(
	ctx context.Context,
	tx pgx.Tx,
	actorID,
	projectID,
	assetType string,
	headingID *int,
	snapshot any,
) (entity.BookProductionDraftRevision, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return entity.BookProductionDraftRevision{}, fmt.Errorf("marshal production draft revision: %w", err)
	}

	revision, err := scanProductionDraftRevision(tx.QueryRow(ctx, `
WITH next_version AS (
    SELECT COALESCE(MAX(version), 0) + 1 AS version
    FROM book_production_draft_revisions
    WHERE project_id = $1
      AND asset_type = $2
      AND COALESCE(heading_id, 0) = COALESCE($3::INTEGER, 0)
)
INSERT INTO book_production_draft_revisions (
    id, project_id, asset_type, heading_id, version, actor_id, snapshot, created_at
)
SELECT $4, $1, $2, $3, next_version.version, $5, $6::jsonb, now()
FROM next_version
RETURNING id, project_id, asset_type, heading_id, version, actor_id, snapshot, created_at`,
		projectID,
		assetType,
		headingID,
		uuid.New().String(),
		emptyStringNil(actorID),
		string(payload),
	))
	if err != nil {
		return entity.BookProductionDraftRevision{}, fmt.Errorf("insert production draft revision: %w", err)
	}

	return revision, nil
}

func restoreProductionDraftSnapshot(
	ctx context.Context,
	tx pgx.Tx,
	actorID,
	projectID string,
	revision entity.BookProductionDraftRevision,
) (any, error) {
	snapshot := string(revision.Snapshot)
	switch revision.AssetType {
	case entity.ProductionAssetBookMetadata:
		return scanMetadataTranslationEditOrNotFound(tx.QueryRow(ctx, `
INSERT INTO book_metadata_translation_edits (
    project_id, display_title, bibliography, hint, description, source, metadata,
    review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
)
VALUES (
    $1,
    $2::jsonb ->> 'display_title',
    $2::jsonb ->> 'bibliography',
    $2::jsonb ->> 'hint',
    $2::jsonb ->> 'description',
    $2::jsonb ->> 'source',
    NULLIF(($2::jsonb -> 'metadata')::text, 'null')::jsonb,
    'draft',
    NULL,
    $3,
    NULL,
    now(),
    NULL
)
ON CONFLICT (project_id) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    description = EXCLUDED.description,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, display_title, bibliography, hint, description, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`, projectID, snapshot, actorID))
	case entity.ProductionAssetAuthorMetadata:
		return scanAuthorTranslationEditOrNotFound(tx.QueryRow(ctx, `
INSERT INTO author_translation_edits (
    project_id, name, biography, death_text, source, metadata,
    review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
)
VALUES (
    $1,
    $2::jsonb ->> 'name',
    $2::jsonb ->> 'biography',
    $2::jsonb ->> 'death_text',
    $2::jsonb ->> 'source',
    NULLIF(($2::jsonb -> 'metadata')::text, 'null')::jsonb,
    'draft',
    NULL,
    $3,
    NULL,
    now(),
    NULL
)
ON CONFLICT (project_id) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, name, biography, death_text, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`, projectID, snapshot, actorID))
	case entity.ProductionAssetCategoryMetadata:
		return scanCategoryTranslationEditOrNotFound(tx.QueryRow(ctx, `
INSERT INTO category_translation_edits (
    project_id, name, source, metadata, review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
)
VALUES (
    $1,
    $2::jsonb ->> 'name',
    $2::jsonb ->> 'source',
    NULLIF(($2::jsonb -> 'metadata')::text, 'null')::jsonb,
    'draft',
    NULL,
    $3,
    NULL,
    now(),
    NULL
)
ON CONFLICT (project_id) DO UPDATE SET
    name = EXCLUDED.name,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, name, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`, projectID, snapshot, actorID))
	case entity.ProductionAssetSectionTranslation:
		return scanSectionTranslationEditOrNotFound(tx.QueryRow(ctx, `
INSERT INTO section_translation_edits (
    project_id, heading_id, title, content, source, metadata,
    review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
)
VALUES (
    $1,
    $2,
    $3::jsonb ->> 'title',
    $3::jsonb ->> 'content',
    $3::jsonb ->> 'source',
    NULLIF(($3::jsonb -> 'metadata')::text, 'null')::jsonb,
    'draft',
    NULL,
    $4,
    NULL,
    now(),
    NULL
)
ON CONFLICT (project_id, heading_id) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, heading_id, title, content, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`, projectID, *revision.HeadingID, snapshot, actorID))
	case entity.ProductionAssetHeadingSummary:
		return scanHeadingSummaryEditOrNotFound(tx.QueryRow(ctx, `
INSERT INTO heading_summary_edits (
    project_id, heading_id, summary, source, metadata,
    review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
)
VALUES (
    $1,
    $2,
    $3::jsonb ->> 'summary',
    $3::jsonb ->> 'source',
    NULLIF(($3::jsonb -> 'metadata')::text, 'null')::jsonb,
    'draft',
    NULL,
    $4,
    NULL,
    now(),
    NULL
)
ON CONFLICT (project_id, heading_id) DO UPDATE SET
    summary = EXCLUDED.summary,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, heading_id, summary, source, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`, projectID, *revision.HeadingID, snapshot, actorID))
	case entity.ProductionAssetSectionAudio:
		return scanSectionAudioEditOrNotFound(tx.QueryRow(ctx, `
INSERT INTO section_audio_edits (
    project_id, heading_id, url, narrator, duration_seconds, mime_type, metadata,
    review_status, review_note, updated_by, reviewed_by, updated_at, reviewed_at
)
VALUES (
    $1,
    $2,
    $3::jsonb ->> 'url',
    $3::jsonb ->> 'narrator',
    ($3::jsonb ->> 'duration_seconds')::INTEGER,
    $3::jsonb ->> 'mime_type',
    NULLIF(($3::jsonb -> 'metadata')::text, 'null')::jsonb,
    'draft',
    NULL,
    $4,
    NULL,
    now(),
    NULL
)
ON CONFLICT (project_id, heading_id) DO UPDATE SET
    url = EXCLUDED.url,
    narrator = EXCLUDED.narrator,
    duration_seconds = EXCLUDED.duration_seconds,
    mime_type = EXCLUDED.mime_type,
    metadata = EXCLUDED.metadata,
    review_status = 'draft',
    review_note = NULL,
    reviewed_by = NULL,
    reviewed_at = NULL,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING project_id, heading_id, url, narrator, duration_seconds, mime_type, metadata, review_status,
          review_note, updated_by, reviewed_by, updated_at, reviewed_at`, projectID, *revision.HeadingID, snapshot, actorID))
	default:
		return nil, entity.ErrInvalidAssetType
	}
}

func (r *EditorialRepo) recordProductionEvent(
	ctx context.Context,
	actorID,
	projectID,
	eventType string,
	assetType *string,
	headingID *int,
	note *string,
	payload any,
) error {
	var payloadJSON []byte
	var err error
	if payload != nil {
		payloadJSON, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal production event payload: %w", err)
		}
	}

	_, err = r.Pool.Exec(ctx, `
INSERT INTO book_production_events (
    id, project_id, actor_id, event_type, asset_type, heading_id, note, payload, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, now())`,
		uuid.New().String(),
		projectID,
		emptyStringNil(actorID),
		eventType,
		assetType,
		headingID,
		note,
		string(payloadJSON),
	)
	if err != nil {
		return fmt.Errorf("insert production event: %w", err)
	}

	return nil
}

func scanProductionEvent(row rowScanner) (entity.BookProductionEvent, error) {
	var event entity.BookProductionEvent
	var actorID sql.NullString
	var assetType sql.NullString
	var headingID sql.NullInt64
	var note sql.NullString
	var payload []byte

	err := row.Scan(
		&event.ID,
		&event.ProjectID,
		&actorID,
		&event.EventType,
		&assetType,
		&headingID,
		&note,
		&payload,
		&event.CreatedAt,
	)
	if err != nil {
		return entity.BookProductionEvent{}, err
	}

	event.ActorID = nullableString(actorID)
	event.AssetType = nullableString(assetType)
	event.HeadingID = nullableInt(headingID)
	event.Note = nullableString(note)
	event.Payload = entity.RawJSON(payload)

	return event, nil
}

func scanProductionDraftRevision(row rowScanner) (entity.BookProductionDraftRevision, error) {
	var revision entity.BookProductionDraftRevision
	var headingID sql.NullInt64
	var actorID sql.NullString
	var snapshot []byte

	err := row.Scan(
		&revision.ID,
		&revision.ProjectID,
		&revision.AssetType,
		&headingID,
		&revision.Version,
		&actorID,
		&snapshot,
		&revision.CreatedAt,
	)
	if err != nil {
		return entity.BookProductionDraftRevision{}, err
	}

	revision.HeadingID = nullableInt(headingID)
	revision.ActorID = nullableString(actorID)
	revision.Snapshot = entity.RawJSON(snapshot)

	return revision, nil
}

func scanProductionProject(row rowScanner) (entity.BookProductionProject, error) {
	var project entity.BookProductionProject
	var ownerID sql.NullString
	var notes sql.NullString
	var createdBy sql.NullString
	var updatedBy sql.NullString
	var publishedBy sql.NullString
	var publishedAt sql.NullTime
	var archivedAt sql.NullTime

	err := row.Scan(
		&project.ID,
		&project.BookID,
		&project.Lang,
		&project.WorkflowStatus,
		&project.PublicationStatus,
		&project.RequiresReview,
		&project.RequiresAudio,
		&project.Priority,
		&ownerID,
		&notes,
		&createdBy,
		&updatedBy,
		&publishedBy,
		&project.CreatedAt,
		&project.UpdatedAt,
		&publishedAt,
		&archivedAt,
	)
	if err != nil {
		return entity.BookProductionProject{}, err
	}

	project.OwnerID = nullableString(ownerID)
	project.Notes = nullableString(notes)
	project.CreatedBy = nullableString(createdBy)
	project.UpdatedBy = nullableString(updatedBy)
	project.PublishedBy = nullableString(publishedBy)
	project.PublishedAt = nullableTime(publishedAt)
	project.ArchivedAt = nullableTime(archivedAt)

	return project, nil
}

func scanMetadataTranslationEditOrNotFound(row rowScanner) (entity.BookMetadataTranslationEdit, error) {
	var edit entity.BookMetadataTranslationEdit
	var bibliography sql.NullString
	var hint sql.NullString
	var description sql.NullString
	var source sql.NullString
	var metadata []byte
	var reviewNote sql.NullString
	var updatedBy sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&edit.ProjectID,
		&edit.DisplayTitle,
		&bibliography,
		&hint,
		&description,
		&source,
		&metadata,
		&edit.ReviewStatus,
		&reviewNote,
		&updatedBy,
		&reviewedBy,
		&edit.UpdatedAt,
		&reviewedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookMetadataTranslationEdit{}, entity.ErrDraftNotFound
		}
		return entity.BookMetadataTranslationEdit{}, err
	}

	edit.Bibliography = nullableString(bibliography)
	edit.Hint = nullableString(hint)
	edit.Description = nullableString(description)
	edit.Source = nullableString(source)
	edit.Metadata = entity.RawJSON(metadata)
	edit.ReviewNote = nullableString(reviewNote)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.ReviewedBy = nullableString(reviewedBy)
	edit.ReviewedAt = nullableTime(reviewedAt)

	return edit, nil
}

func scanAuthorTranslationEditOrNotFound(row rowScanner) (entity.AuthorTranslationEdit, error) {
	var edit entity.AuthorTranslationEdit
	var biography sql.NullString
	var deathText sql.NullString
	var source sql.NullString
	var metadata []byte
	var reviewNote sql.NullString
	var updatedBy sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&edit.ProjectID,
		&edit.Name,
		&biography,
		&deathText,
		&source,
		&metadata,
		&edit.ReviewStatus,
		&reviewNote,
		&updatedBy,
		&reviewedBy,
		&edit.UpdatedAt,
		&reviewedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.AuthorTranslationEdit{}, entity.ErrDraftNotFound
		}
		return entity.AuthorTranslationEdit{}, err
	}

	edit.Biography = nullableString(biography)
	edit.DeathText = nullableString(deathText)
	edit.Source = nullableString(source)
	edit.Metadata = entity.RawJSON(metadata)
	edit.ReviewNote = nullableString(reviewNote)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.ReviewedBy = nullableString(reviewedBy)
	edit.ReviewedAt = nullableTime(reviewedAt)

	return edit, nil
}

func scanCategoryTranslationEditOrNotFound(row rowScanner) (entity.CategoryTranslationEdit, error) {
	var edit entity.CategoryTranslationEdit
	var source sql.NullString
	var metadata []byte
	var reviewNote sql.NullString
	var updatedBy sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&edit.ProjectID,
		&edit.Name,
		&source,
		&metadata,
		&edit.ReviewStatus,
		&reviewNote,
		&updatedBy,
		&reviewedBy,
		&edit.UpdatedAt,
		&reviewedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.CategoryTranslationEdit{}, entity.ErrDraftNotFound
		}
		return entity.CategoryTranslationEdit{}, err
	}

	edit.Source = nullableString(source)
	edit.Metadata = entity.RawJSON(metadata)
	edit.ReviewNote = nullableString(reviewNote)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.ReviewedBy = nullableString(reviewedBy)
	edit.ReviewedAt = nullableTime(reviewedAt)

	return edit, nil
}

func scanSectionTranslationEditOrNotFound(row rowScanner) (entity.SectionTranslationEdit, error) {
	var edit entity.SectionTranslationEdit
	var title sql.NullString
	var source sql.NullString
	var metadata []byte
	var reviewNote sql.NullString
	var updatedBy sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&edit.ProjectID,
		&edit.HeadingID,
		&title,
		&edit.Content,
		&source,
		&metadata,
		&edit.ReviewStatus,
		&reviewNote,
		&updatedBy,
		&reviewedBy,
		&edit.UpdatedAt,
		&reviewedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.SectionTranslationEdit{}, entity.ErrDraftNotFound
		}
		return entity.SectionTranslationEdit{}, err
	}

	edit.Title = nullableString(title)
	edit.Source = nullableString(source)
	edit.Metadata = entity.RawJSON(metadata)
	edit.ReviewNote = nullableString(reviewNote)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.ReviewedBy = nullableString(reviewedBy)
	edit.ReviewedAt = nullableTime(reviewedAt)

	return edit, nil
}

func scanHeadingSummaryEditOrNotFound(row rowScanner) (entity.HeadingSummaryEdit, error) {
	var edit entity.HeadingSummaryEdit
	var source sql.NullString
	var metadata []byte
	var reviewNote sql.NullString
	var updatedBy sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&edit.ProjectID,
		&edit.HeadingID,
		&edit.Summary,
		&source,
		&metadata,
		&edit.ReviewStatus,
		&reviewNote,
		&updatedBy,
		&reviewedBy,
		&edit.UpdatedAt,
		&reviewedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.HeadingSummaryEdit{}, entity.ErrDraftNotFound
		}
		return entity.HeadingSummaryEdit{}, err
	}

	edit.Source = nullableString(source)
	edit.Metadata = entity.RawJSON(metadata)
	edit.ReviewNote = nullableString(reviewNote)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.ReviewedBy = nullableString(reviewedBy)
	edit.ReviewedAt = nullableTime(reviewedAt)

	return edit, nil
}

func scanSectionAudioEditOrNotFound(row rowScanner) (entity.SectionAudioEdit, error) {
	var edit entity.SectionAudioEdit
	var narrator sql.NullString
	var duration sql.NullInt64
	var mimeType sql.NullString
	var metadata []byte
	var reviewNote sql.NullString
	var updatedBy sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&edit.ProjectID,
		&edit.HeadingID,
		&edit.URL,
		&narrator,
		&duration,
		&mimeType,
		&metadata,
		&edit.ReviewStatus,
		&reviewNote,
		&updatedBy,
		&reviewedBy,
		&edit.UpdatedAt,
		&reviewedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.SectionAudioEdit{}, entity.ErrDraftNotFound
		}
		return entity.SectionAudioEdit{}, err
	}

	edit.Narrator = nullableString(narrator)
	edit.DurationSeconds = nullableInt(duration)
	edit.MIMEType = nullableString(mimeType)
	edit.Metadata = entity.RawJSON(metadata)
	edit.ReviewNote = nullableString(reviewNote)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.ReviewedBy = nullableString(reviewedBy)
	edit.ReviewedAt = nullableTime(reviewedAt)

	return edit, nil
}

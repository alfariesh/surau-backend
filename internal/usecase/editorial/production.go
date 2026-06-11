package editorial

import (
	"context"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
)

const maxProductionActivityLimit = 100

// CreateProductionProject starts a book+language production workflow.
func (uc *UseCase) CreateProductionProject(
	ctx context.Context,
	actorID string,
	project entity.BookProductionProject,
) (entity.BookProductionProject, error) {
	lang, err := entity.NormalizeProductionLang(project.Lang)
	if err != nil {
		return entity.BookProductionProject{}, err
	}

	status := strings.TrimSpace(project.WorkflowStatus)
	if status == "" {
		status = entity.ProductionWorkflowCandidate
	} else {
		status, err = entity.NormalizeProductionWorkflowStatus(status)
		if err != nil {
			return entity.BookProductionProject{}, err
		}
	}

	project.Lang = lang
	project.WorkflowStatus = status
	project.PublicationStatus = entity.ProductionPublicationHidden
	project.Notes = trimStringPtr(project.Notes)
	if project.Priority < 0 {
		project.Priority = 0
	}
	if project.BookID <= 0 {
		return entity.BookProductionProject{}, entity.ErrBookNotFound
	}

	return uc.repo.CreateProductionProject(ctx, actorID, project)
}

// ProductionDashboard returns compact operational counts and recent activity for one target language.
func (uc *UseCase) ProductionDashboard(
	ctx context.Context,
	lang string,
	activityLimit int,
) (entity.BookProductionDashboard, error) {
	normalizedLang, err := entity.NormalizeProductionLang(lang)
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	hasContent := true
	_, candidateTotal, err := uc.repo.ListProductionCandidates(ctx, repo.ProductionCandidateFilter{
		Lang:       normalizedLang,
		HasContent: &hasContent,
		Unstarted:  true,
		Limit:      1,
		Offset:     0,
	})
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	_, activeTotal, err := uc.repo.ListProductionProjects(ctx, repo.ProductionProjectFilter{
		Lang:              normalizedLang,
		PublicationStatus: entity.ProductionPublicationHidden,
		Limit:             1,
		Offset:            0,
	})
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	_, needsWorkTotal, err := uc.repo.ListProductionProjects(ctx, repo.ProductionProjectFilter{
		Lang:      normalizedLang,
		NeedsWork: true,
		Limit:     1,
		Offset:    0,
	})
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	_, readyTotal, err := uc.repo.ListProductionProjects(ctx, repo.ProductionProjectFilter{
		Lang:           normalizedLang,
		ReadyToPublish: true,
		Limit:          1,
		Offset:         0,
	})
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	_, publishedTotal, err := uc.repo.ListProductionProjects(ctx, repo.ProductionProjectFilter{
		Lang:              normalizedLang,
		PublicationStatus: entity.ProductionPublicationPublished,
		Limit:             1,
		Offset:            0,
	})
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	events, eventsTotal, err := uc.repo.ListProductionEventsGlobal(
		ctx,
		normalizedLang,
		clampActivityLimit(activityLimit),
		0,
	)
	if err != nil {
		return entity.BookProductionDashboard{}, err
	}

	return entity.BookProductionDashboard{
		Lang:                normalizedLang,
		CandidateCount:      candidateTotal,
		ActiveProjectCount:  activeTotal,
		NeedsWorkCount:      needsWorkTotal,
		ReadyToPublishCount: readyTotal,
		PublishedCount:      publishedTotal,
		RecentEvents:        events,
		RecentEventsTotal:   eventsTotal,
	}, nil
}

// ProductionProjects lists translation production projects.
func (uc *UseCase) ProductionProjects(
	ctx context.Context,
	bookID *int,
	lang,
	workflowStatus,
	publicationStatus string,
	readyToPublish,
	needsWork bool,
	limit,
	offset int,
) ([]entity.BookProductionProject, int, error) {
	if readyToPublish && needsWork {
		return nil, 0, entity.ErrInvalidStatus
	}

	var err error
	if lang = strings.TrimSpace(lang); lang != "" {
		lang, err = entity.NormalizeProductionLang(lang)
		if err != nil {
			return nil, 0, err
		}
	}

	workflowStatus = strings.ToLower(strings.TrimSpace(workflowStatus))
	if workflowStatus != "" {
		workflowStatus, err = entity.NormalizeProductionWorkflowStatus(workflowStatus)
		if err != nil {
			return nil, 0, err
		}
	}

	publicationStatus = strings.ToLower(strings.TrimSpace(publicationStatus))
	if publicationStatus != "" && !isProductionPublicationStatus(publicationStatus) {
		return nil, 0, entity.ErrInvalidStatus
	}

	return uc.repo.ListProductionProjects(ctx, repo.ProductionProjectFilter{
		BookID:            bookID,
		Lang:              lang,
		WorkflowStatus:    workflowStatus,
		PublicationStatus: publicationStatus,
		ReadyToPublish:    readyToPublish,
		NeedsWork:         needsWork,
		Limit:             clampLimit(limit),
		Offset:            clampOffset(offset),
	})
}

// ProductionProject returns one production project.
func (uc *UseCase) ProductionProject(ctx context.Context, projectID string) (entity.BookProductionProject, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
	}

	return uc.repo.GetProductionProject(ctx, projectID)
}

// ProductionWorkspace returns the editor workspace for one project.
func (uc *UseCase) ProductionWorkspace(ctx context.Context, projectID string) (entity.BookProductionWorkspace, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionWorkspace{}, entity.ErrProductionProjectNotFound
	}

	return uc.repo.ProductionWorkspace(ctx, projectID)
}

// ProductionActivity returns the production timeline for one project.
func (uc *UseCase) ProductionActivity(
	ctx context.Context,
	projectID string,
	limit,
	offset int,
) ([]entity.BookProductionEvent, int, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, 0, entity.ErrProductionProjectNotFound
	}

	return uc.repo.ListProductionEvents(ctx, projectID, clampActivityLimit(limit), clampOffset(offset))
}

// GlobalProductionActivity returns the latest timeline events across production projects.
func (uc *UseCase) GlobalProductionActivity(
	ctx context.Context,
	lang string,
	limit,
	offset int,
) ([]entity.BookProductionEvent, int, error) {
	var err error
	if lang = strings.TrimSpace(lang); lang != "" {
		lang, err = entity.NormalizeProductionLang(lang)
		if err != nil {
			return nil, 0, err
		}
	}

	return uc.repo.ListProductionEventsGlobal(ctx, lang, clampActivityLimit(limit), clampOffset(offset))
}

// ProductionDraftRevisions returns immutable draft snapshots for one production asset.
func (uc *UseCase) ProductionDraftRevisions(
	ctx context.Context,
	projectID,
	assetType string,
	headingID *int,
	limit,
	offset int,
) ([]entity.BookProductionDraftRevision, int, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, 0, entity.ErrProductionProjectNotFound
	}

	assetType, headingID, err := entity.NormalizeProductionDraftTarget(assetType, headingID)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListProductionDraftRevisions(ctx, repo.ProductionDraftRevisionFilter{
		ProjectID: projectID,
		AssetType: assetType,
		HeadingID: headingID,
		Limit:     clampLimit(limit),
		Offset:    clampOffset(offset),
	})
}

// RestoreProductionDraftRevision restores a previous draft snapshot and creates a new revision.
func (uc *UseCase) RestoreProductionDraftRevision(
	ctx context.Context,
	actorID,
	projectID,
	revisionID string,
) (entity.BookProductionDraftRevision, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionDraftRevision{}, entity.ErrProductionProjectNotFound
	}

	revisionID = strings.TrimSpace(revisionID)
	if revisionID == "" {
		return entity.BookProductionDraftRevision{}, entity.ErrDraftNotFound
	}

	return uc.repo.RestoreProductionDraftRevision(ctx, actorID, projectID, revisionID)
}

// ProductionPublishCheck returns a read-only publish validator payload.
func (uc *UseCase) ProductionPublishCheck(
	ctx context.Context,
	projectID string,
) (entity.BookProductionPublishCheck, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionPublishCheck{}, entity.ErrProductionProjectNotFound
	}

	return uc.repo.ProductionPublishCheck(ctx, projectID)
}

// UpdateProductionProject updates mutable production settings.
func (uc *UseCase) UpdateProductionProject(
	ctx context.Context,
	actorID,
	projectID string,
	patch entity.BookProductionProjectPatch,
	expected *time.Time,
) (entity.BookProductionProject, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
	}

	if patch.WorkflowStatus != nil {
		normalized, normalizeErr := entity.NormalizeProductionWorkflowStatus(*patch.WorkflowStatus)
		if normalizeErr != nil {
			return entity.BookProductionProject{}, normalizeErr
		}
		patch.WorkflowStatus = &normalized
	}
	if patch.OwnerID != nil {
		patch.OwnerID = trimStringPtr(patch.OwnerID)
	}
	if patch.Notes != nil {
		patch.Notes = trimStringPtr(patch.Notes)
	}
	if patch.Priority != nil && *patch.Priority < 0 {
		zero := 0
		patch.Priority = &zero
	}

	return uc.repo.UpdateProductionProject(ctx, actorID, projectID, patch, expected)
}

// ProductionCompleteness returns publish readiness for a project.
func (uc *UseCase) ProductionCompleteness(ctx context.Context, projectID string) (entity.BookProductionCompleteness, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionCompleteness{}, entity.ErrProductionProjectNotFound
	}

	return uc.repo.ProductionCompleteness(ctx, projectID)
}

func (uc *UseCase) GetMetadataTranslationDraft(ctx context.Context, projectID string) (entity.BookMetadataTranslationEdit, error) {
	return uc.repo.GetMetadataTranslationDraft(ctx, strings.TrimSpace(projectID))
}

func (uc *UseCase) SaveMetadataTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.BookMetadataTranslationEdit,
	expected *time.Time,
) (entity.BookMetadataTranslationEdit, error) {
	edit.ProjectID = strings.TrimSpace(projectID)
	edit.DisplayTitle = strings.TrimSpace(edit.DisplayTitle)
	edit.Bibliography = trimStringPtr(edit.Bibliography)
	edit.Hint = trimStringPtr(edit.Hint)
	edit.Description = trimStringPtr(edit.Description)
	edit.Source = trimStringPtr(edit.Source)
	edit.ReviewStatus = entity.ProductionReviewDraft
	if edit.ProjectID == "" || edit.DisplayTitle == "" {
		return entity.BookMetadataTranslationEdit{}, entity.ErrInvalidProductionDraft
	}

	return uc.repo.SaveMetadataTranslationDraft(ctx, actorID, edit.ProjectID, edit, expected)
}

func (uc *UseCase) DeleteMetadataTranslationDraft(ctx context.Context, actorID, projectID string, expected *time.Time) error {
	return uc.repo.DeleteMetadataTranslationDraft(ctx, actorID, strings.TrimSpace(projectID), expected)
}

func (uc *UseCase) GetAuthorTranslationDraft(ctx context.Context, projectID string) (entity.AuthorTranslationEdit, error) {
	return uc.repo.GetAuthorTranslationDraft(ctx, strings.TrimSpace(projectID))
}

func (uc *UseCase) SaveAuthorTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.AuthorTranslationEdit,
	expected *time.Time,
) (entity.AuthorTranslationEdit, error) {
	edit.ProjectID = strings.TrimSpace(projectID)
	edit.Name = strings.TrimSpace(edit.Name)
	edit.Biography = trimStringPtr(edit.Biography)
	edit.DeathText = trimStringPtr(edit.DeathText)
	edit.Source = trimStringPtr(edit.Source)
	edit.ReviewStatus = entity.ProductionReviewDraft
	if edit.ProjectID == "" || edit.Name == "" {
		return entity.AuthorTranslationEdit{}, entity.ErrInvalidProductionDraft
	}

	return uc.repo.SaveAuthorTranslationDraft(ctx, actorID, edit.ProjectID, edit, expected)
}

func (uc *UseCase) DeleteAuthorTranslationDraft(ctx context.Context, actorID, projectID string, expected *time.Time) error {
	return uc.repo.DeleteAuthorTranslationDraft(ctx, actorID, strings.TrimSpace(projectID), expected)
}

func (uc *UseCase) GetCategoryTranslationDraft(ctx context.Context, projectID string) (entity.CategoryTranslationEdit, error) {
	return uc.repo.GetCategoryTranslationDraft(ctx, strings.TrimSpace(projectID))
}

func (uc *UseCase) SaveCategoryTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.CategoryTranslationEdit,
	expected *time.Time,
) (entity.CategoryTranslationEdit, error) {
	edit.ProjectID = strings.TrimSpace(projectID)
	edit.Name = strings.TrimSpace(edit.Name)
	edit.Source = trimStringPtr(edit.Source)
	edit.ReviewStatus = entity.ProductionReviewDraft
	if edit.ProjectID == "" || edit.Name == "" {
		return entity.CategoryTranslationEdit{}, entity.ErrInvalidProductionDraft
	}

	return uc.repo.SaveCategoryTranslationDraft(ctx, actorID, edit.ProjectID, edit, expected)
}

func (uc *UseCase) DeleteCategoryTranslationDraft(ctx context.Context, actorID, projectID string, expected *time.Time) error {
	return uc.repo.DeleteCategoryTranslationDraft(ctx, actorID, strings.TrimSpace(projectID), expected)
}

func (uc *UseCase) GetSectionTranslationDraft(
	ctx context.Context,
	projectID string,
	headingID int,
) (entity.SectionTranslationEdit, error) {
	if headingID <= 0 {
		return entity.SectionTranslationEdit{}, entity.ErrHeadingNotFound
	}

	return uc.repo.GetSectionTranslationDraft(ctx, strings.TrimSpace(projectID), headingID)
}

func (uc *UseCase) SaveSectionTranslationDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.SectionTranslationEdit,
	expected *time.Time,
) (entity.SectionTranslationEdit, error) {
	edit.ProjectID = strings.TrimSpace(projectID)
	edit.Title = trimStringPtr(edit.Title)
	edit.Content = strings.TrimSpace(edit.Content)
	edit.Source = trimStringPtr(edit.Source)
	edit.ReviewStatus = entity.ProductionReviewDraft
	if edit.ProjectID == "" || edit.HeadingID <= 0 || edit.Content == "" {
		return entity.SectionTranslationEdit{}, entity.ErrInvalidProductionDraft
	}

	return uc.repo.SaveSectionTranslationDraft(ctx, actorID, edit.ProjectID, edit, expected)
}

func (uc *UseCase) DeleteSectionTranslationDraft(ctx context.Context, actorID, projectID string, headingID int, expected *time.Time) error {
	if headingID <= 0 {
		return entity.ErrHeadingNotFound
	}

	return uc.repo.DeleteSectionTranslationDraft(ctx, actorID, strings.TrimSpace(projectID), headingID, expected)
}

func (uc *UseCase) GetHeadingSummaryDraft(
	ctx context.Context,
	projectID string,
	headingID int,
) (entity.HeadingSummaryEdit, error) {
	if headingID <= 0 {
		return entity.HeadingSummaryEdit{}, entity.ErrHeadingNotFound
	}

	return uc.repo.GetHeadingSummaryDraft(ctx, strings.TrimSpace(projectID), headingID)
}

func (uc *UseCase) SaveHeadingSummaryDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.HeadingSummaryEdit,
	expected *time.Time,
) (entity.HeadingSummaryEdit, error) {
	edit.ProjectID = strings.TrimSpace(projectID)
	edit.Summary = strings.TrimSpace(edit.Summary)
	edit.Source = trimStringPtr(edit.Source)
	edit.ReviewStatus = entity.ProductionReviewDraft
	if edit.ProjectID == "" || edit.HeadingID <= 0 || edit.Summary == "" {
		return entity.HeadingSummaryEdit{}, entity.ErrInvalidProductionDraft
	}

	return uc.repo.SaveHeadingSummaryDraft(ctx, actorID, edit.ProjectID, edit, expected)
}

func (uc *UseCase) DeleteHeadingSummaryDraft(ctx context.Context, actorID, projectID string, headingID int, expected *time.Time) error {
	if headingID <= 0 {
		return entity.ErrHeadingNotFound
	}

	return uc.repo.DeleteHeadingSummaryDraft(ctx, actorID, strings.TrimSpace(projectID), headingID, expected)
}

func (uc *UseCase) GetSectionAudioDraft(
	ctx context.Context,
	projectID string,
	headingID int,
) (entity.SectionAudioEdit, error) {
	if headingID <= 0 {
		return entity.SectionAudioEdit{}, entity.ErrHeadingNotFound
	}

	return uc.repo.GetSectionAudioDraft(ctx, strings.TrimSpace(projectID), headingID)
}

func (uc *UseCase) SaveSectionAudioDraft(
	ctx context.Context,
	actorID,
	projectID string,
	edit entity.SectionAudioEdit,
	expected *time.Time,
) (entity.SectionAudioEdit, error) {
	edit.ProjectID = strings.TrimSpace(projectID)
	edit.URL = strings.TrimSpace(edit.URL)
	edit.Narrator = trimStringPtr(edit.Narrator)
	edit.MIMEType = trimStringPtr(edit.MIMEType)
	edit.ReviewStatus = entity.ProductionReviewDraft
	if edit.ProjectID == "" || edit.HeadingID <= 0 || edit.URL == "" {
		return entity.SectionAudioEdit{}, entity.ErrInvalidProductionDraft
	}
	if edit.DurationSeconds != nil && *edit.DurationSeconds < 0 {
		return entity.SectionAudioEdit{}, entity.ErrInvalidProductionDraft
	}

	return uc.repo.SaveSectionAudioDraft(ctx, actorID, edit.ProjectID, edit, expected)
}

func (uc *UseCase) DeleteSectionAudioDraft(ctx context.Context, actorID, projectID string, headingID int, expected *time.Time) error {
	if headingID <= 0 {
		return entity.ErrHeadingNotFound
	}

	return uc.repo.DeleteSectionAudioDraft(ctx, actorID, strings.TrimSpace(projectID), headingID, expected)
}

// ReviewProductionAsset changes draft review status for one production asset.
func (uc *UseCase) ReviewProductionAsset(
	ctx context.Context,
	actorID,
	projectID,
	assetType string,
	headingID *int,
	decision string,
	note *string,
) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.ErrProductionProjectNotFound
	}

	assetType, err := entity.NormalizeProductionAssetType(assetType)
	if err != nil {
		return err
	}
	decision, err = entity.NormalizeProductionReviewDecision(decision)
	if err != nil {
		return err
	}
	note = trimStringPtr(note)

	if entity.IsHeadingProductionAsset(assetType) {
		if headingID == nil || *headingID <= 0 {
			return entity.ErrHeadingNotFound
		}
	} else {
		headingID = nil
	}

	return uc.repo.ReviewProductionAsset(ctx, actorID, projectID, assetType, headingID, decision, note)
}

func (uc *UseCase) PublishProductionProject(
	ctx context.Context,
	actorID,
	projectID string,
	expected *time.Time,
) (entity.BookProductionProject, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
	}

	return uc.repo.PublishProductionProject(ctx, actorID, projectID, expected)
}

func (uc *UseCase) UnpublishProductionProject(
	ctx context.Context,
	actorID,
	projectID string,
	expected *time.Time,
) (entity.BookProductionProject, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.BookProductionProject{}, entity.ErrProductionProjectNotFound
	}

	return uc.repo.UnpublishProductionProject(ctx, actorID, projectID, expected)
}

func (uc *UseCase) DeleteFinalProductionAsset(
	ctx context.Context,
	actorID,
	projectID,
	assetType string,
	headingID *int,
	reason *string,
) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return entity.ErrProductionProjectNotFound
	}

	assetType, err := entity.NormalizeProductionAssetType(assetType)
	if err != nil {
		return err
	}
	reason = trimStringPtr(reason)
	if entity.IsHeadingProductionAsset(assetType) {
		if headingID == nil || *headingID <= 0 {
			return entity.ErrHeadingNotFound
		}
	} else {
		headingID = nil
	}

	return uc.repo.DeleteFinalProductionAsset(ctx, actorID, projectID, assetType, headingID, reason)
}

func isProductionPublicationStatus(status string) bool {
	switch status {
	case entity.ProductionPublicationHidden,
		entity.ProductionPublicationPublished,
		entity.ProductionPublicationArchived:
		return true
	default:
		return false
	}
}

func clampActivityLimit(limit int) uint64 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxProductionActivityLimit {
		return maxProductionActivityLimit
	}

	return uint64(limit)
}

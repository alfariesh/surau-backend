package editorial

import (
	"context"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// QuranSourceLicenses returns the protected source inventory. The default is
// unresolved so newly imported needs_review sources are impossible to miss.
//
//nolint:wsl_v5 // normalization, validation, repository read, and non-nil envelope form one linear contract
func (uc *UseCase) QuranSourceLicenses(
	ctx context.Context,
	sourceKind, status string,
	limit, offset int,
) (entity.QuranSourceLicenseList, error) {
	if uc.quranLicense == nil {
		return entity.QuranSourceLicenseList{}, errLicenseRepoUnavailable
	}
	sourceKind = strings.TrimSpace(sourceKind)
	if sourceKind != "" && !validQuranSourceKind(sourceKind) {
		return entity.QuranSourceLicenseList{}, entity.ErrQuranSourceNotFound
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = entity.LicenseAuditStatusUnresolved
	}
	if status != entity.LicenseAuditStatusUnresolved && status != entity.LicenseAuditStatusAll &&
		!entity.IsValidLicenseStatus(status) {
		return entity.QuranSourceLicenseList{}, entity.ErrInvalidLicenseStatus
	}

	items, total, err := uc.quranLicense.ListQuranSourceLicenses(
		ctx, sourceKind, status, clampLimit(limit), clampLicenseAuditOffset(offset),
	)
	if err != nil {
		return entity.QuranSourceLicenseList{}, err
	}
	if items == nil {
		items = make([]entity.QuranSourceLicense, 0)
	}

	return entity.QuranSourceLicenseList{Items: items, Total: total}, nil
}

// QuranSourceLicense returns one source plus its append-only decision history.
//
//nolint:wsl_v5 // paired source identifiers are normalized and validated together
func (uc *UseCase) QuranSourceLicense(
	ctx context.Context,
	sourceKind, sourceID string,
) (entity.QuranSourceLicense, error) {
	if uc.quranLicense == nil {
		return entity.QuranSourceLicense{}, errLicenseRepoUnavailable
	}
	sourceKind, sourceID = strings.TrimSpace(sourceKind), strings.TrimSpace(sourceID)
	if !validQuranSourceKind(sourceKind) || sourceID == "" {
		return entity.QuranSourceLicense{}, entity.ErrQuranSourceNotFound
	}

	return uc.quranLicense.GetQuranSourceLicense(ctx, sourceKind, sourceID)
}

// UpdateQuranSourceLicense validates one evidence-backed, actor-attributed
// decision. PostgreSQL serializes the ETag check and appends the history row.
//
//nolint:wsl_v5 // normalization and validation intentionally remain adjacent to the audited repository call
func (uc *UseCase) UpdateQuranSourceLicense(
	ctx context.Context,
	actorID, sourceKind, sourceID, status, reason string,
	evidenceURL, translator, responsibleName, responsibleRole *string,
	expectedUpdatedAt *time.Time,
) (entity.QuranSourceLicense, error) {
	if uc.quranLicense == nil {
		return entity.QuranSourceLicense{}, errLicenseRepoUnavailable
	}
	sourceKind, sourceID = strings.TrimSpace(sourceKind), strings.TrimSpace(sourceID)
	if !validQuranSourceKind(sourceKind) || sourceID == "" {
		return entity.QuranSourceLicense{}, entity.ErrQuranSourceNotFound
	}
	status = strings.TrimSpace(status)
	if !entity.IsValidLicenseStatus(status) {
		return entity.QuranSourceLicense{}, entity.ErrInvalidLicenseStatus
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > licenseReasonMaxLength {
		return entity.QuranSourceLicense{}, entity.ErrInvalidLicenseReason
	}
	normalizedEvidenceURL, err := normalizeLicenseEvidenceURL(evidenceURL)
	if err != nil {
		return entity.QuranSourceLicense{}, err
	}

	return uc.quranLicense.UpdateQuranSourceLicense(ctx, strings.TrimSpace(actorID),
		entity.QuranSourceLicenseUpdate{
			SourceKind:      sourceKind,
			SourceID:        sourceID,
			LicenseStatus:   status,
			Reason:          reason,
			EvidenceURL:     normalizedEvidenceURL,
			Translator:      normalizedOptionalText(translator),
			ResponsibleName: normalizedOptionalText(responsibleName),
			ResponsibleRole: normalizedOptionalText(responsibleRole),
		}, expectedUpdatedAt)
}

func validQuranSourceKind(value string) bool {
	switch value {
	case entity.QuranSourceKindScript,
		entity.QuranSourceKindTranslation,
		entity.QuranSourceKindTransliteration:
		return true
	default:
		return false
	}
}

func normalizedOptionalText(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

package editorial

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
)

const (
	licenseReasonMaxLength = 2000
	licenseURLMaxLength    = 2048
	licenseAuditMaxOffset  = 10000
)

var errLicenseRepoUnavailable = errors.New("license repository is unavailable")

// LicenseAuditReport returns one paginated queue plus a complete coverage
// snapshot. An empty status deliberately means unresolved so the safest and
// most useful view is also the default.
func (uc *UseCase) LicenseAuditReport(
	ctx context.Context,
	status string,
	limit,
	offset int,
) (entity.BookLicenseAuditReport, error) {
	if uc.license == nil {
		return entity.BookLicenseAuditReport{}, errLicenseRepoUnavailable
	}

	status = strings.TrimSpace(status)
	if status == "" {
		status = entity.LicenseAuditStatusUnresolved
	}

	if status != entity.LicenseAuditStatusUnresolved &&
		status != entity.LicenseAuditStatusAll &&
		!entity.IsValidLicenseStatus(status) {
		return entity.BookLicenseAuditReport{}, entity.ErrInvalidLicenseStatus
	}

	items, total, counts, err := uc.license.ListBookLicenseAudit(ctx, repo.LicenseAuditFilter{
		Status: status,
		Limit:  clampLimit(limit),
		Offset: clampLicenseAuditOffset(offset),
	})
	if err != nil {
		return entity.BookLicenseAuditReport{}, err
	}

	if items == nil {
		items = make([]entity.BookLicenseAuditItem, 0)
	}

	return entity.BookLicenseAuditReport{
		Items:       items,
		Total:       total,
		Counts:      counts,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

// BookLicense returns the Edition-level status and the current grandfather
// decision used for its effective public visibility.
func (uc *UseCase) BookLicense(ctx context.Context, bookID int) (entity.BookLicense, error) {
	if uc.license == nil {
		return entity.BookLicense{}, errLicenseRepoUnavailable
	}

	if bookID <= 0 {
		return entity.BookLicense{}, entity.ErrBookNotFound
	}

	return uc.license.GetBookLicense(ctx, bookID)
}

// UpdateBookLicense validates and normalizes curator input before handing one
// atomic state+history transition to PostgreSQL. expectedUpdatedAt is nil only
// for an explicit If-Match: * request; the controller rejects a missing header.
func (uc *UseCase) UpdateBookLicense(
	ctx context.Context,
	actorID string,
	bookID int,
	status,
	reason string,
	evidenceURL *string,
	expectedUpdatedAt *time.Time,
) (entity.BookLicense, error) {
	if uc.license == nil {
		return entity.BookLicense{}, errLicenseRepoUnavailable
	}

	if bookID <= 0 {
		return entity.BookLicense{}, entity.ErrBookNotFound
	}

	status = strings.TrimSpace(status)
	if !entity.IsValidLicenseStatus(status) {
		return entity.BookLicense{}, entity.ErrInvalidLicenseStatus
	}

	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > licenseReasonMaxLength {
		return entity.BookLicense{}, entity.ErrInvalidLicenseReason
	}

	normalizedEvidenceURL, err := normalizeLicenseEvidenceURL(evidenceURL)
	if err != nil {
		return entity.BookLicense{}, err
	}

	return uc.license.UpdateBookLicense(ctx, strings.TrimSpace(actorID), entity.BookLicenseUpdate{
		BookID:        bookID,
		LicenseStatus: status,
		Reason:        reason,
		EvidenceURL:   normalizedEvidenceURL,
	}, expectedUpdatedAt)
}

func normalizeLicenseEvidenceURL(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}

	if len([]rune(trimmed)) > licenseURLMaxLength {
		return nil, entity.ErrInvalidLicenseEvidenceURL
	}

	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, entity.ErrInvalidLicenseEvidenceURL
	}

	return &trimmed, nil
}

func clampLicenseAuditOffset(offset int) uint64 {
	if offset <= 0 {
		return 0
	}

	if offset > licenseAuditMaxOffset {
		return licenseAuditMaxOffset
	}

	return uint64(offset)
}

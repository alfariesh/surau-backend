package editorial

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errLicenseDatabaseUnavailable = errors.New("database unavailable")

type fakeLicenseRepo struct {
	items          []entity.BookLicenseAuditItem
	total          int
	counts         entity.BookLicenseAuditCounts
	license        entity.BookLicense
	err            error
	listFilter     repo.LicenseAuditFilter
	actorID        string
	update         entity.BookLicenseUpdate
	expectedUpdate *time.Time
}

func (f *fakeLicenseRepo) ListBookLicenseAudit(
	_ context.Context,
	filter repo.LicenseAuditFilter,
) ([]entity.BookLicenseAuditItem, int, entity.BookLicenseAuditCounts, error) {
	f.listFilter = filter

	return f.items, f.total, f.counts, f.err
}

func (f *fakeLicenseRepo) GetBookLicense(_ context.Context, _ int) (entity.BookLicense, error) {
	return f.license, f.err
}

func (f *fakeLicenseRepo) UpdateBookLicense(
	_ context.Context,
	actorID string,
	update entity.BookLicenseUpdate,
	expectedUpdatedAt *time.Time,
) (entity.BookLicense, error) {
	f.actorID = actorID
	f.update = update
	f.expectedUpdate = expectedUpdatedAt

	return f.license, f.err
}

func TestLicenseAuditReportDefaultsAndClamps(t *testing.T) {
	t.Parallel()

	fake := &fakeLicenseRepo{
		total:  7,
		counts: entity.BookLicenseAuditCounts{Total: 10, Unresolved: 7},
	}
	uc := &UseCase{license: fake}

	report, err := uc.LicenseAuditReport(context.Background(), " ", 1000, 999999)
	require.NoError(t, err)
	assert.Empty(t, report.Items, "list envelope must encode [] instead of null")
	assert.Equal(t, 7, report.Total)
	assert.Equal(t, 10, report.Counts.Total)
	assert.False(t, report.GeneratedAt.IsZero())
	assert.Equal(t, repo.LicenseAuditFilter{
		Status: entity.LicenseAuditStatusUnresolved,
		Limit:  200,
		Offset: 10000,
	}, fake.listFilter)
}

func TestLicenseAuditReportAcceptsEveryFilter(t *testing.T) {
	t.Parallel()

	statuses := []string{
		entity.LicenseAuditStatusUnresolved,
		entity.LicenseAuditStatusAll,
		entity.LicenseStatusUnknown,
		entity.LicenseStatusNeedsReview,
		entity.LicenseStatusPermitted,
		entity.LicenseStatusRestricted,
		entity.LicenseStatusPublicDomain,
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			fake := &fakeLicenseRepo{}
			uc := &UseCase{license: fake}
			_, err := uc.LicenseAuditReport(context.Background(), "  "+status+" ", 0, -1)
			require.NoError(t, err)
			assert.Equal(t, status, fake.listFilter.Status)
			assert.Equal(t, uint64(50), fake.listFilter.Limit)
			assert.Zero(t, fake.listFilter.Offset)
		})
	}
}

func TestLicenseAuditReportRejectsInvalidFilter(t *testing.T) {
	t.Parallel()

	uc := &UseCase{license: &fakeLicenseRepo{}}
	_, err := uc.LicenseAuditReport(context.Background(), "copyrighted", 50, 0)
	assert.ErrorIs(t, err, entity.ErrInvalidLicenseStatus)
}

func TestBookLicenseRejectsInvalidBookID(t *testing.T) {
	t.Parallel()

	uc := &UseCase{license: &fakeLicenseRepo{}}
	_, err := uc.BookLicense(context.Background(), 0)
	assert.ErrorIs(t, err, entity.ErrBookNotFound)
}

func TestUpdateBookLicenseNormalizesEvidenceBackedDecision(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	evidence := "  https://example.org/license/797  "
	fake := &fakeLicenseRepo{license: entity.BookLicense{BookID: 797, UpdatedAt: updatedAt}}
	uc := &UseCase{license: fake}

	got, err := uc.UpdateBookLicense(
		context.Background(),
		" actor-id ",
		797,
		" permitted ",
		"  Permission received from publisher. ",
		&evidence,
		&updatedAt,
	)
	require.NoError(t, err)
	assert.Equal(t, 797, got.BookID)
	assert.Equal(t, "actor-id", fake.actorID)
	assert.Equal(t, entity.BookLicenseUpdate{
		BookID:        797,
		LicenseStatus: entity.LicenseStatusPermitted,
		Reason:        "Permission received from publisher.",
		EvidenceURL:   new("https://example.org/license/797"),
	}, fake.update)
	assert.Equal(t, &updatedAt, fake.expectedUpdate)
}

func TestUpdateBookLicenseValidatesInput(t *testing.T) {
	t.Parallel()

	longReason := make([]rune, licenseReasonMaxLength+1)
	for index := range longReason {
		longReason[index] = 'a'
	}

	tests := []struct {
		name        string
		bookID      int
		status      string
		reason      string
		evidence    *string
		expectedErr error
	}{
		{name: "book", bookID: 0, status: entity.LicenseStatusPermitted, reason: "ok", expectedErr: entity.ErrBookNotFound},
		{name: "status", bookID: 1, status: "copyrighted", reason: "ok", expectedErr: entity.ErrInvalidLicenseStatus},
		{name: "blank reason", bookID: 1, status: entity.LicenseStatusPermitted, reason: " ", expectedErr: entity.ErrInvalidLicenseReason},
		{name: "long reason", bookID: 1, status: entity.LicenseStatusPermitted, reason: string(longReason), expectedErr: entity.ErrInvalidLicenseReason},
		{name: "relative evidence", bookID: 1, status: entity.LicenseStatusPermitted, reason: "ok", evidence: new("/permission"), expectedErr: entity.ErrInvalidLicenseEvidenceURL},
		{name: "non-http evidence", bookID: 1, status: entity.LicenseStatusPermitted, reason: "ok", evidence: new("file:///tmp/license"), expectedErr: entity.ErrInvalidLicenseEvidenceURL},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			uc := &UseCase{license: &fakeLicenseRepo{}}
			_, err := uc.UpdateBookLicense(
				context.Background(), "actor", test.bookID, test.status, test.reason, test.evidence, nil,
			)
			assert.ErrorIs(t, err, test.expectedErr)
		})
	}
}

func TestLicenseAuditPropagatesRepositoryError(t *testing.T) {
	t.Parallel()

	want := errLicenseDatabaseUnavailable
	uc := &UseCase{license: &fakeLicenseRepo{err: want}}
	_, err := uc.LicenseAuditReport(context.Background(), "all", 1, 0)
	assert.ErrorIs(t, err, want)
}

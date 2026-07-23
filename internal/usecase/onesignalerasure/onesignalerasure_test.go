package onesignalerasure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAppID      = "7a650cae-1c1e-4b19-a7fe-393c14b894f0"
	testExternalID = "11111111-1111-4111-8111-111111111111"
	testSecret     = "01234567890123456789012345678901"
	testJWT        = "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiIxIn0.aaaaaaaaaaaaaaaa"
)

var errTestProviderTimeout = errors.New("provider timeout")

type erasureRepoStub struct {
	claimed       []entity.OneSignalErasure
	attempts      []entity.OneSignalErasureAttempt
	cleanupBefore time.Time
	cleanupCount  int64
}

func (s *erasureRepoStub) ClaimDueOneSignalErasures(
	context.Context,
	time.Time,
	string,
	time.Time,
	int,
) ([]entity.OneSignalErasure, error) {
	return append([]entity.OneSignalErasure(nil), s.claimed...), nil
}

func (s *erasureRepoStub) RecordOneSignalErasureAttempt(
	_ context.Context,
	attempt *entity.OneSignalErasureAttempt,
) error {
	s.attempts = append(s.attempts, *attempt)

	return nil
}

func (s *erasureRepoStub) CleanupVerifiedOneSignalErasures(
	_ context.Context,
	before time.Time,
) (int64, error) {
	s.cleanupBefore = before

	return s.cleanupCount, nil
}

type erasureProviderStub struct {
	deleteResult entity.OneSignalErasureProviderResult
	deleteErr    error
	viewResult   entity.OneSignalErasureProviderResult
	viewErr      error
	deletedID    string
	viewedID     string
}

func (s *erasureProviderStub) DeleteUser(
	_ context.Context,
	externalID string,
) (entity.OneSignalErasureProviderResult, error) {
	s.deletedID = externalID

	return s.deleteResult, s.deleteErr
}

func (s *erasureProviderStub) ViewUser(
	_ context.Context,
	externalID string,
) (entity.OneSignalErasureProviderResult, error) {
	s.viewedID = externalID

	return s.viewResult, s.viewErr
}

func TestPrepareEncryptsIdentifierAndKeepsStableAuditHash(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	uc := newTestUseCase(t, &erasureRepoStub{}, &erasureProviderStub{}, now)

	first, err := uc.Prepare(testExternalID)
	require.NoError(t, err)
	second, err := uc.Prepare(testExternalID)
	require.NoError(t, err)

	assert.Equal(t, testAppID, first.AppID)
	assert.Equal(t, now, first.NextAttemptAt)
	assert.NotEqual(t, testExternalID, first.ExternalIDCiphertext)
	assert.NotEqual(t, first.ExternalIDCiphertext, second.ExternalIDCiphertext)
	assert.Equal(t, first.ExternalIDHash, second.ExternalIDHash)
	assert.Len(t, first.ExternalIDHash, 64)
	assert.NotContains(t, first.ExternalIDHash, testExternalID)

	_, err = uc.Prepare("AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA")
	require.ErrorIs(t, err, errInvalidExternalID)
}

func TestDispatchSurvivesRestartFromDeleteAcceptedToVerified(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	firstRepo := &erasureRepoStub{}
	firstProvider := &erasureProviderStub{deleteResult: entity.OneSignalErasureProviderResult{
		HTTPStatus: 202, ReasonCode: "delete_accepted", Accepted: true,
		ReasonDetail: testExternalID + " " + testJWT,
	}}
	firstUC := newTestUseCase(t, firstRepo, firstProvider, now)
	prepared, err := firstUC.Prepare(testExternalID)
	require.NoError(t, err)

	firstRepo.claimed = []entity.OneSignalErasure{{
		ID: prepared.ID, AppID: prepared.AppID,
		ExternalIDCiphertext: prepared.ExternalIDCiphertext,
		ExternalIDHash:       prepared.ExternalIDHash, Status: entity.OneSignalErasureStatusPending,
		LeaseToken: "lease-one",
	}}

	report, err := firstUC.Dispatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, report.Claimed)
	require.Len(t, firstRepo.attempts, 1)
	firstAttempt := firstRepo.attempts[0]

	assert.Equal(t, testExternalID, firstProvider.deletedID)
	assert.Equal(t, entity.OneSignalErasureStatusVerifying, firstAttempt.Status)
	assert.Equal(t, "delete", firstAttempt.Operation)
	assert.Equal(t, "accepted", firstAttempt.ProviderCallOutcome)
	assert.NotContains(t, firstAttempt.ReasonDetail, testExternalID)
	assert.NotContains(t, firstAttempt.ReasonDetail, testJWT)
	require.NotNil(t, firstAttempt.AcceptedAt)

	secondRepo := &erasureRepoStub{}
	secondProvider := &erasureProviderStub{viewResult: entity.OneSignalErasureProviderResult{
		HTTPStatus: 404, ReasonCode: "not_found", NotFound: true,
	}}
	secondUC := newTestUseCase(t, secondRepo, secondProvider, now.Add(time.Minute))
	secondRepo.claimed = []entity.OneSignalErasure{{
		ID: prepared.ID, AppID: prepared.AppID,
		ExternalIDCiphertext: prepared.ExternalIDCiphertext,
		ExternalIDHash:       prepared.ExternalIDHash, Status: entity.OneSignalErasureStatusVerifying,
		LeaseToken: "lease-two", AcceptedAt: firstAttempt.AcceptedAt,
	}}

	report, err = secondUC.Dispatch(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Verified)
	require.Len(t, secondRepo.attempts, 1)
	secondAttempt := secondRepo.attempts[0]

	assert.Equal(t, testExternalID, secondProvider.viewedID)
	assert.Equal(t, entity.OneSignalErasureStatusVerified, secondAttempt.Status)
	assert.True(t, secondAttempt.ClearExternalID)
	assert.Equal(t, "not_found", secondAttempt.ProviderCallOutcome)
	require.NotNil(t, secondAttempt.VerifiedAt)
}

func TestDispatchKeepsVerifyingWhileViewUserExistsThenFinishesOn404(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	existsRepo := &erasureRepoStub{}
	existsProvider := &erasureProviderStub{viewResult: entity.OneSignalErasureProviderResult{
		HTTPStatus: 200, ReasonCode: "user_exists", Accepted: true,
	}}
	existsUC := newTestUseCase(t, existsRepo, existsProvider, now)
	prepared, err := existsUC.Prepare(testExternalID)
	require.NoError(t, err)

	acceptedAt := now.Add(-time.Minute)
	existsRepo.claimed = []entity.OneSignalErasure{{
		ID: prepared.ID, AppID: prepared.AppID,
		ExternalIDCiphertext: prepared.ExternalIDCiphertext,
		ExternalIDHash:       prepared.ExternalIDHash,
		Status:               entity.OneSignalErasureStatusVerifying,
		LeaseToken:           "lease-exists",
		AcceptedAt:           &acceptedAt,
	}}

	report, err := existsUC.Dispatch(t.Context())
	require.NoError(t, err)
	assert.Zero(t, report.Verified)
	require.Len(t, existsRepo.attempts, 1)
	existsAttempt := existsRepo.attempts[0]
	assert.Equal(t, entity.OneSignalErasureStatusVerifying, existsAttempt.Status)
	assert.Equal(t, "exists", existsAttempt.ProviderCallOutcome)
	assert.False(t, existsAttempt.ClearExternalID)
	assert.Equal(t, now.Add(30*time.Second), existsAttempt.NextAttemptAt)

	absentRepo := &erasureRepoStub{}
	absentProvider := &erasureProviderStub{viewResult: entity.OneSignalErasureProviderResult{
		HTTPStatus: 404, ReasonCode: "not_found", NotFound: true,
	}}
	absentUC := newTestUseCase(t, absentRepo, absentProvider, now.Add(30*time.Second))
	absentRepo.claimed = []entity.OneSignalErasure{{
		ID: prepared.ID, AppID: prepared.AppID,
		ExternalIDCiphertext: prepared.ExternalIDCiphertext,
		ExternalIDHash:       prepared.ExternalIDHash,
		Status:               entity.OneSignalErasureStatusVerifying,
		LeaseToken:           "lease-absent",
		AcceptedAt:           &acceptedAt,
	}}

	report, err = absentUC.Dispatch(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Verified)
	require.Len(t, absentRepo.attempts, 1)
	assert.True(t, absentRepo.attempts[0].ClearExternalID)
}

func TestDispatchRetriesProviderFailuresWithoutDiscardingWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	repo := &erasureRepoStub{}
	providerErr := fmt.Errorf("%w: %s %s", errTestProviderTimeout, testExternalID, testJWT)
	provider := &erasureProviderStub{
		deleteErr: providerErr,
		deleteResult: entity.OneSignalErasureProviderResult{
			ReasonCode: "rate_limited", Retryable: true, Systemic: true,
			RetryAfter: time.Hour,
		},
	}
	uc := newTestUseCase(t, repo, provider, now)
	prepared, err := uc.Prepare(testExternalID)
	require.NoError(t, err)

	repo.claimed = []entity.OneSignalErasure{{
		ID: prepared.ID, AppID: prepared.AppID,
		ExternalIDCiphertext: prepared.ExternalIDCiphertext,
		ExternalIDHash:       prepared.ExternalIDHash,
		Status:               entity.OneSignalErasureStatusPending, LeaseToken: "lease",
	}}

	_, err = uc.Dispatch(t.Context())
	require.ErrorIs(t, err, providerErr)
	assert.NotContains(t, err.Error(), testExternalID)
	assert.NotContains(t, err.Error(), testJWT)
	require.Len(t, repo.attempts, 1)
	attempt := repo.attempts[0]
	assert.Equal(t, entity.OneSignalErasureStatusPending, attempt.Status)
	assert.Equal(t, now.Add(15*time.Minute), attempt.NextAttemptAt)
	assert.False(t, attempt.ClearExternalID)
	assert.Equal(t, "failed", attempt.ProviderCallOutcome)
	assert.Equal(t, now.Add(-90*24*time.Hour), repo.cleanupBefore)
}

func TestDispatchRejectsCiphertextSwappedBetweenAuditRows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	erasureRepo := &erasureRepoStub{}
	provider := &erasureProviderStub{}
	uc := newTestUseCase(t, erasureRepo, provider, now)
	prepared, err := uc.Prepare(testExternalID)
	require.NoError(t, err)

	erasureRepo.claimed = []entity.OneSignalErasure{{
		ID: prepared.ID, AppID: prepared.AppID,
		ExternalIDCiphertext: prepared.ExternalIDCiphertext,
		ExternalIDHash:       strings.Repeat("f", 64),
		Status:               entity.OneSignalErasureStatusPending,
		LeaseToken:           "lease",
	}}

	_, err = uc.Dispatch(t.Context())

	require.ErrorIs(t, err, errCiphertextBinding)
	assert.Empty(t, provider.deletedID)
	require.Len(t, erasureRepo.attempts, 1)
	assert.Equal(t, "ciphertext_binding_mismatch", erasureRepo.attempts[0].ReasonCode)
}

func TestDispatchKeepsInvalidProviderDataVisibleAndStopsPassOnAuthorizationFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		result      entity.OneSignalErasureProviderResult
		wantPassErr bool
	}{
		{
			name: "bad request stays queued without becoming terminal",
			result: entity.OneSignalErasureProviderResult{
				HTTPStatus: 400, ReasonCode: "invalid_request",
			},
		},
		{
			name: "unauthorized stops the supervised pass",
			result: entity.OneSignalErasureProviderResult{
				HTTPStatus: 401, ReasonCode: "unauthorized", Retryable: true, Systemic: true,
			},
			wantPassErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			erasureRepo := &erasureRepoStub{}
			provider := &erasureProviderStub{deleteResult: tc.result}
			uc := newTestUseCase(t, erasureRepo, provider, now)
			prepared, err := uc.Prepare(testExternalID)
			require.NoError(t, err)

			erasureRepo.claimed = []entity.OneSignalErasure{{
				ID: prepared.ID, AppID: prepared.AppID,
				ExternalIDCiphertext: prepared.ExternalIDCiphertext,
				ExternalIDHash:       prepared.ExternalIDHash,
				Status:               entity.OneSignalErasureStatusPending,
				LeaseToken:           "lease",
			}}

			_, err = uc.Dispatch(t.Context())
			if tc.wantPassErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Len(t, erasureRepo.attempts, 1)
			assert.Equal(t, entity.OneSignalErasureStatusPending, erasureRepo.attempts[0].Status)
			assert.Equal(t, "failed", erasureRepo.attempts[0].ProviderCallOutcome)
		})
	}
}

func newTestUseCase(
	t *testing.T,
	repo *erasureRepoStub,
	provider *erasureProviderStub,
	now time.Time,
) *UseCase {
	t.Helper()

	uc, err := New(repo, provider, &Options{
		AppID: testAppID, Secret: testSecret, BatchSize: 10,
		LeaseDuration: 2 * time.Minute, VerificationDelay: 30 * time.Second,
		Retention: 90 * 24 * time.Hour, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	return uc
}

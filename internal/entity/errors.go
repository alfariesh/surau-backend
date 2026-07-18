package entity

import (
	"errors"
	"time"
)

var (
	ErrUserNotFound                     = errors.New("user not found")
	ErrUserAlreadyExists                = errors.New("user already exists")
	ErrInvalidAuthInput                 = errors.New("invalid auth input")
	ErrInvalidCredentials               = errors.New("invalid credentials")
	ErrEmailNotVerified                 = errors.New("email not verified")
	ErrInvalidUserPreference            = errors.New("invalid user preference")
	ErrInvalidVerificationToken         = errors.New("invalid verification token")
	ErrVerificationTokenNotFound        = errors.New("verification token not found")
	ErrEmailDeliveryFailed              = errors.New("email delivery failed")
	ErrAuthRateLimited                  = errors.New("auth rate limited")
	ErrTokenRevoked                     = errors.New("token revoked")
	ErrVerificationRateLimited          = errors.New("verification email rate limited")
	ErrInvalidPasswordResetToken        = errors.New("invalid password reset token")
	ErrPasswordResetTokenNotFound       = errors.New("password reset token not found")
	ErrPasswordResetRateLimited         = errors.New("password reset rate limited")
	ErrInvalidEmailChangeToken          = errors.New("invalid email change token")
	ErrInvalidRefreshToken              = errors.New("invalid refresh token")
	ErrRefreshSessionExpired            = errors.New("refresh session expired")
	ErrAuthSessionNotFound              = errors.New("auth session not found")
	ErrAccountLocked                    = errors.New("account temporarily locked")
	ErrLastAdmin                        = errors.New("cannot demote the last admin")
	ErrSelfRoleChange                   = errors.New("admins cannot change their own role")
	ErrMFAAlreadyEnabled                = errors.New("mfa already enabled")
	ErrMFANotEnabled                    = errors.New("mfa not enabled")
	ErrMFAEnrollmentNotStarted          = errors.New("mfa enrollment not started")
	ErrInvalidMFACode                   = errors.New("invalid mfa code")
	ErrInvalidMFAChallenge              = errors.New("invalid mfa challenge")
	ErrInvalidMFAReset                  = errors.New("invalid mfa reset")
	ErrMFAStepUpRequired                = errors.New("mfa step-up required")
	ErrMFAEnrollmentRequired            = errors.New("mfa enrollment required")
	ErrEmailChangeTokenNotFound         = errors.New("email change token not found")
	ErrEmailChangeRateLimited           = errors.New("email change rate limited")
	ErrEmailPermanentBounce             = errors.New("email permanent bounce")
	ErrEmailTemplateNotFound            = errors.New("email template not found")
	ErrEmailTemplateVersionNotFound     = errors.New("email template version not found")
	ErrEmailEventSettingNotFound        = errors.New("email event setting not found")
	ErrEmailMessageNotFound             = errors.New("email message not found")
	ErrEmailMessageNotResendable        = errors.New("email message not resendable")
	ErrEmailRecipientSuppressed         = errors.New("email recipient suppressed")
	ErrEmailProviderPollCursorNotFound  = errors.New("email provider poll cursor not found")
	ErrEmailSubscriptionNotFound        = errors.New("email subscription not found")
	ErrEmailCampaignNotFound            = errors.New("email campaign not found")
	ErrEmailSuppressionNotFound         = errors.New("email suppression not found")
	ErrInvalidEmailTemplate             = errors.New("invalid email template")
	ErrInvalidEmailCampaign             = errors.New("invalid email campaign")
	ErrInvalidUnsubscribeToken          = errors.New("invalid unsubscribe token")
	ErrTaskNotFound                     = errors.New("task not found")
	ErrTaskForbidden                    = errors.New("task does not belong to user")
	ErrInvalidTransition                = errors.New("invalid status transition")
	ErrBookNotFound                     = errors.New("book not found")
	ErrPageNotFound                     = errors.New("page not found")
	ErrHeadingNotFound                  = errors.New("heading not found")
	ErrTranslationNotFound              = errors.New("translation not found")
	ErrUnsupportedLanguage              = errors.New("unsupported language")
	ErrFeedbackNotFound                 = errors.New("feedback not found")
	ErrSavedItemNotFound                = errors.New("saved item not found")
	ErrProgressNotFound                 = errors.New("reading progress not found")
	ErrInvalidReaderLocation            = errors.New("invalid reader location")
	ErrInvalidSavedItem                 = errors.New("invalid saved item")
	ErrInvalidFeedback                  = errors.New("invalid feedback")
	ErrInvalidQuestion                  = errors.New("invalid question")
	ErrRAGNotConfigured                 = errors.New("rag llm is not configured")
	ErrRAGEvidenceNotFound              = errors.New("rag evidence not found")
	ErrRAGUnitMaterializationIncomplete = errors.New("rag unit materialization incomplete")
	ErrRAGUnitMaterializationStale      = errors.New("rag unit materialization stale")
	ErrForbidden                        = errors.New("forbidden")
	ErrPreconditionFailed               = errors.New("precondition failed")
	ErrPreconditionRequired             = errors.New("precondition required")
	ErrInvalidRole                      = errors.New("invalid role")
	ErrInvalidStatus                    = errors.New("invalid status")
	ErrInvalidLicenseStatus             = errors.New("invalid license status")
	ErrInvalidLicenseReason             = errors.New("invalid license reason")
	ErrInvalidLicenseEvidenceURL        = errors.New("invalid license evidence url")
	ErrLicenseNotPermitted              = errors.New("license status does not permit publication")
	ErrInvalidAssetType                 = errors.New("invalid asset type")
	ErrDraftNotFound                    = errors.New("draft not found")
	ErrProductionProjectNotFound        = errors.New("production project not found")
	ErrProductionProjectExists          = errors.New("production project already exists")
	ErrProductionNotReady               = errors.New("production project is not ready")
	ErrInvalidReviewDecision            = errors.New("invalid review decision")
	ErrInvalidProductionDraft           = errors.New("invalid production draft")
	ErrQuranSurahNotFound               = errors.New("quran surah not found")
	ErrQuranAyahNotFound                = errors.New("quran ayah not found")
	ErrQuranNavigationNotFound          = errors.New("quran navigation not found")
	ErrQuranRecitationNotFound          = errors.New("quran recitation not found")
	ErrQuranTranslationSourceNotFound   = errors.New("quran translation source not found")
	ErrQuranSourceNotFound              = errors.New("quran source not found")
	ErrInvalidQuranSourceAttribution    = errors.New("invalid Quran source attribution")
	ErrInvalidAyahKey                   = errors.New("invalid ayah key")
	ErrInvalidQuranRange                = errors.New("invalid quran range")
	ErrInvalidQuranProgress             = errors.New("invalid quran progress")
	ErrInvalidQuranEditorial            = errors.New("invalid quran editorial")
	ErrInvalidQuranSlug                 = errors.New("invalid quran slug")
	ErrQuranSlugNotFound                = errors.New("quran slug not found")
	ErrInvalidQuranPageType             = errors.New("invalid quran page type")
	ErrEditorialUnavailable             = errors.New("editorial workflow unavailable")
	ErrInvalidReadingProgress           = errors.New("invalid reading progress")
	ErrKhatamCycleNotFound              = errors.New("khatam cycle not found")
	ErrKhatamCycleActiveExists          = errors.New("active khatam cycle already exists")
	ErrKhatamCycleIncomplete            = errors.New("khatam cycle incomplete")
	ErrInvalidJuzNumber                 = errors.New("invalid juz number")
	ErrInvalidSyncSince                 = errors.New("invalid sync since")
	ErrInvalidActivityDate              = errors.New("invalid activity date")
	ErrInvalidActivityRange             = errors.New("invalid activity range")
	ErrPushDeliveryFailed               = errors.New("push delivery failed")
	ErrUnitNotFound                     = errors.New("citable unit not found")
	ErrUnitReconcileConflict            = errors.New("unit registry changed since plan was built")
	ErrQuranPrimaryTextDrift            = errors.New("quran primary text changed after Citable Unit mint")
	ErrInvalidQuranFootnotes            = errors.New("invalid Quran translation footnotes")
	ErrInvalidAnchor                    = errors.New("invalid anchor")
	ErrAnchorNotFound                   = errors.New("anchor not found")
	ErrAnchorLineageCycle               = errors.New("anchor lineage cycle detected")
	ErrCrossReferenceNotFound           = errors.New("cross-reference not found")
	ErrInvalidCrossReference            = errors.New("invalid cross-reference")
	ErrCrossReferenceConflict           = errors.New("cross-reference origin already exists")
	ErrGenerationRunNotFound            = errors.New("generation run not found")
	ErrInvalidGenerationRun             = errors.New("invalid generation run")
	ErrGenerationRunConflict            = errors.New("generation run descriptor conflicts with registered run")
	ErrServicePrincipalNotFound         = errors.New("service identity not found")
	ErrInvalidServicePrincipal          = errors.New("invalid service identity")
	ErrInvalidServiceScope              = errors.New("invalid service scope")
	ErrServicePrincipalRevoked          = errors.New("service identity revoked")
	ErrServiceTokenNotFound             = errors.New("service token not found")
	ErrInvalidServiceToken              = errors.New("invalid service token")
	ErrInsufficientServiceScope         = errors.New("insufficient service scope")
	ErrServiceIdentityUnavailable       = errors.New("service identity unavailable")
)

// AuthRateLimitedError carries the retry-after hint computed by the rate
// limiter so transports can surface it (Retry-After header / retry_after
// field). errors.Is(err, ErrAuthRateLimited) keeps matching via Unwrap.
type AuthRateLimitedError struct {
	RetryAfter time.Duration
}

func (e *AuthRateLimitedError) Error() string {
	return ErrAuthRateLimited.Error()
}

func (e *AuthRateLimitedError) Unwrap() error {
	return ErrAuthRateLimited
}

// ProductionProjectExistsError carries the active project that blocks a duplicate create.
type ProductionProjectExistsError struct {
	ExistingProjectID string
}

func (e *ProductionProjectExistsError) Error() string {
	return ErrProductionProjectExists.Error()
}

func (e *ProductionProjectExistsError) Unwrap() error {
	return ErrProductionProjectExists
}

// NewProductionProjectExistsError keeps generic conflict behavior when the ID cannot be resolved.
func NewProductionProjectExistsError(existingProjectID string) error {
	if existingProjectID == "" {
		return ErrProductionProjectExists
	}

	return &ProductionProjectExistsError{ExistingProjectID: existingProjectID}
}

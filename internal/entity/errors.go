package entity

import "errors"

var (
	ErrUserNotFound                   = errors.New("user not found")
	ErrUserAlreadyExists              = errors.New("user already exists")
	ErrInvalidAuthInput               = errors.New("invalid auth input")
	ErrInvalidCredentials             = errors.New("invalid credentials")
	ErrEmailNotVerified               = errors.New("email not verified")
	ErrInvalidUserPreference          = errors.New("invalid user preference")
	ErrInvalidVerificationToken       = errors.New("invalid verification token")
	ErrVerificationTokenNotFound      = errors.New("verification token not found")
	ErrEmailDeliveryFailed            = errors.New("email delivery failed")
	ErrAuthRateLimited                = errors.New("auth rate limited")
	ErrTokenRevoked                   = errors.New("token revoked")
	ErrVerificationRateLimited        = errors.New("verification email rate limited")
	ErrInvalidPasswordResetToken      = errors.New("invalid password reset token")
	ErrPasswordResetTokenNotFound     = errors.New("password reset token not found")
	ErrPasswordResetRateLimited       = errors.New("password reset rate limited")
	ErrInvalidEmailChangeToken        = errors.New("invalid email change token")
	ErrEmailChangeTokenNotFound       = errors.New("email change token not found")
	ErrEmailChangeRateLimited         = errors.New("email change rate limited")
	ErrEmailPermanentBounce           = errors.New("email permanent bounce")
	ErrEmailTemplateNotFound          = errors.New("email template not found")
	ErrEmailTemplateVersionNotFound   = errors.New("email template version not found")
	ErrEmailEventSettingNotFound      = errors.New("email event setting not found")
	ErrEmailMessageNotFound           = errors.New("email message not found")
	ErrEmailSubscriptionNotFound      = errors.New("email subscription not found")
	ErrEmailCampaignNotFound          = errors.New("email campaign not found")
	ErrEmailSuppressionNotFound       = errors.New("email suppression not found")
	ErrInvalidEmailTemplate           = errors.New("invalid email template")
	ErrInvalidEmailCampaign           = errors.New("invalid email campaign")
	ErrInvalidUnsubscribeToken        = errors.New("invalid unsubscribe token")
	ErrTaskNotFound                   = errors.New("task not found")
	ErrTaskForbidden                  = errors.New("task does not belong to user")
	ErrInvalidTransition              = errors.New("invalid status transition")
	ErrBookNotFound                   = errors.New("book not found")
	ErrPageNotFound                   = errors.New("page not found")
	ErrHeadingNotFound                = errors.New("heading not found")
	ErrTranslationNotFound            = errors.New("translation not found")
	ErrUnsupportedLanguage            = errors.New("unsupported language")
	ErrFeedbackNotFound               = errors.New("feedback not found")
	ErrSavedItemNotFound              = errors.New("saved item not found")
	ErrProgressNotFound               = errors.New("reading progress not found")
	ErrInvalidReaderLocation          = errors.New("invalid reader location")
	ErrInvalidSavedItem               = errors.New("invalid saved item")
	ErrInvalidFeedback                = errors.New("invalid feedback")
	ErrInvalidQuestion                = errors.New("invalid question")
	ErrRAGNotConfigured               = errors.New("rag llm is not configured")
	ErrRAGEvidenceNotFound            = errors.New("rag evidence not found")
	ErrForbidden                      = errors.New("forbidden")
	ErrInvalidRole                    = errors.New("invalid role")
	ErrInvalidStatus                  = errors.New("invalid status")
	ErrInvalidAssetType               = errors.New("invalid asset type")
	ErrDraftNotFound                  = errors.New("draft not found")
	ErrProductionProjectNotFound      = errors.New("production project not found")
	ErrProductionProjectExists        = errors.New("production project already exists")
	ErrProductionNotReady             = errors.New("production project is not ready")
	ErrInvalidReviewDecision          = errors.New("invalid review decision")
	ErrInvalidProductionDraft         = errors.New("invalid production draft")
	ErrQuranSurahNotFound             = errors.New("quran surah not found")
	ErrQuranAyahNotFound              = errors.New("quran ayah not found")
	ErrQuranNavigationNotFound        = errors.New("quran navigation not found")
	ErrQuranRecitationNotFound        = errors.New("quran recitation not found")
	ErrQuranTranslationSourceNotFound = errors.New("quran translation source not found")
	ErrInvalidAyahKey                 = errors.New("invalid ayah key")
	ErrInvalidQuranRange              = errors.New("invalid quran range")
	ErrInvalidQuranProgress           = errors.New("invalid quran progress")
)

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

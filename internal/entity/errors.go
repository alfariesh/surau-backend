package entity

import "errors"

var (
	ErrUserNotFound          = errors.New("user not found")
	ErrUserAlreadyExists     = errors.New("user already exists")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrTaskNotFound          = errors.New("task not found")
	ErrTaskForbidden         = errors.New("task does not belong to user")
	ErrInvalidTransition     = errors.New("invalid status transition")
	ErrBookNotFound          = errors.New("book not found")
	ErrPageNotFound          = errors.New("page not found")
	ErrHeadingNotFound       = errors.New("heading not found")
	ErrTranslationNotFound   = errors.New("translation not found")
	ErrFeedbackNotFound      = errors.New("feedback not found")
	ErrBookmarkNotFound      = errors.New("bookmark not found")
	ErrProgressNotFound      = errors.New("reading progress not found")
	ErrInvalidReaderLocation = errors.New("invalid reader location")
	ErrInvalidFeedback       = errors.New("invalid feedback")
	ErrInvalidQuestion       = errors.New("invalid question")
	ErrRAGNotConfigured      = errors.New("rag llm is not configured")
	ErrRAGEvidenceNotFound   = errors.New("rag evidence not found")
	ErrForbidden             = errors.New("forbidden")
	ErrInvalidRole           = errors.New("invalid role")
	ErrInvalidStatus         = errors.New("invalid status")
	ErrDraftNotFound         = errors.New("draft not found")
)

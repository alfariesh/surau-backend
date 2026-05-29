package v1

import (
	"errors"

	"github.com/evrone/go-clean-template/internal/entity"
)

func (r *V1) logReaderError(err error, operation string) {
	if isExpectedReaderError(err) {
		r.l.Warn("%s: %s", operation, err)
		return
	}

	r.l.Error(err, operation)
}

func (r *V1) logEditorialError(err error, operation string) {
	if isExpectedEditorialError(err) {
		r.l.Warn("%s: %s", operation, err)
		return
	}

	r.l.Error(err, operation)
}

func (r *V1) logQuranError(err error, operation string) {
	if isExpectedQuranError(err) {
		r.l.Warn("%s: %s", operation, err)
		return
	}

	r.l.Error(err, operation)
}

func isExpectedReaderError(err error) bool {
	return errors.Is(err, entity.ErrUnsupportedLanguage) ||
		errors.Is(err, entity.ErrBookNotFound) ||
		errors.Is(err, entity.ErrPageNotFound) ||
		errors.Is(err, entity.ErrHeadingNotFound) ||
		errors.Is(err, entity.ErrTranslationNotFound) ||
		errors.Is(err, entity.ErrInvalidFeedback) ||
		errors.Is(err, entity.ErrInvalidQuestion) ||
		errors.Is(err, entity.ErrRAGEvidenceNotFound)
}

func isExpectedEditorialError(err error) bool {
	return isExpectedReaderError(err) ||
		errors.Is(err, entity.ErrInvalidStatus) ||
		errors.Is(err, entity.ErrInvalidRole) ||
		errors.Is(err, entity.ErrInvalidAssetType) ||
		errors.Is(err, entity.ErrDraftNotFound) ||
		errors.Is(err, entity.ErrFeedbackNotFound) ||
		errors.Is(err, entity.ErrForbidden)
}

func isExpectedQuranError(err error) bool {
	return errors.Is(err, entity.ErrUnsupportedLanguage) ||
		errors.Is(err, entity.ErrQuranSurahNotFound) ||
		errors.Is(err, entity.ErrQuranAyahNotFound) ||
		errors.Is(err, entity.ErrQuranRecitationNotFound) ||
		errors.Is(err, entity.ErrQuranTranslationSourceNotFound) ||
		errors.Is(err, entity.ErrInvalidAyahKey) ||
		errors.Is(err, entity.ErrInvalidQuranRange) ||
		errors.Is(err, entity.ErrInvalidAssetType) ||
		errors.Is(err, entity.ErrBookNotFound)
}

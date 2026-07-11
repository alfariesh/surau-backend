package editorial

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/google/uuid"
)

var tafsirRangePattern = regexp.MustCompile(`^\d+(-\d+)?$`)

const quranEditorialMaxOffset = 10000

// SurahEditorialWorkspace returns the protected draft/published states for one
// surah language.
func (uc *UseCase) SurahEditorialWorkspace(
	ctx context.Context,
	surahID int,
	lang string,
) (entity.QuranSurahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranSurahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	lang, err := normalizeQuranEditorialScope(surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, err
	}

	return uc.quranEditorial.GetSurahEditorialWorkspace(ctx, surahID, lang)
}

// SaveSurahEditorialDraft stores a complete REST-authored draft snapshot.
//
//nolint:gocritic // value parameter is fixed by the public usecase contract and copied for normalization
func (uc *UseCase) SaveSurahEditorialDraft(
	ctx context.Context,
	actorID string,
	edit entity.QuranSurahEditorialEdit,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranSurahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	lang, err := normalizeQuranEditorialScope(edit.SurahID, edit.Lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, err
	}

	edit.Lang = lang

	edit.Status = entity.EditStatusDraft

	if normalizeErr := normalizeSurahEditorialEdit(&edit); normalizeErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, normalizeErr
	}

	return uc.quranEditorial.SaveSurahEditorialDraft(ctx, actorID, edit, expected, entity.EditOriginREST)
}

// PublishSurahEditorialDraft promotes the current permitted draft.
func (uc *UseCase) PublishSurahEditorialDraft(
	ctx context.Context,
	actorID string,
	surahID int,
	lang string,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranSurahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	lang, err := normalizeQuranEditorialScope(surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, err
	}

	return uc.quranEditorial.PublishSurahEditorialDraft(ctx, actorID, surahID, lang, expected, entity.EditOriginREST)
}

// RestoreSurahEditorialRevision restores one snapshot into draft only.
func (uc *UseCase) RestoreSurahEditorialRevision(
	ctx context.Context,
	actorID string,
	surahID int,
	lang,
	revisionID string,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranSurahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	lang, err := normalizeQuranEditorialScope(surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, err
	}

	revisionID = strings.TrimSpace(revisionID)
	if _, parseErr := uuid.Parse(revisionID); parseErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, entity.ErrInvalidQuranEditorial
	}

	return uc.quranEditorial.RestoreSurahEditorialRevision(
		ctx, actorID, surahID, lang, revisionID, expected,
	)
}

// AyahEditorialWorkspace returns the protected draft/published states for one
// canonical ayah key and language.
func (uc *UseCase) AyahEditorialWorkspace(
	ctx context.Context,
	ayahKey,
	lang string,
) (entity.QuranAyahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	_, _, normalizedKey, normalizedLang, err := normalizeAyahEditorialScope(ayahKey, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, err
	}

	return uc.quranEditorial.GetAyahEditorialWorkspace(ctx, normalizedKey, normalizedLang)
}

// SaveAyahEditorialDraft stores a complete REST-authored draft snapshot.
//
//nolint:gocritic // value parameter is fixed by the public usecase contract and copied for normalization
func (uc *UseCase) SaveAyahEditorialDraft(
	ctx context.Context,
	actorID string,
	edit entity.QuranAyahEditorialEdit,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	surahID, ayahNumber, ayahKey, lang, err := normalizeAyahEditorialScope(edit.AyahKey, edit.Lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, err
	}

	edit.SurahID = surahID
	edit.AyahNumber = ayahNumber
	edit.AyahKey = ayahKey
	edit.Lang = lang

	edit.Status = entity.EditStatusDraft

	if normalizeErr := normalizeAyahEditorialEdit(&edit); normalizeErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, normalizeErr
	}

	return uc.quranEditorial.SaveAyahEditorialDraft(ctx, actorID, edit, expected, entity.EditOriginREST)
}

// PublishAyahEditorialDraft promotes the current permitted draft.
func (uc *UseCase) PublishAyahEditorialDraft(
	ctx context.Context,
	actorID,
	ayahKey,
	lang string,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	_, _, ayahKey, lang, err := normalizeAyahEditorialScope(ayahKey, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, err
	}

	return uc.quranEditorial.PublishAyahEditorialDraft(ctx, actorID, ayahKey, lang, expected, entity.EditOriginREST)
}

// RestoreAyahEditorialRevision restores one snapshot into draft only.
func (uc *UseCase) RestoreAyahEditorialRevision(
	ctx context.Context,
	actorID,
	ayahKey,
	lang,
	revisionID string,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	if uc.quranEditorial == nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrEditorialUnavailable
	}

	_, _, ayahKey, lang, err := normalizeAyahEditorialScope(ayahKey, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, err
	}

	revisionID = strings.TrimSpace(revisionID)
	if _, parseErr := uuid.Parse(revisionID); parseErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrInvalidQuranEditorial
	}

	return uc.quranEditorial.RestoreAyahEditorialRevision(
		ctx, actorID, ayahKey, lang, revisionID, expected,
	)
}

// QuranEditorialRevisions returns newest-first history for one resource.
func (uc *UseCase) QuranEditorialRevisions(
	ctx context.Context,
	assetType string,
	surahID int,
	ayahNumber *int,
	lang string,
	limit,
	offset int,
) ([]entity.QuranEditorialRevision, int, error) {
	if uc.quranEditorial == nil {
		return nil, 0, entity.ErrEditorialUnavailable
	}

	assetType = strings.TrimSpace(assetType)
	if assetType != entity.QuranEditorialAssetSurah && assetType != entity.QuranEditorialAssetAyah {
		return nil, 0, entity.ErrInvalidAssetType
	}

	lang, err := normalizeQuranEditorialScope(surahID, lang)
	if err != nil {
		return nil, 0, err
	}

	if assetType == entity.QuranEditorialAssetSurah && ayahNumber != nil {
		return nil, 0, entity.ErrInvalidQuranEditorial
	}

	if assetType == entity.QuranEditorialAssetAyah && (ayahNumber == nil || *ayahNumber <= 0) {
		return nil, 0, entity.ErrInvalidQuranEditorial
	}

	return uc.quranEditorial.ListQuranEditorialRevisions(ctx, repo.QuranEditorialRevisionFilter{
		AssetType:  assetType,
		SurahID:    surahID,
		AyahNumber: ayahNumber,
		Lang:       lang,
		Limit:      clampLimit(limit),
		Offset:     clampQuranEditorialOffset(offset),
	})
}

func clampQuranEditorialOffset(offset int) uint64 {
	if offset < 0 {
		return 0
	}

	if offset > quranEditorialMaxOffset {
		return quranEditorialMaxOffset
	}

	return uint64(offset)
}

func normalizeQuranEditorialScope(surahID int, lang string) (string, error) {
	if surahID < 1 || surahID > 114 {
		return "", entity.ErrQuranSurahNotFound
	}

	normalized, err := contentlang.Normalize(lang)
	if err != nil {
		return "", entity.ErrUnsupportedLanguage
	}

	return normalized, nil
}

func normalizeAyahEditorialScope(
	ayahKey,
	lang string,
) (surahID, ayahNumber int, normalizedKey, normalizedLang string, err error) {
	surahID, ayahNumber, err = quranutil.ParseAyahKey(ayahKey)
	if err != nil || surahID < 1 || surahID > 114 {
		return 0, 0, "", "", entity.ErrInvalidAyahKey
	}

	normalizedLang, err = normalizeQuranEditorialScope(surahID, lang)
	if err != nil {
		return 0, 0, "", "", err
	}

	normalizedKey = quranutil.AyahKey(surahID, ayahNumber)

	return surahID, ayahNumber, normalizedKey, normalizedLang, nil
}

func normalizeSurahEditorialEdit(edit *entity.QuranSurahEditorialEdit) error {
	if edit == nil || !entity.IsValidEditorialLicenseStatus(edit.LicenseStatus) {
		return entity.ErrInvalidLicenseStatus
	}

	edit.MetaTitle = trimStringPtr(edit.MetaTitle)
	edit.MetaDescription = trimStringPtr(edit.MetaDescription)
	edit.ArtiNama = trimStringPtr(edit.ArtiNama)
	edit.Keutamaan = trimStringPtr(edit.Keutamaan)
	edit.AsbabunNuzul = trimStringPtr(edit.AsbabunNuzul)
	edit.PokokKandungan = trimStringPtr(edit.PokokKandungan)
	edit.AuthorName = trimStringPtr(edit.AuthorName)
	edit.ReviewedBy = trimStringPtr(edit.ReviewedBy)

	return nil
}

func normalizeAyahEditorialEdit(edit *entity.QuranAyahEditorialEdit) error {
	if edit == nil || !entity.IsValidEditorialLicenseStatus(edit.LicenseStatus) {
		return entity.ErrInvalidLicenseStatus
	}

	edit.MetaTitle = trimStringPtr(edit.MetaTitle)
	edit.MetaDescription = trimStringPtr(edit.MetaDescription)
	edit.Intisari = trimStringPtr(edit.Intisari)
	edit.Keutamaan = trimStringPtr(edit.Keutamaan)
	edit.TafsirRange = trimStringPtr(edit.TafsirRange)
	edit.AuthorName = trimStringPtr(edit.AuthorName)
	edit.ReviewedBy = trimStringPtr(edit.ReviewedBy)

	if edit.TafsirRange != nil && !tafsirRangePattern.MatchString(*edit.TafsirRange) {
		return entity.ErrInvalidQuranEditorial
	}

	for i := range edit.FAQ {
		edit.FAQ[i].Question = strings.TrimSpace(edit.FAQ[i].Question)
		edit.FAQ[i].AnswerHTML = strings.TrimSpace(edit.FAQ[i].AnswerHTML)

		if edit.FAQ[i].Question == "" || edit.FAQ[i].AnswerHTML == "" {
			return entity.ErrInvalidQuranEditorial
		}
	}

	return nil
}

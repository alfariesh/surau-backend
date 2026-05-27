package quran

import (
	"context"
	"slices"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	defaultLimit               = 50
	maxLimit                   = 200
	defaultLang                = "id"
	defaultTranslationSourceID = "qul-kfgqpc-id-simple"
)

var allowedReviewStatuses = []string{"approved", "pending", "rejected", "ambiguous", "needs_review", "all"}

// UseCase provides public Quran read operations.
type UseCase struct {
	repo repo.QuranRepo
}

// New creates a Quran usecase.
func New(r repo.QuranRepo) *UseCase {
	return &UseCase{repo: r}
}

// Surahs returns all imported Quran surahs.
func (uc *UseCase) Surahs(ctx context.Context, lang string) ([]entity.QuranSurah, error) {
	return uc.repo.ListSurahs(ctx, normalizeLang(lang))
}

// Recitations returns imported Quran recitation resources.
func (uc *UseCase) Recitations(ctx context.Context) ([]entity.QuranRecitation, error) {
	return uc.repo.ListRecitations(ctx)
}

// Ayah returns one ayah by canonical QUL ayah key.
func (uc *UseCase) Ayah(
	ctx context.Context,
	ayahKey string,
	lang string,
	translationSource string,
	includeAudio bool,
	recitationID string,
) (entity.QuranAyah, error) {
	ayahKey = strings.TrimSpace(ayahKey)
	if _, _, err := quranutil.ParseAyahKey(ayahKey); err != nil {
		return entity.QuranAyah{}, entity.ErrInvalidAyahKey
	}

	return uc.repo.GetAyah(
		ctx,
		ayahKey,
		normalizeLang(lang),
		normalizeTranslationSource(translationSource),
		includeAudio,
		normalizeRecitationID(recitationID),
	)
}

// SurahAyahs returns one surah or ayah range.
func (uc *UseCase) SurahAyahs(
	ctx context.Context,
	surahID int,
	fromAyah int,
	toAyah int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	if surahID <= 0 || surahID > 114 {
		return nil, entity.ErrQuranSurahNotFound
	}
	if fromAyah < 0 || toAyah < 0 {
		return nil, entity.ErrInvalidQuranRange
	}
	if fromAyah == 0 && toAyah > 0 {
		fromAyah = 1
	}
	if fromAyah > 0 && toAyah > 0 && toAyah < fromAyah {
		return nil, entity.ErrInvalidQuranRange
	}

	return uc.repo.ListSurahAyahs(
		ctx,
		surahID,
		fromAyah,
		toAyah,
		normalizeLang(lang),
		normalizeTranslationSource(translationSource),
		includeTranslation,
		includeAudio,
		normalizeRecitationID(recitationID),
	)
}

// Search returns ranked Quran text or translation hits.
func (uc *UseCase) Search(ctx context.Context, query, lang string, limit, offset int) ([]entity.QuranSearchResult, int, error) {
	return uc.repo.SearchAyahs(ctx, repo.QuranSearchFilter{
		Query:             strings.TrimSpace(query),
		Lang:              normalizeLang(lang),
		TranslationSource: defaultTranslationSourceID,
		Limit:             clampLimit(limit),
		Offset:            clampOffset(offset),
	})
}

// BookReferences returns Quran references linked to a public kitab.
func (uc *UseCase) BookReferences(
	ctx context.Context,
	bookID int,
	lang string,
	status string,
	limit int,
	offset int,
) ([]entity.BookQuranReference, int, error) {
	if bookID <= 0 {
		return nil, 0, entity.ErrBookNotFound
	}

	return uc.repo.ListBookQuranReferences(ctx, repo.QuranBookReferenceFilter{
		BookID:            bookID,
		Lang:              normalizeLang(lang),
		TranslationSource: defaultTranslationSourceID,
		Status:            normalizeStatus(status),
		Limit:             clampLimit(limit),
		Offset:            clampOffset(offset),
	})
}

func clampLimit(limit int) uint64 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}

	return uint64(limit)
}

func clampOffset(offset int) uint64 {
	if offset < 0 {
		return 0
	}

	return uint64(offset)
}

func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return defaultLang
	}

	return lang
}

func normalizeTranslationSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return defaultTranslationSourceID
	}

	return source
}

func normalizeRecitationID(recitationID string) string {
	return strings.TrimSpace(recitationID)
}

func normalizeStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if slices.Contains(allowedReviewStatuses, status) {
		return status
	}

	return "approved"
}

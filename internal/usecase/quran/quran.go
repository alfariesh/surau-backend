package quran

import (
	"context"
	"slices"
	"strings"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	defaultLimit = 50
	maxLimit     = 200

	navigationKindJuz  = "juz"
	navigationKindHizb = "hizb"
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
func (uc *UseCase) Surahs(ctx context.Context, lang string, includeInfo bool) ([]entity.QuranSurah, error) {
	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, err
	}

	return uc.repo.ListSurahs(ctx, normalizedLang, includeInfo)
}

// Surah returns one imported Quran surah with language-specific info.
func (uc *UseCase) Surah(ctx context.Context, surahID int, lang string) (entity.QuranSurah, error) {
	if surahID <= 0 || surahID > 114 {
		return entity.QuranSurah{}, entity.ErrQuranSurahNotFound
	}

	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return entity.QuranSurah{}, err
	}

	return uc.repo.GetSurah(ctx, surahID, normalizedLang)
}

// Recitations returns imported Quran recitation resources.
func (uc *UseCase) Recitations(ctx context.Context) ([]entity.QuranRecitation, error) {
	return uc.repo.ListRecitations(ctx)
}

// TranslationSources returns Quran translation sources for one language.
func (uc *UseCase) TranslationSources(ctx context.Context, lang string) ([]entity.QuranTranslationSource, error) {
	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, err
	}
	if contentlang.IsArabic(normalizedLang) {
		return []entity.QuranTranslationSource{}, nil
	}

	return uc.repo.ListTranslationSources(ctx, normalizedLang)
}

// Juz returns imported Quran juz navigation segments.
func (uc *UseCase) Juz(ctx context.Context, lang string) ([]entity.QuranNavigationSegment, error) {
	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, err
	}

	return uc.repo.ListNavigationSegments(ctx, navigationKindJuz, normalizedLang)
}

// JuzAyahs returns ayahs inside one imported juz segment.
func (uc *UseCase) JuzAyahs(
	ctx context.Context,
	juzNumber int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	if juzNumber < 1 || juzNumber > 30 {
		return nil, entity.ErrInvalidQuranRange
	}

	return uc.navigationAyahs(
		ctx,
		navigationKindJuz,
		juzNumber,
		lang,
		translationSource,
		includeTranslation,
		includeAudio,
		recitationID,
	)
}

// Hizbs returns imported Quran hizb navigation segments.
func (uc *UseCase) Hizbs(ctx context.Context, lang string) ([]entity.QuranNavigationSegment, error) {
	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, err
	}

	return uc.repo.ListNavigationSegments(ctx, navigationKindHizb, normalizedLang)
}

// HizbAyahs returns ayahs inside one imported hizb segment.
func (uc *UseCase) HizbAyahs(
	ctx context.Context,
	hizbNumber int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	if hizbNumber < 1 || hizbNumber > 60 {
		return nil, entity.ErrInvalidQuranRange
	}

	return uc.navigationAyahs(
		ctx,
		navigationKindHizb,
		hizbNumber,
		lang,
		translationSource,
		includeTranslation,
		includeAudio,
		recitationID,
	)
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

	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return entity.QuranAyah{}, err
	}

	return uc.repo.GetAyah(
		ctx,
		ayahKey,
		normalizedLang,
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

	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, err
	}

	return uc.repo.ListSurahAyahs(
		ctx,
		surahID,
		fromAyah,
		toAyah,
		normalizedLang,
		normalizeTranslationSource(translationSource),
		includeTranslation,
		includeAudio,
		normalizeRecitationID(recitationID),
	)
}

func (uc *UseCase) navigationAyahs(
	ctx context.Context,
	kind string,
	number int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, err
	}

	return uc.repo.ListNavigationAyahs(
		ctx,
		kind,
		number,
		normalizedLang,
		normalizeTranslationSource(translationSource),
		includeTranslation,
		includeAudio,
		normalizeRecitationID(recitationID),
	)
}

// Search returns ranked Quran text or translation hits.
func (uc *UseCase) Search(ctx context.Context, query, lang string, limit, offset int) ([]entity.QuranSearchResult, int, error) {
	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.SearchAyahs(ctx, repo.QuranSearchFilter{
		Query:             strings.TrimSpace(query),
		Lang:              normalizedLang,
		TranslationSource: "",
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

	normalizedLang, err := contentlang.Normalize(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListBookQuranReferences(ctx, repo.QuranBookReferenceFilter{
		BookID:            bookID,
		Lang:              normalizedLang,
		TranslationSource: "",
		Status:            normalizeStatus(status),
		Limit:             clampLimit(limit),
		Offset:            clampOffset(offset),
	})
}

// MissingAssets returns admin queue items for missing Quran assets.
func (uc *UseCase) MissingAssets(
	ctx context.Context,
	targetLang string,
	assetType string,
	surahID *int,
	limit int,
	offset int,
) (entity.EditorialMissingQuranAssets, error) {
	filter, err := missingQuranAssetFilter(targetLang, assetType, surahID, limit, offset)
	if err != nil {
		return entity.EditorialMissingQuranAssets{}, err
	}

	return uc.repo.ListMissingQuranAssets(ctx, filter)
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

func normalizeTranslationSource(source string) string {
	source = strings.TrimSpace(source)
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

func missingQuranAssetFilter(
	targetLang string,
	assetType string,
	surahID *int,
	limit int,
	offset int,
) (repo.MissingQuranAssetFilter, error) {
	targetLang = strings.TrimSpace(targetLang)
	targetLangs := []string{contentlang.Default, contentlang.English}
	if targetLang != "" {
		normalized, err := contentlang.Normalize(targetLang)
		if err != nil || normalized == contentlang.Arabic {
			return repo.MissingQuranAssetFilter{}, entity.ErrUnsupportedLanguage
		}

		targetLangs = []string{normalized}
	}

	assetType = strings.ToLower(strings.TrimSpace(assetType))
	if assetType != "" && !isMissingQuranAssetType(assetType) {
		return repo.MissingQuranAssetFilter{}, entity.ErrInvalidAssetType
	}

	return repo.MissingQuranAssetFilter{
		TargetLangs: targetLangs,
		AssetType:   assetType,
		SurahID:     surahID,
		Limit:       clampLimit(limit),
		Offset:      clampOffset(offset),
	}, nil
}

func isMissingQuranAssetType(assetType string) bool {
	switch assetType {
	case entity.MissingQuranAssetSurahInfo,
		entity.MissingQuranAssetAyahTranslation,
		entity.MissingQuranAssetTranslationSource,
		entity.MissingQuranAssetAudioPublic:
		return true
	default:
		return false
	}
}

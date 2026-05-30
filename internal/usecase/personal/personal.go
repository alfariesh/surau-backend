package personal

import (
	"context"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/google/uuid"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxTags      = 20
	maxTagLength = 64

	quranProgressFutureTolerance = 5 * time.Minute
)

// UseCase provides authenticated reader operations.
type UseCase struct {
	repo repo.PersonalRepo
}

// New creates a personal usecase.
func New(r repo.PersonalRepo) *UseCase {
	return &UseCase{repo: r}
}

// GetProgress returns one user's progress for a book.
func (uc *UseCase) GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error) {
	return uc.repo.GetProgress(ctx, userID, bookID)
}

// SaveProgress upserts one user's progress for a book.
func (uc *UseCase) SaveProgress(
	ctx context.Context,
	userID string,
	bookID int,
	pageID, headingID *int,
	progressPercent *float64,
) (entity.ReadingProgress, error) {
	return uc.repo.SaveProgress(ctx, entity.ReadingProgress{
		UserID:          userID,
		BookID:          bookID,
		PageID:          pageID,
		HeadingID:       headingID,
		ProgressPercent: progressPercent,
	})
}

// GetQuranProgress returns the user's latest Quran resume position across surahs.
func (uc *UseCase) GetQuranProgress(ctx context.Context, userID string) (entity.QuranReadingProgress, error) {
	return uc.repo.GetQuranProgress(ctx, userID)
}

// GetQuranSurahProgress returns the user's latest Quran resume position for one surah.
func (uc *UseCase) GetQuranSurahProgress(
	ctx context.Context,
	userID string,
	surahID int,
) (entity.QuranReadingProgress, error) {
	if surahID <= 0 || surahID > 114 {
		return entity.QuranReadingProgress{}, entity.ErrQuranSurahNotFound
	}

	return uc.repo.GetQuranSurahProgress(ctx, userID, surahID)
}

// ListQuranSurahProgress returns all per-surah Quran resume positions.
func (uc *UseCase) ListQuranSurahProgress(
	ctx context.Context,
	userID string,
) ([]entity.QuranReadingProgress, error) {
	return uc.repo.ListQuranSurahProgress(ctx, userID)
}

// SaveQuranProgress upserts one user's Quran resume position for the ayah's surah.
func (uc *UseCase) SaveQuranProgress(
	ctx context.Context,
	userID string,
	ayahKey string,
	clientObservedAt *time.Time,
) (entity.QuranReadingProgress, error) {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(ayahKey)
	if err != nil {
		return entity.QuranReadingProgress{}, entity.ErrInvalidAyahKey
	}

	now := time.Now().UTC()
	observedAt := now
	if clientObservedAt != nil {
		observedAt = clientObservedAt.UTC()
	}
	if observedAt.IsZero() || observedAt.After(now.Add(quranProgressFutureTolerance)) {
		return entity.QuranReadingProgress{}, entity.ErrInvalidQuranProgress
	}

	return uc.repo.SaveQuranProgress(ctx, entity.QuranReadingProgress{
		UserID:     userID,
		SurahID:    surahID,
		AyahNumber: ayahNumber,
		AyahKey:    quranutil.AyahKey(surahID, ayahNumber),
		ObservedAt: observedAt,
	})
}

// ListSavedItems returns paginated private saved items.
func (uc *UseCase) ListSavedItems(
	ctx context.Context,
	userID string,
	itemType string,
	bookID, surahID *int,
	tag string,
	limit, offset int,
) ([]entity.SavedItem, int, error) {
	normalizedType, err := normalizeSavedItemType(itemType, true)
	if err != nil {
		return nil, 0, err
	}
	normalizedTag, err := normalizeSavedItemTag(tag)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListSavedItems(ctx, userID, repo.SavedItemFilter{
		ItemType: normalizedType,
		BookID:   bookID,
		SurahID:  surahID,
		Tag:      normalizedTag,
		Limit:    clampLimit(limit),
		Offset:   clampOffset(offset),
	})
}

// UpsertSavedItem creates or updates a saved item for the same target.
func (uc *UseCase) UpsertSavedItem(
	ctx context.Context,
	userID string,
	item entity.SavedItem,
) (entity.SavedItem, error) {
	item.ID = uuid.New().String()
	item.UserID = userID

	normalized, err := normalizeSavedItem(item)
	if err != nil {
		return entity.SavedItem{}, err
	}

	return uc.repo.UpsertSavedItem(ctx, normalized)
}

// UpdateSavedItem updates mutable saved item metadata.
func (uc *UseCase) UpdateSavedItem(
	ctx context.Context,
	userID string,
	savedItemID string,
	label, note *string,
	tags []string,
) (entity.SavedItem, error) {
	normalizedTags, err := normalizeSavedItemTags(tags)
	if err != nil {
		return entity.SavedItem{}, err
	}

	return uc.repo.UpdateSavedItem(ctx, entity.SavedItem{
		ID:     strings.TrimSpace(savedItemID),
		UserID: userID,
		Label:  label,
		Note:   note,
		Tags:   normalizedTags,
	})
}

// DeleteSavedItem removes one saved item.
func (uc *UseCase) DeleteSavedItem(ctx context.Context, userID, savedItemID string) error {
	return uc.repo.DeleteSavedItem(ctx, userID, strings.TrimSpace(savedItemID))
}

// ListSavedItemTags returns all private saved-item tags for autocomplete.
func (uc *UseCase) ListSavedItemTags(ctx context.Context, userID string) ([]string, error) {
	return uc.repo.ListSavedItemTags(ctx, userID)
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

func normalizeSavedItem(item entity.SavedItem) (entity.SavedItem, error) {
	var err error
	item.ItemType, err = normalizeSavedItemType(item.ItemType, false)
	if err != nil {
		return entity.SavedItem{}, err
	}
	item.Tags, err = normalizeSavedItemTags(item.Tags)
	if err != nil {
		return entity.SavedItem{}, err
	}

	switch item.ItemType {
	case entity.SavedItemTypeBookPage:
		if item.BookID == nil || item.PageID == nil || item.HeadingID != nil || hasQuranTarget(item) {
			return entity.SavedItem{}, entity.ErrInvalidSavedItem
		}
	case entity.SavedItemTypeBookHeading:
		if item.BookID == nil || item.PageID != nil || item.HeadingID == nil || hasQuranTarget(item) {
			return entity.SavedItem{}, entity.ErrInvalidSavedItem
		}
	case entity.SavedItemTypeQuranAyah:
		if item.AyahKey == nil || item.BookID != nil || item.PageID != nil || item.HeadingID != nil ||
			item.FromAyahNumber != nil || item.ToAyahNumber != nil {
			return entity.SavedItem{}, entity.ErrInvalidSavedItem
		}

		surahID, ayahNumber, err := quranutil.ParseAyahKey(*item.AyahKey)
		if err != nil {
			return entity.SavedItem{}, entity.ErrInvalidAyahKey
		}
		item.SurahID = intPtr(surahID)
		item.AyahKey = stringPtr(quranutil.AyahKey(surahID, ayahNumber))
	case entity.SavedItemTypeQuranRange:
		if item.SurahID == nil || item.FromAyahNumber == nil || item.ToAyahNumber == nil ||
			item.AyahKey != nil || item.BookID != nil || item.PageID != nil || item.HeadingID != nil {
			return entity.SavedItem{}, entity.ErrInvalidSavedItem
		}
		if *item.SurahID <= 0 || *item.SurahID > 114 || *item.FromAyahNumber <= 0 || *item.ToAyahNumber < *item.FromAyahNumber {
			return entity.SavedItem{}, entity.ErrInvalidQuranRange
		}
		if *item.FromAyahNumber == *item.ToAyahNumber {
			item.ItemType = entity.SavedItemTypeQuranAyah
			item.AyahKey = stringPtr(quranutil.AyahKey(*item.SurahID, *item.FromAyahNumber))
			item.FromAyahNumber = nil
			item.ToAyahNumber = nil
		}
	default:
		return entity.SavedItem{}, entity.ErrInvalidSavedItem
	}

	return item, nil
}

func normalizeSavedItemType(itemType string, allowEmpty bool) (string, error) {
	itemType = strings.ToLower(strings.TrimSpace(itemType))
	if itemType == "" && allowEmpty {
		return "", nil
	}
	switch itemType {
	case entity.SavedItemTypeBookPage,
		entity.SavedItemTypeBookHeading,
		entity.SavedItemTypeQuranAyah,
		entity.SavedItemTypeQuranRange:
		return itemType, nil
	default:
		return "", entity.ErrInvalidSavedItem
	}
}

func normalizeSavedItemTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return []string{}, nil
	}

	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		normalizedTag, err := normalizeSavedItemTag(tag)
		if err != nil {
			return nil, err
		}
		if normalizedTag == "" {
			continue
		}
		if _, exists := seen[normalizedTag]; exists {
			continue
		}
		seen[normalizedTag] = struct{}{}
		normalized = append(normalized, normalizedTag)
	}
	if len(normalized) > maxTags {
		return nil, entity.ErrInvalidSavedItem
	}

	return normalized, nil
}

func normalizeSavedItemTag(tag string) (string, error) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if len(tag) > maxTagLength {
		return "", entity.ErrInvalidSavedItem
	}

	return tag, nil
}

func hasQuranTarget(item entity.SavedItem) bool {
	return item.SurahID != nil || item.AyahKey != nil || item.FromAyahNumber != nil || item.ToAyahNumber != nil
}

func intPtr(value int) *int {
	return &value
}

func stringPtr(value string) *string {
	return &value
}

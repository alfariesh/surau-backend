package personal

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/evrone/go-clean-template/internal/readerlang"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/google/uuid"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	// maxOffset bounds deep-offset scans; keyset pagination is future work.
	maxOffset = 10000
	maxTags   = 20
	// maxTagLength/maxLabelLength/maxNoteLength mirror the saved_items CHECK
	// constraints; PATCH bodies bypass validator tags so they are enforced here.
	maxTagLength   = 64
	maxLabelLength = 255
	maxNoteLength  = 2000

	progressFutureTolerance = 5 * time.Minute
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

// SaveProgress upserts one user's progress for a book. Stale
// client_observed_at events never roll the stored position back.
func (uc *UseCase) SaveProgress(
	ctx context.Context,
	userID string,
	bookID int,
	pageID, headingID *int,
	progressPercent *float64,
	clientObservedAt *time.Time,
) (entity.ReadingProgress, error) {
	observedAt, ok := resolveObservedAt(clientObservedAt)
	if !ok {
		return entity.ReadingProgress{}, entity.ErrInvalidReadingProgress
	}

	return uc.repo.SaveProgress(ctx, entity.ReadingProgress{
		UserID:          userID,
		BookID:          bookID,
		PageID:          pageID,
		HeadingID:       headingID,
		ProgressPercent: progressPercent,
		ObservedAt:      observedAt,
	})
}

// ListProgress returns the user's in-progress books for the continue-reading shelf.
func (uc *UseCase) ListProgress(
	ctx context.Context,
	userID, lang string,
	limit, offset int,
) ([]entity.ContinueReadingEntry, int, error) {
	normalizedLang, err := readerlang.Normalize(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListProgress(ctx, userID, normalizedLang, clampLimit(limit), clampOffset(offset))
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

	observedAt, ok := resolveObservedAt(clientObservedAt)
	if !ok {
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
// Absent metadata never overwrites stored values; the returned bool reports
// whether a new item was created.
func (uc *UseCase) UpsertSavedItem(
	ctx context.Context,
	userID string,
	item entity.SavedItem,
) (entity.SavedItem, bool, error) {
	item.ID = uuid.New().String()
	item.UserID = userID

	normalized, err := normalizeSavedItem(item)
	if err != nil {
		return entity.SavedItem{}, false, err
	}

	return uc.repo.UpsertSavedItem(ctx, normalized)
}

// UpdateSavedItem applies a partial metadata update: absent fields stay
// unchanged, explicit nulls clear. A patch without any field is rejected.
func (uc *UseCase) UpdateSavedItem(
	ctx context.Context,
	userID string,
	savedItemID string,
	patch entity.SavedItemPatch,
) (entity.SavedItem, error) {
	if !patch.LabelSet && !patch.NoteSet && !patch.TagsSet {
		return entity.SavedItem{}, entity.ErrInvalidSavedItem
	}
	if patch.LabelSet && patch.Label != nil && utf8.RuneCountInString(*patch.Label) > maxLabelLength {
		return entity.SavedItem{}, entity.ErrInvalidSavedItem
	}
	if patch.NoteSet && patch.Note != nil && utf8.RuneCountInString(*patch.Note) > maxNoteLength {
		return entity.SavedItem{}, entity.ErrInvalidSavedItem
	}
	if patch.TagsSet {
		if patch.Tags == nil {
			// Explicit null clears all tags.
			patch.Tags = []string{}
		}
		normalizedTags, err := normalizeSavedItemTags(patch.Tags)
		if err != nil {
			return entity.SavedItem{}, err
		}
		if normalizedTags == nil {
			normalizedTags = []string{}
		}
		patch.Tags = normalizedTags
	}

	return uc.repo.UpdateSavedItem(ctx, userID, strings.TrimSpace(savedItemID), patch)
}

// DeleteSavedItem removes one saved item.
func (uc *UseCase) DeleteSavedItem(ctx context.Context, userID, savedItemID string) error {
	return uc.repo.DeleteSavedItem(ctx, userID, strings.TrimSpace(savedItemID))
}

// ListSavedItemTags returns all private saved-item tags for autocomplete.
func (uc *UseCase) ListSavedItemTags(ctx context.Context, userID string) ([]string, error) {
	return uc.repo.ListSavedItemTags(ctx, userID)
}

// StartKhatamCycle begins a new khatam cycle. Only one active cycle is
// allowed per user.
func (uc *UseCase) StartKhatamCycle(
	ctx context.Context,
	userID string,
	notes *string,
) (entity.QuranKhatamCycle, error) {
	if notes != nil {
		trimmed := strings.TrimSpace(*notes)
		if trimmed == "" {
			notes = nil
		} else {
			notes = &trimmed
		}
	}

	return uc.repo.StartKhatamCycle(ctx, entity.QuranKhatamCycle{
		ID:     uuid.New().String(),
		UserID: userID,
		Notes:  notes,
	})
}

// GetActiveKhatamCycle returns the user's active khatam cycle.
func (uc *UseCase) GetActiveKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error) {
	return uc.repo.GetActiveKhatamCycle(ctx, userID)
}

// MarkKhatamJuz marks one juz as completed on the active cycle (idempotent).
func (uc *UseCase) MarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, error) {
	if juzNumber < 1 || juzNumber > entity.KhatamJuzTotal {
		return entity.QuranKhatamCycle{}, entity.ErrInvalidJuzNumber
	}

	return uc.repo.MarkKhatamJuz(ctx, userID, juzNumber)
}

// UnmarkKhatamJuz removes one juz mark from the active cycle (idempotent).
func (uc *UseCase) UnmarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, error) {
	if juzNumber < 1 || juzNumber > entity.KhatamJuzTotal {
		return entity.QuranKhatamCycle{}, entity.ErrInvalidJuzNumber
	}

	return uc.repo.UnmarkKhatamJuz(ctx, userID, juzNumber)
}

// CompleteKhatamCycle completes the active cycle once all 30 juz are marked.
// Completion is explicit so an accidental final mark stays reversible.
func (uc *UseCase) CompleteKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error) {
	return uc.repo.CompleteKhatamCycle(ctx, userID)
}

// ListKhatamHistory returns completed khatam cycles.
func (uc *UseCase) ListKhatamHistory(
	ctx context.Context,
	userID string,
	limit, offset int,
) ([]entity.QuranKhatamCycle, int, error) {
	return uc.repo.ListKhatamHistory(ctx, userID, clampLimit(limit), clampOffset(offset))
}

// SyncPersonalData returns the user's personal reader state changed since the
// given cursor, or a full snapshot when since is nil.
func (uc *UseCase) SyncPersonalData(
	ctx context.Context,
	userID string,
	since *time.Time,
) (entity.PersonalSyncSnapshot, error) {
	if since != nil {
		normalized := since.UTC()
		if normalized.IsZero() || normalized.After(time.Now().UTC().Add(progressFutureTolerance)) {
			return entity.PersonalSyncSnapshot{}, entity.ErrInvalidSyncSince
		}
		since = &normalized
	}

	return uc.repo.SyncSnapshot(ctx, userID, since)
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

	if offset > maxOffset {
		return maxOffset
	}

	return uint64(offset)
}

// resolveObservedAt validates the client-supplied event time used by the
// monotonic progress upserts. The reported false means the time is zero or
// further in the future than the shared tolerance allows.
func resolveObservedAt(clientObservedAt *time.Time) (time.Time, bool) {
	now := time.Now().UTC()
	observedAt := now
	if clientObservedAt != nil {
		observedAt = clientObservedAt.UTC()
	}
	if observedAt.IsZero() || observedAt.After(now.Add(progressFutureTolerance)) {
		return time.Time{}, false
	}

	return observedAt, true
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
		item.SurahID = new(surahID)
		item.AyahKey = new(quranutil.AyahKey(surahID, ayahNumber))
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
			item.AyahKey = new(quranutil.AyahKey(*item.SurahID, *item.FromAyahNumber))
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
	if tags == nil {
		// Absent tags stay nil so upserts can preserve stored values.
		return nil, nil
	}
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

package personal

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/readerlang"
	"github.com/alfariesh/surau-backend/internal/repo"
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

	activityDateLayout       = "2006-01-02"
	activityDefaultRangeDays = 30
	activityMaxRangeDays     = 366
	hoursPerDay              = 24
)

// Notifier fires fire-and-forget push notifications for khatam events. Implemented by the
// notification usecase; nil when push notifications are disabled.
type Notifier interface {
	NotifyKhatamCompleted(ctx context.Context, userID string)
	NotifyKhatamMilestone(ctx context.Context, userID string, juzCount int)
}

// UseCase provides authenticated reader operations.
type UseCase struct {
	repo     repo.PersonalRepo
	notifier Notifier
}

// New creates a personal usecase. notifier may be nil when push notifications are disabled.
func New(r repo.PersonalRepo, notifier Notifier) *UseCase {
	return &UseCase{repo: r, notifier: notifier}
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

	// Only notify when a NEW mark was inserted (a genuine 9->10 / 19->20 transition
	// once isKhatamMilestone gates on the count). An idempotent re-mark returns
	// changed=false, so retries/offline replays no longer re-send the milestone push.
	cycle, changed, err := uc.repo.MarkKhatamJuz(ctx, userID, juzNumber)
	if err == nil && changed && uc.notifier != nil {
		uc.notifier.NotifyKhatamMilestone(ctx, userID, cycle.JuzCount)
	}

	return cycle, err
}

// UnmarkKhatamJuz removes one juz mark from the active cycle (idempotent).
func (uc *UseCase) UnmarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, error) {
	if juzNumber < 1 || juzNumber > entity.KhatamJuzTotal {
		return entity.QuranKhatamCycle{}, entity.ErrInvalidJuzNumber
	}

	cycle, _, err := uc.repo.UnmarkKhatamJuz(ctx, userID, juzNumber)

	return cycle, err
}

// CompleteKhatamCycle completes the active cycle once all 30 juz are marked.
// Completion is explicit so an accidental final mark stays reversible.
func (uc *UseCase) CompleteKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error) {
	cycle, err := uc.repo.CompleteKhatamCycle(ctx, userID)
	if err == nil && uc.notifier != nil {
		uc.notifier.NotifyKhatamCompleted(ctx, userID)
	}

	return cycle, err
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

// GetReadingStreak returns the user's reading streak relative to the
// client's local date (defaults to the server's UTC date).
func (uc *UseCase) GetReadingStreak(ctx context.Context, userID, today string) (entity.ReadingStreak, error) {
	normalizedToday, err := resolveActivityDate(today)
	if err != nil {
		return entity.ReadingStreak{}, err
	}

	return uc.repo.GetReadingStreak(ctx, userID, normalizedToday)
}

// GetReadingActivity returns daily activity buckets plus an aggregate for
// [from, to]; defaults to the most recent activityDefaultRangeDays days.
func (uc *UseCase) GetReadingActivity(
	ctx context.Context,
	userID, from, to string,
) (entity.ReadingActivitySummary, error) {
	normalizedTo, err := resolveActivityDate(to)
	if err != nil {
		return entity.ReadingActivitySummary{}, err
	}

	toDate, err := time.Parse(activityDateLayout, normalizedTo)
	if err != nil {
		return entity.ReadingActivitySummary{}, entity.ErrInvalidActivityDate
	}

	normalizedFrom := strings.TrimSpace(from)
	if normalizedFrom == "" {
		normalizedFrom = toDate.AddDate(0, 0, -(activityDefaultRangeDays - 1)).Format(activityDateLayout)
	}

	fromDate, err := time.Parse(activityDateLayout, normalizedFrom)
	if err != nil {
		return entity.ReadingActivitySummary{}, entity.ErrInvalidActivityDate
	}

	if fromDate.After(toDate) || toDate.Sub(fromDate) > activityMaxRangeDays*hoursPerDay*time.Hour {
		return entity.ReadingActivitySummary{}, entity.ErrInvalidActivityRange
	}

	return uc.repo.GetReadingActivity(ctx, userID, normalizedFrom, normalizedTo)
}

// resolveActivityDate validates a client-supplied local calendar date. Empty
// defaults to the server's UTC date; dates further than two days from it are
// rejected (no real timezone is that far away).
func resolveActivityDate(date string) (string, error) {
	serverToday := time.Now().UTC().Truncate(hoursPerDay * time.Hour)

	date = strings.TrimSpace(date)
	if date == "" {
		return serverToday.Format(activityDateLayout), nil
	}

	parsed, err := time.Parse(activityDateLayout, date)
	if err != nil {
		return "", entity.ErrInvalidActivityDate
	}

	if parsed.After(serverToday.Add(2*24*time.Hour)) || parsed.Before(serverToday.Add(-2*24*time.Hour)) {
		return "", entity.ErrInvalidActivityDate
	}

	return parsed.Format(activityDateLayout), nil
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
// further in the future than the shared tolerance allows. The original UTC
// offset is preserved: instant comparisons are zone-agnostic, and the offset
// carries the client's local calendar date for reading-activity bucketing.
func resolveObservedAt(clientObservedAt *time.Time) (time.Time, bool) {
	now := time.Now().UTC()

	observedAt := now
	if clientObservedAt != nil {
		observedAt = *clientObservedAt
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
	// Count runes, not bytes, so non-ASCII tags (Arabic, etc.) hit the limit at the
	// expected character count — consistent with the label/note checks above.
	if utf8.RuneCountInString(tag) > maxTagLength {
		return "", entity.ErrInvalidSavedItem
	}

	return tag, nil
}

func hasQuranTarget(item entity.SavedItem) bool {
	return item.SurahID != nil || item.AyahKey != nil || item.FromAyahNumber != nil || item.ToAyahNumber != nil
}

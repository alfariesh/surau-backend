package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// These tests pin the public-DoS guards (roadmap E5, defects D2/D4): offsets
// are clamped to 10k, headings are paginated with an additive default of 200,
// and search queries are bounded in length.

func TestReaderBooksClampsHugeOffset(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)

	mockRepo.EXPECT().
		ListBooks(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, filter repo.BookFilter) ([]entity.Book, int, error) {
			assert.Equal(t, uint64(10000), filter.Offset, "offset=10^9 must clamp to 10k (D2)")
			assert.Equal(t, uint64(200), filter.Limit)

			return []entity.Book{}, 0, nil
		})

	_, _, err := uc.Books(context.Background(), "", nil, nil, nil, 999999, 1_000_000_000, "ar")
	require.NoError(t, err)
}

func TestReaderAuthorsClampsNegativeOffset(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)

	mockRepo.EXPECT().
		ListAuthors(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, filter repo.AuthorFilter) ([]entity.Author, int, error) {
			assert.Equal(t, uint64(0), filter.Offset)

			return []entity.Author{}, 0, nil
		})

	_, _, err := uc.Authors(context.Background(), "q", 10, -5, "ar")
	require.NoError(t, err)
}

func TestReaderHeadingsPaginationDefaults(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)

	mockRepo.EXPECT().
		ListBookHeadings(gomock.Any(), 7, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, filter repo.HeadingFilter) ([]entity.BookHeading, int, error) {
			assert.Equal(t, uint64(200), filter.Limit, "omitted limit must default to 200 (D4, additive)")
			assert.Equal(t, uint64(0), filter.Offset)
			assert.Equal(t, "bab", filter.Query)

			return []entity.BookHeading{{BookID: 7, HeadingID: 1}}, 431, nil
		})

	items, total, err := uc.Headings(context.Background(), 7, "  bab  ", 0, 0)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, 431, total, "total must be the full match count, not len(items)")
}

func TestReaderHeadingsPaginationClamps(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)

	mockRepo.EXPECT().
		ListBookHeadings(gomock.Any(), 7, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, filter repo.HeadingFilter) ([]entity.BookHeading, int, error) {
			assert.Equal(t, uint64(200), filter.Limit, "limit above max must clamp to 200")
			assert.Equal(t, uint64(10000), filter.Offset, "offset must clamp to 10k")

			return []entity.BookHeading{}, 0, nil
		})

	_, _, err := uc.Headings(context.Background(), 7, "", 5000, 1_000_000_000)
	require.NoError(t, err)
}

func TestReaderSearchQueryLengthBounded(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	huge := strings.Repeat("ب", 5000)

	mockRepo.EXPECT().
		ListBooks(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, filter repo.BookFilter) ([]entity.Book, int, error) {
			assert.Len(t, []rune(filter.Query), 200, "search query must be bounded to 200 runes")

			return []entity.Book{}, 0, nil
		})

	_, _, err := uc.Books(context.Background(), huge, nil, nil, nil, 10, 0, "ar")
	require.NoError(t, err)
}

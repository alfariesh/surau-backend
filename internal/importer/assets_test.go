package importer_test

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/evrone/go-clean-template/internal/importer"
	"github.com/stretchr/testify/require"
)

func TestReaderAssetValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		asset   importer.ReaderAsset
		wantErr bool
	}{
		{
			name: "translation",
			asset: importer.ReaderAsset{
				Kind:      "translation",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
				Content:   "Terjemahan",
			},
		},
		{
			name: "english region language",
			asset: importer.ReaderAsset{
				Kind:      "translation",
				BookID:    797,
				HeadingID: 10,
				Lang:      "en-US",
				Content:   "Translation",
			},
		},
		{
			name: "unsupported language",
			asset: importer.ReaderAsset{
				Kind:      "translation",
				BookID:    797,
				HeadingID: 10,
				Lang:      "fr",
				Content:   "Traduction",
			},
			wantErr: true,
		},
		{
			name: "audio",
			asset: importer.ReaderAsset{
				Kind:      "audio",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
				URL:       "https://cdn.example/audio.mp3",
			},
		},
		{
			name: "heading summary",
			asset: importer.ReaderAsset{
				Kind:      "heading_summary",
				BookID:    797,
				HeadingID: 10,
				Lang:      "ar",
				Summary:   "يتناول الباب تعريف الحديث الصحيح.",
			},
		},
		{
			name: "missing content",
			asset: importer.ReaderAsset{
				Kind:      "translation",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
			},
			wantErr: true,
		},
		{
			name: "heading summary missing summary",
			asset: importer.ReaderAsset{
				Kind:      "heading_summary",
				BookID:    797,
				HeadingID: 10,
				Lang:      "ar",
			},
			wantErr: true,
		},
		{
			name: "unsupported kind",
			asset: importer.ReaderAsset{
				Kind:      "pdf",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
			},
			wantErr: true,
		},
		{
			name: "reviewed heading summary",
			asset: importer.ReaderAsset{
				Kind:              "heading_summary",
				BookID:            797,
				HeadingID:         10,
				Lang:              "id",
				Summary:           "Bab ini menjelaskan hadis sahih.",
				SummaryStatus:     "reviewed",
				SummaryReviewedBy: new("Editor A"),
			},
		},
		{
			name: "reviewed heading summary missing reviewer",
			asset: importer.ReaderAsset{
				Kind:          "heading_summary",
				BookID:        797,
				HeadingID:     10,
				Lang:          "id",
				Summary:       "Bab ini menjelaskan hadis sahih.",
				SummaryStatus: "reviewed",
			},
			wantErr: true,
		},
		{
			name: "book metadata translation",
			asset: importer.ReaderAsset{
				Kind:         "book_metadata_translation",
				BookID:       797,
				Lang:         "id",
				DisplayTitle: new("Judul Kitab"),
			},
		},
		{
			name: "author translation",
			asset: importer.ReaderAsset{
				Kind:     "author_translation",
				AuthorID: 177,
				Lang:     "id",
				Name:     new("Nama Penulis"),
			},
		},
		{
			name: "category translation",
			asset: importer.ReaderAsset{
				Kind:       "category_translation",
				CategoryID: 10,
				Lang:       "id",
				Name:       new("Ilmu Hadis"),
			},
		},
		{
			name: "reviewed translation",
			asset: importer.ReaderAsset{
				Kind:       "translation",
				BookID:     797,
				HeadingID:  10,
				Lang:       "id",
				Content:    "Terjemahan",
				Status:     "reviewed",
				ReviewedBy: new("Editor A"),
			},
		},
		{
			name: "reviewed translation missing reviewer",
			asset: importer.ReaderAsset{
				Kind:      "translation",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
				Content:   "Terjemahan",
				Status:    "reviewed",
			},
			wantErr: true,
		},
		{
			name: "invalid translation status",
			asset: importer.ReaderAsset{
				Kind:      "translation",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
				Content:   "Terjemahan",
				Status:    "approved",
			},
			wantErr: true,
		},
		{
			name: "book metadata missing title",
			asset: importer.ReaderAsset{
				Kind:   "book_metadata_translation",
				BookID: 797,
				Lang:   "id",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.asset.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestReaderAssetSampleJSONL(t *testing.T) {
	t.Parallel()

	file, err := os.Open("../../examples/reader-assets.sample.jsonl")
	require.NoError(t, err)
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var asset importer.ReaderAsset
		require.NoError(t, json.Unmarshal([]byte(line), &asset))
		require.NoError(t, asset.Validate())
		count++
	}

	require.NoError(t, scanner.Err())
	require.Positive(t, count)
}

//go:fix inline
func stringPtr(value string) *string {
	return new(value)
}

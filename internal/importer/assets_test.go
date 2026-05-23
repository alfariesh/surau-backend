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
			name: "unsupported kind",
			asset: importer.ReaderAsset{
				Kind:      "pdf",
				BookID:    797,
				HeadingID: 10,
				Lang:      "id",
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

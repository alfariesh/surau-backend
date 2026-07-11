package importer

import (
	"errors"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapLicenseGateError(t *testing.T) {
	t.Parallel()

	databaseErr := &pgconn.PgError{
		Code:           "23514",
		Message:        "new public writes require license_status=permitted",
		ConstraintName: licensePublishConstraint,
	}

	mapped := mapLicenseGateError(databaseErr)
	require.Error(t, mapped)
	assert.ErrorIs(t, mapped, entity.ErrLicenseNotPermitted)
	assert.Contains(t, mapped.Error(), "new public writes require")
}

func TestMapLicenseGateErrorLeavesUnrelatedErrorsAlone(t *testing.T) {
	t.Parallel()

	original := errors.New("unrelated")
	assert.Same(t, original, mapLicenseGateError(original))
}

func TestSameMasterBookMetadata(t *testing.T) {
	t.Parallel()

	categoryID := 7
	authorID := 8
	sourceDate := "1445"
	first := masterBook{
		ID: 1, Name: "Kitab", CategoryID: &categoryID, AuthorID: &authorID,
		SourceDate: &sourceDate, PDFLinks: `{"a":1,"b":2}`, Metadata: `{"source":"fixture"}`,
	}
	second := first
	second.PDFLinks = `{ "b": 2, "a": 1 }`

	assert.True(t, sameMasterBookMetadata(&first, &second), "JSON formatting and key order are not material")

	second.Name = "Changed title"
	assert.False(t, sameMasterBookMetadata(&first, &second))
}

func TestSameMasterSharedMetadataIgnoresOnlyWriteBookkeeping(t *testing.T) {
	t.Parallel()

	displayOrder := 7
	category := masterCategory{
		ID: 1, Name: "Aqidah", DisplayOrder: &displayOrder,
	}
	categoryNoOp := category
	categoryNoOp.skipWrite = true
	assert.True(t, sameMasterCategory(category, categoryNoOp))

	categoryChanged := category
	categoryChanged.Name = "Changed"
	assert.False(t, sameMasterCategory(category, categoryChanged))

	biography := "Biography"
	author := masterAuthor{
		ID: 2, Name: "Scholar", Biography: &biography,
		NameSearch: "scholar", NameSearchNormalizationVersion: 1,
	}
	authorNoOp := author
	authorNoOp.skipWrite = true
	assert.True(t, sameMasterAuthor(&author, &authorNoOp))

	authorChanged := author
	authorChanged.NameSearchNormalizationVersion++
	assert.False(t, sameMasterAuthor(&author, &authorChanged),
		"a normalization profile rewrite changes public search behavior")
}

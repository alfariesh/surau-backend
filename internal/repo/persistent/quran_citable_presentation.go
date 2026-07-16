package persistent

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

type quranRowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type quranTranslationAttribution struct {
	name            string
	translator      *string
	responsibleName *string
	responsibleRole *string
	sourceURL       *string
	licenseStatus   string
}

type quranTransliterationAttribution struct {
	name            string
	responsibleName *string
	responsibleRole *string
	sourceURL       *string
	licenseStatus   string
}

// hydrateQuranCitablePresentation attaches attribution and current Citable
// Unit identities in a constant number of set-based queries. Surahs not yet
// backfilled retain the legacy rendering fallback; once derived, a rendering
// is returned only when its active binding still matches the current source.
//
//nolint:gocognit,gocyclo,cyclop,funlen,wsl_v5 // flat, set-based hydration of three roles plus linked footnotes
func hydrateQuranCitablePresentation(
	ctx context.Context,
	queryer quranRowsQuerier,
	ayahs []entity.QuranAyah,
) error {
	if len(ayahs) == 0 {
		return nil
	}

	ayahKeys := make([]string, 0, len(ayahs))
	translationSourceSet := make(map[string]struct{})
	transliterationSourceSet := make(map[string]struct{})
	ayahByKey := make(map[string]*entity.QuranAyah, len(ayahs))
	for i := range ayahs {
		ayah := &ayahs[i]
		ayahKeys = append(ayahKeys, ayah.AyahKey)
		ayahByKey[ayah.AyahKey] = ayah
		if ayah.Translation != nil {
			translationSourceSet[ayah.Translation.SourceID] = struct{}{}
		}
		if ayah.Transliteration != nil {
			transliterationSourceSet[ayah.Transliteration.SourceID] = struct{}{}
		}
	}

	translationSources := mapKeys(translationSourceSet)
	translationAttribution, err := loadQuranTranslationAttribution(ctx, queryer, translationSources)
	if err != nil {
		return err
	}
	transliterationSources := mapKeys(transliterationSourceSet)
	transliterationAttribution, err := loadQuranTransliterationAttribution(
		ctx, queryer, transliterationSources,
	)
	if err != nil {
		return err
	}

	for i := range ayahs {
		ayah := &ayahs[i]
		if ayah.Translation != nil {
			attribution, exists := translationAttribution[ayah.Translation.SourceID]
			if !exists {
				ayah.Translation = nil
			} else {
				ayah.Translation.SourceName = attribution.name
				ayah.Translation.Translator = attribution.translator
				ayah.Translation.ResponsibleName = attribution.responsibleName
				ayah.Translation.ResponsibleRole = attribution.responsibleRole
				ayah.Translation.SourceURL = attribution.sourceURL
				ayah.Translation.LicenseStatus = attribution.licenseStatus
			}
		}
		if ayah.Transliteration != nil {
			attribution, exists := transliterationAttribution[ayah.Transliteration.SourceID]
			if !exists {
				ayah.Transliteration = nil
			} else {
				ayah.Transliteration.SourceName = attribution.name
				ayah.Transliteration.ResponsibleName = attribution.responsibleName
				ayah.Transliteration.ResponsibleRole = attribution.responsibleRole
				ayah.Transliteration.SourceURL = attribution.sourceURL
				ayah.Transliteration.LicenseStatus = attribution.licenseStatus
			}
		}
	}

	derivedByAyah, err := loadQuranAyahDerivedState(ctx, queryer, ayahKeys)
	if err != nil {
		return err
	}

	rows, err := queryer.Query(ctx, `
SELECT a.ayah_key,
       u.id::text,
       u.anchor,
       u.parent_unit_id::text,
       u.marker,
       u.text,
       b.role,
       b.translation_source_id,
       b.transliteration_source_id,
       b.footnote_key
FROM quran_ayahs a
JOIN quran_surahs s ON s.surah_id = a.surah_id AND s.units_stale_at IS NULL
JOIN quran_citable_unit_bindings b
  ON b.surah_id = a.surah_id AND b.ayah_number = a.ayah_number
JOIN citable_units u
  ON u.id = b.unit_id
 AND u.corpus = 'quran'
 AND u.lifecycle = 'active'
JOIN LATERAL (
    SELECT license.id
    FROM citable_units_with_effective_license license
    WHERE license.id = u.id
      AND license.corpus = 'quran'
      AND license.effective_license_status = 'permitted'
    LIMIT 1
) license ON true
LEFT JOIN quran_ayah_translations t
  ON t.source_id = b.translation_source_id
 AND t.surah_id = b.surah_id AND t.ayah_number = b.ayah_number
LEFT JOIN quran_ayah_transliterations x
  ON x.source_id = b.transliteration_source_id
 AND x.surah_id = b.surah_id AND x.ayah_number = b.ayah_number
WHERE a.ayah_key = ANY($1::text[])
  AND (
      (b.role = 'primary_text' AND b.source_updated_at = a.updated_at AND u.text = a.text_qpc_hafs)
      OR (b.role = 'translation' AND b.source_updated_at = t.updated_at AND u.text = t.text)
      OR (b.role = 'footnote' AND b.source_updated_at = t.updated_at)
      OR (b.role = 'transliteration' AND b.source_updated_at = x.updated_at AND u.text = x.text)
  )
ORDER BY b.surah_id, b.ayah_number, b.ordinal`, ayahKeys)
	if err != nil {
		return fmt.Errorf("QuranRepo hydrate Citable Units: %w", err)
	}
	defer rows.Close()

	translationByUnitID := make(map[string]*entity.QuranTranslation)
	footnotes := make([]entity.QuranCitableFootnote, 0)
	for rows.Next() {
		var (
			ayahKey                 string
			unitID                  string
			anchor                  string
			parentUnitID            sql.NullString
			marker                  sql.NullString
			text                    string
			role                    string
			translationSourceID     sql.NullString
			transliterationSourceID sql.NullString
			footnoteKey             sql.NullString
		)
		if err := rows.Scan(&ayahKey, &unitID, &anchor, &parentUnitID, &marker, &text,
			&role, &translationSourceID, &transliterationSourceID, &footnoteKey); err != nil {
			return fmt.Errorf("QuranRepo hydrate Citable Unit scan: %w", err)
		}

		ayah := ayahByKey[ayahKey]
		if ayah == nil {
			continue
		}
		switch role {
		case entity.QuranUnitRolePrimaryText:
			ayah.PrimaryUnitID = new(unitID)
			ayah.PrimaryUnitAnchor = new(anchor)
		case entity.QuranUnitRoleTranslation:
			if ayah.Translation == nil || !translationSourceID.Valid ||
				ayah.Translation.SourceID != translationSourceID.String {
				continue
			}
			ayah.Translation.UnitID = new(unitID)
			ayah.Translation.Anchor = new(anchor)
			translationByUnitID[unitID] = ayah.Translation
		case entity.QuranUnitRoleTransliteration:
			if ayah.Transliteration == nil || !transliterationSourceID.Valid ||
				ayah.Transliteration.SourceID != transliterationSourceID.String {
				continue
			}
			ayah.Transliteration.UnitID = new(unitID)
			ayah.Transliteration.Anchor = new(anchor)
		case entity.QuranUnitRoleFootnote:
			if !parentUnitID.Valid || !translationSourceID.Valid || !footnoteKey.Valid {
				continue
			}
			footnotes = append(footnotes, entity.QuranCitableFootnote{
				UnitID:       unitID,
				Anchor:       anchor,
				ParentUnitID: parentUnitID.String,
				FootnoteKey:  footnoteKey.String,
				Marker:       nullableString(marker),
				Text:         text,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("QuranRepo hydrate Citable Unit rows: %w", err)
	}

	for i := range footnotes {
		footnote := footnotes[i]
		if translation := translationByUnitID[footnote.ParentUnitID]; translation != nil {
			translation.FootnoteUnits = append(translation.FootnoteUnits, footnote)
		}
	}

	for i := range ayahs {
		ayah := &ayahs[i]
		if !derivedByAyah[ayah.AyahKey] {
			continue
		}
		if ayah.Translation != nil && ayah.Translation.UnitID == nil {
			ayah.Translation = nil
		}
		if ayah.Transliteration != nil && ayah.Transliteration.UnitID == nil {
			ayah.Transliteration = nil
		}
	}

	return nil
}

//nolint:wsl_v5 // one ordered scan maps every public attribution field explicitly
func loadQuranTranslationAttribution(
	ctx context.Context,
	queryer quranRowsQuerier,
	sourceIDs []string,
) (map[string]quranTranslationAttribution, error) {
	result := make(map[string]quranTranslationAttribution)
	if len(sourceIDs) == 0 {
		return result, nil
	}
	rows, err := queryer.Query(ctx, `
SELECT id, name, translator, responsible_name, responsible_role, source_url, license_status
FROM public_quran_translation_sources
WHERE id = ANY($1::text[])`, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo translation attribution: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id              string
			value           quranTranslationAttribution
			translator      sql.NullString
			responsibleName sql.NullString
			responsibleRole sql.NullString
			sourceURL       sql.NullString
		)
		if err := rows.Scan(&id, &value.name, &translator, &responsibleName,
			&responsibleRole, &sourceURL, &value.licenseStatus); err != nil {
			return nil, fmt.Errorf("QuranRepo translation attribution scan: %w", err)
		}
		value.translator = nullableString(translator)
		value.responsibleName = nullableString(responsibleName)
		value.responsibleRole = nullableString(responsibleRole)
		value.sourceURL = nullableString(sourceURL)
		result[id] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo translation attribution rows: %w", err)
	}

	return result, nil
}

//nolint:wsl_v5 // one ordered scan maps every public attribution field explicitly
func loadQuranTransliterationAttribution(
	ctx context.Context,
	queryer quranRowsQuerier,
	sourceIDs []string,
) (map[string]quranTransliterationAttribution, error) {
	result := make(map[string]quranTransliterationAttribution)
	if len(sourceIDs) == 0 {
		return result, nil
	}
	rows, err := queryer.Query(ctx, `
SELECT id, name, responsible_name, responsible_role, source_url, license_status
FROM public_quran_transliteration_sources
WHERE id = ANY($1::text[])`, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo transliteration attribution: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id              string
			value           quranTransliterationAttribution
			responsibleName sql.NullString
			responsibleRole sql.NullString
			sourceURL       sql.NullString
		)
		if err := rows.Scan(&id, &value.name, &responsibleName, &responsibleRole,
			&sourceURL, &value.licenseStatus); err != nil {
			return nil, fmt.Errorf("QuranRepo transliteration attribution scan: %w", err)
		}
		value.responsibleName = nullableString(responsibleName)
		value.responsibleRole = nullableString(responsibleRole)
		value.sourceURL = nullableString(sourceURL)
		result[id] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo transliteration attribution rows: %w", err)
	}

	return result, nil
}

//nolint:wsl_v5 // one ordered scan into the derived-state lookup
func loadQuranAyahDerivedState(
	ctx context.Context,
	queryer quranRowsQuerier,
	ayahKeys []string,
) (map[string]bool, error) {
	rows, err := queryer.Query(ctx, `
SELECT a.ayah_key, s.units_derived_at IS NOT NULL AND s.units_stale_at IS NULL
FROM quran_ayahs a
JOIN quran_surahs s ON s.surah_id = a.surah_id
WHERE a.ayah_key = ANY($1::text[])`, ayahKeys)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo Citable Unit derived state: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool, len(ayahKeys))

	for rows.Next() {
		var (
			ayahKey string
			derived bool
		)
		if err := rows.Scan(&ayahKey, &derived); err != nil {
			return nil, fmt.Errorf("QuranRepo Citable Unit derived state scan: %w", err)
		}
		result[ayahKey] = derived
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo Citable Unit derived state rows: %w", err)
	}

	return result, nil
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}

	return result
}

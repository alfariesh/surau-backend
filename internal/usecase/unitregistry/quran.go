package unitregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/searchtext"
)

type derivedQuranUnit struct {
	ayahNumber      int
	pageNumber      *int
	role            string
	kind            string
	sourceID        string
	footnoteKey     string
	parentSlot      string
	position        int
	marker          string
	text            string
	language        string
	sourceUpdatedAt time.Time
	contentHash     []byte
}

type quranFootnoteWire struct {
	Number json.RawMessage `json:"number"`
	Marker string          `json:"marker"`
	Text   string          `json:"text"`
	N      json.RawMessage `json:"n"`
	T      string          `json:"t"`
}

type derivedQuranFootnote struct {
	key    string
	marker string
	text   string
}

const quranDerivedCapacityPerAyah = 4

var (
	errQuranUnitRepositoryUnavailable = errors.New("quran Citable Unit repository unavailable")
	errInvalidQuranUnitSource         = errors.New("invalid Quran Citable Unit source")
	errInvalidQuranUnitPlan           = errors.New("invalid Quran Citable Unit plan")
)

// ReconcileQuranSurah derives one whole surah and applies it through the same
// B-1 registry transaction used by kitab. A conflicting writer is replanned;
// a primary-text drift fails closed and never partially updates the registry.
//
//nolint:wsl_v5 // load, derive, snapshot, plan, and optimistic apply are one bounded retry pipeline
func (uc *UseCase) ReconcileQuranSurah(
	ctx context.Context,
	surahID int,
) (entity.QuranUnitReconcileReport, error) {
	if uc.quranRepo == nil {
		return entity.QuranUnitReconcileReport{}, errQuranUnitRepositoryUnavailable
	}

	var lastErr error

	for attempt := 1; attempt <= reconcileAttempts; attempt++ {
		source, err := uc.quranRepo.LoadQuranSource(ctx, surahID)
		if err != nil {
			return entity.QuranUnitReconcileReport{}, err
		}

		derived, err := deriveQuranSurah(&source)
		if err != nil {
			return entity.QuranUnitReconcileReport{}, err
		}

		snapshot, err := uc.quranRepo.QuranSnapshot(ctx, surahID)
		if err != nil {
			return entity.QuranUnitReconcileReport{}, err
		}

		plan, err := planQuranSurah(&source, derived, &snapshot)
		if err != nil {
			return entity.QuranUnitReconcileReport{}, err
		}

		plan.Report.Attempts = attempt

		err = uc.quranRepo.ApplyQuranReconcile(ctx, &plan)
		if err == nil {
			return plan.Report, nil
		}
		if !errors.Is(err, entity.ErrUnitReconcileConflict) {
			return entity.QuranUnitReconcileReport{}, err
		}

		lastErr = err
	}

	return entity.QuranUnitReconcileReport{}, fmt.Errorf(
		"reconcile Quran surah %d: %w", surahID, lastErr,
	)
}

//nolint:funlen,gocognit,gocyclo,cyclop,wsl_v5 // deterministic per-ayah pipeline keeps primary, translations, footnotes, and transliteration ordering adjacent
func deriveQuranSurah(source *entity.QuranUnitSource) ([]derivedQuranUnit, error) {
	ayahs := append([]entity.QuranUnitSourceAyah(nil), source.Ayahs...)
	sort.Slice(ayahs, func(i, j int) bool { return ayahs[i].AyahNumber < ayahs[j].AyahNumber })

	derived := make([]derivedQuranUnit, 0, len(ayahs)*quranDerivedCapacityPerAyah)
	seenAyah := make(map[int]struct{}, len(ayahs))

	for i := range ayahs {
		ayah := &ayahs[i]
		if _, exists := seenAyah[ayah.AyahNumber]; exists || ayah.AyahNumber < 1 {
			return nil, fmt.Errorf("%w: surah %d ayah %d duplicated or invalid",
				errInvalidQuranUnitSource, source.SurahID, ayah.AyahNumber)
		}
		seenAyah[ayah.AyahNumber] = struct{}{}

		primary := strings.TrimSpace(ayah.PrimaryText)
		if primary == "" {
			// Legacy reader/editorial fixtures can legitimately predate the QPC
			// script import. They are not Quran Citable Units until primary text
			// exists, and dependent translations/transliterations must not be
			// minted without that primary unit. If a previously-derived primary
			// disappears, planQuranSurah still fails closed below.
			continue
		}

		position := 0
		derived = append(derived, newDerivedQuranUnit(
			ayah.AyahNumber, ayah.PageNumber, entity.QuranUnitRolePrimaryText,
			"qpc-hafs", "", "", position, "", primary, "ar", ayah.PrimaryUpdatedAt,
		))
		position++

		translations := append([]entity.QuranUnitSourceTranslation(nil), ayah.Translations...)
		sort.Slice(translations, func(i, j int) bool {
			if translations[i].Language != translations[j].Language {
				return translations[i].Language < translations[j].Language
			}

			return translations[i].SourceID < translations[j].SourceID
		})
		seenTranslations := make(map[string]struct{}, len(translations))

		for j := range translations {
			translation := &translations[j]
			if strings.TrimSpace(translation.SourceID) == "" || strings.TrimSpace(translation.Text) == "" {
				return nil, fmt.Errorf("%w: surah %d ayah %d has invalid translation source",
					errInvalidQuranUnitSource, source.SurahID, ayah.AyahNumber)
			}
			if _, exists := seenTranslations[translation.SourceID]; exists {
				return nil, fmt.Errorf("%w: surah %d ayah %d translation source %q duplicated",
					errInvalidQuranUnitSource, source.SurahID, ayah.AyahNumber, translation.SourceID)
			}
			seenTranslations[translation.SourceID] = struct{}{}

			translationSlot := quranSlotKey(
				ayah.AyahNumber, entity.QuranUnitRoleTranslation, translation.SourceID, "",
			)
			derived = append(derived, newDerivedQuranUnit(
				ayah.AyahNumber, ayah.PageNumber, entity.QuranUnitRoleTranslation,
				translation.SourceID, "", "", position, "", strings.TrimSpace(translation.Text),
				translation.Language, translation.UpdatedAt,
			))
			position++

			footnotes, err := parseQuranFootnotes(
				translation.Footnotes, quranFootnoteMarkerText(translation),
			)
			if err != nil {
				return nil, fmt.Errorf("surah %d ayah %d source %s: %w",
					source.SurahID, ayah.AyahNumber, translation.SourceID, err)
			}
			for _, footnote := range footnotes {
				derived = append(derived, newDerivedQuranUnit(
					ayah.AyahNumber, ayah.PageNumber, entity.QuranUnitRoleFootnote,
					translation.SourceID, footnote.key, translationSlot, position,
					footnote.marker, footnote.text, translation.Language, translation.UpdatedAt,
				))
				position++
			}
		}

		transliterations := append([]entity.QuranUnitSourceTransliteration(nil), ayah.Transliterations...)
		sort.Slice(transliterations, func(i, j int) bool {
			if transliterations[i].Language != transliterations[j].Language {
				return transliterations[i].Language < transliterations[j].Language
			}

			return transliterations[i].SourceID < transliterations[j].SourceID
		})
		seenTransliterations := make(map[string]struct{}, len(transliterations))

		for j := range transliterations {
			transliteration := &transliterations[j]
			if strings.TrimSpace(transliteration.SourceID) == "" || strings.TrimSpace(transliteration.Text) == "" {
				return nil, fmt.Errorf("%w: surah %d ayah %d has invalid transliteration source",
					errInvalidQuranUnitSource, source.SurahID, ayah.AyahNumber)
			}
			if _, exists := seenTransliterations[transliteration.SourceID]; exists {
				return nil, fmt.Errorf("%w: surah %d ayah %d transliteration source %q duplicated",
					errInvalidQuranUnitSource, source.SurahID, ayah.AyahNumber, transliteration.SourceID)
			}
			seenTransliterations[transliteration.SourceID] = struct{}{}

			derived = append(derived, newDerivedQuranUnit(
				ayah.AyahNumber, ayah.PageNumber, entity.QuranUnitRoleTransliteration,
				transliteration.SourceID, "", "", position, "", strings.TrimSpace(transliteration.Text),
				transliteration.Language, transliteration.UpdatedAt,
			))
			position++
		}
	}

	return derived, nil
}

func newDerivedQuranUnit(
	ayahNumber int,
	pageNumber *int,
	role, sourceID, footnoteKey, parentSlot string,
	position int,
	marker, text, language string,
	sourceUpdatedAt time.Time,
) derivedQuranUnit {
	kind := role
	if role == entity.QuranUnitRoleFootnote {
		kind = entity.UnitKindFootnote
	}

	return derivedQuranUnit{
		ayahNumber:      ayahNumber,
		pageNumber:      pageNumber,
		role:            role,
		kind:            kind,
		sourceID:        sourceID,
		footnoteKey:     footnoteKey,
		parentSlot:      parentSlot,
		position:        position,
		marker:          marker,
		text:            text,
		language:        language,
		sourceUpdatedAt: sourceUpdatedAt,
		contentHash:     ContentHash(kind, marker, text),
	}
}

var quranFootnoteMarkerRE = regexp.MustCompile(
	`(?i)<sup\b[^>]*\bfoot_note\s*=\s*["']([^"']+)["'][^>]*>([^<]*)</sup\s*>`,
)

//nolint:gocognit,gocyclo,cyclop,wsl_v5 // one flat compatibility parser validates both supported source shapes
func parseQuranFootnotes(raw json.RawMessage, translationText string) ([]derivedQuranFootnote, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	if trimmed[0] == '{' {
		return parseQULQuranFootnotes(trimmed, translationText)
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("%w: expected array or QUL object map", entity.ErrInvalidQuranFootnotes)
	}

	var wire []quranFootnoteWire
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return nil, fmt.Errorf("%w: %w", entity.ErrInvalidQuranFootnotes, err)
	}

	result := make([]derivedQuranFootnote, 0, len(wire))
	seen := make(map[string]struct{}, len(wire))
	for i := range wire {
		item := &wire[i]
		keyRaw := item.Number
		text := item.Text
		if len(bytes.TrimSpace(keyRaw)) == 0 {
			keyRaw = item.N
			text = item.T
		}

		key, err := footnoteJSONKey(keyRaw)
		if err != nil || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("%w: item %d requires number/n and text/t",
				entity.ErrInvalidQuranFootnotes, i)
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate key %q", entity.ErrInvalidQuranFootnotes, key)
		}
		seen[key] = struct{}{}

		marker := strings.TrimSpace(item.Marker)
		if marker == "" {
			marker = key
		}
		result = append(result, derivedQuranFootnote{
			key: key, marker: marker, text: strings.TrimSpace(text),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		left, leftErr := strconv.Atoi(result[i].key)
		right, rightErr := strconv.Atoi(result[j].key)
		if leftErr == nil && rightErr == nil && left != right {
			return left < right
		}

		return result[i].key < result[j].key
	})

	return result, nil
}

// parseQULQuranFootnotes normalizes QUL's footnote-tags JSON shape. The map key
// is the stable footnote ID, while the human-facing marker lives in the
// matching <sup foot_note="ID">marker</sup> tag in the tagged source payload.
func parseQULQuranFootnotes(
	raw json.RawMessage,
	translationText string,
) ([]derivedQuranFootnote, error) {
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("%w: %w", entity.ErrInvalidQuranFootnotes, err)
	}

	markers := quranFootnoteMarkers(translationText)
	result := make([]derivedQuranFootnote, 0, len(wire))

	seen := make(map[string]struct{}, len(wire))
	for rawKey, rawText := range wire {
		key, err := quranObjectFootnoteKey(rawKey)
		if err != nil {
			return nil, fmt.Errorf("%w: object key %q must be a positive integer",
				entity.ErrInvalidQuranFootnotes, rawKey)
		}

		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate canonical key %q",
				entity.ErrInvalidQuranFootnotes, key)
		}

		seen[key] = struct{}{}

		var text string
		if err := json.Unmarshal(rawText, &text); err != nil || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("%w: object key %q requires non-empty string text",
				entity.ErrInvalidQuranFootnotes, rawKey)
		}

		marker := markers[key]
		if marker == "" {
			marker = key
		}

		result = append(result, derivedQuranFootnote{
			key: key, marker: marker, text: strings.TrimSpace(text),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if len(result[i].key) != len(result[j].key) {
			return len(result[i].key) < len(result[j].key)
		}

		return result[i].key < result[j].key
	})

	return result, nil
}

func quranObjectFootnoteKey(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)

	number, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || number == 0 {
		return "", entity.ErrInvalidQuranFootnotes
	}

	return strconv.FormatUint(number, 10), nil
}

func quranFootnoteMarkers(translationText string) map[string]string {
	result := make(map[string]string)

	for _, match := range quranFootnoteMarkerRE.FindAllStringSubmatch(translationText, -1) {
		key, err := quranObjectFootnoteKey(match[1])

		marker := strings.TrimSpace(html.UnescapeString(match[2]))
		if err != nil || marker == "" || result[key] != "" {
			continue
		}

		result[key] = marker
	}

	return result
}

func quranFootnoteMarkerText(translation *entity.QuranUnitSourceTranslation) string {
	var metadata struct {
		T           string `json:"t"`
		Text        string `json:"text"`
		Translation string `json:"translation"`
	}
	if err := json.Unmarshal(translation.Metadata, &metadata); err != nil {
		return translation.Text
	}

	return strings.Join([]string{
		translation.Text, metadata.T, metadata.Text, metadata.Translation,
	}, "\n")
}

//nolint:wsl_v5 // string and numeric compatibility forms are validated in sequence
func footnoteJSONKey(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", entity.ErrInvalidQuranFootnotes
	}

	if strings.HasPrefix(trimmed, "\"") {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
			return "", entity.ErrInvalidQuranFootnotes
		}

		return strings.TrimSpace(value), nil
	}

	var number json.Number

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", entity.ErrInvalidQuranFootnotes
	}

	return number.String(), nil
}

type plannedQuranUnit struct {
	derived    derivedQuranUnit
	unitID     string
	ordinal    int
	occurrence int
	matched    *entity.QuranCitableUnitRecord
	mintIndex  int
}

//nolint:funlen,gocognit,gocyclo,cyclop,nestif,wsl_v5 // one deterministic registry pass keeps slot matching, immutable-primary checks, lineage, and parent wiring auditable
func planQuranSurah(
	source *entity.QuranUnitSource,
	derived []derivedQuranUnit,
	snapshot *entity.QuranUnitRegistrySnapshot,
) (entity.QuranUnitReconcilePlan, error) {
	plan := entity.QuranUnitReconcilePlan{
		SurahID:        source.SurahID,
		LoadedAt:       source.LoadedAt,
		BasedOn:        snapshot.Fingerprint,
		ExpectedActive: int64(len(derived)),
		Report: entity.QuranUnitReconcileReport{
			SurahID: source.SurahID,
			Ayahs:   len(source.Ayahs),
			Derived: len(derived),
		},
	}

	activeBySlot := make(map[string]*entity.QuranCitableUnitRecord, len(snapshot.Active))
	for i := range snapshot.Active {
		record := &snapshot.Active[i]
		slot := quranBindingSlotKey(&record.Binding)
		if _, exists := activeBySlot[slot]; exists {
			return plan, fmt.Errorf("%w: duplicate active Quran Citable Unit slot %s",
				errInvalidQuranUnitPlan, slot)
		}
		activeBySlot[slot] = record
	}

	highwater := make(map[int]int, len(snapshot.MaxOrdinalByAyah))
	maps.Copy(highwater, snapshot.MaxOrdinalByAyah)

	planned := make([]plannedQuranUnit, 0, len(derived))
	desiredSlots := make(map[string]struct{}, len(derived))
	unitBySlot := make(map[string]string, len(derived))

	for i := range derived {
		d := derived[i]
		slot := quranSlotKey(d.ayahNumber, d.role, d.sourceID, d.footnoteKey)
		if _, exists := desiredSlots[slot]; exists {
			return plan, fmt.Errorf("%w: duplicate derived Quran Citable Unit slot %s",
				errInvalidQuranUnitPlan, slot)
		}
		desiredSlots[slot] = struct{}{}

		item := plannedQuranUnit{derived: d, mintIndex: -1}
		if current, exists := activeBySlot[slot]; exists && bytes.Equal(current.Unit.ContentHash, d.contentHash) {
			item.unitID = current.Unit.ID
			item.ordinal = current.Binding.Ordinal
			item.occurrence = current.Unit.Occurrence
			item.matched = current
			plan.Report.Matched++
		} else {
			if exists && d.role == entity.QuranUnitRolePrimaryText {
				return plan, fmt.Errorf("surah %d ayah %d: %w", source.SurahID, d.ayahNumber,
					entity.ErrQuranPrimaryTextDrift)
			}

			highwater[d.ayahNumber]++
			item.ordinal = highwater[d.ayahNumber]
			if d.role == entity.QuranUnitRolePrimaryText && item.ordinal != 1 {
				return plan, fmt.Errorf("%w: surah %d ayah %d primary unit cannot mint at ordinal %d",
					errInvalidQuranUnitPlan, source.SurahID, d.ayahNumber, item.ordinal)
			}

			item.occurrence = 1
			for {
				item.unitID = QuranUnitID(source.SurahID, d.ayahNumber, d.role,
					d.sourceID, d.footnoteKey, d.contentHash, item.occurrence)
				if _, exists := snapshot.ExistingIDs[item.unitID]; !exists {
					break
				}
				item.occurrence++
			}

			if current, exists := activeBySlot[slot]; exists {
				plan.Retires = append(plan.Retires, entity.UnitPlanRetire{
					ID: current.Unit.ID, Lifecycle: entity.UnitLifecycleSuperseded,
				})
				plan.Edges = append(plan.Edges, entity.CitableUnitLineage{
					PredecessorID: current.Unit.ID,
					SuccessorID:   item.unitID,
					Reason:        entity.UnitLineageReasonEdit,
				})
				plan.Report.Superseded++
				plan.Report.Edges++
			}
		}

		unitBySlot[slot] = item.unitID
		planned = append(planned, item)
	}

	for i := range planned {
		item := &planned[i]
		var parentID *string
		if item.derived.parentSlot != "" {
			id, exists := unitBySlot[item.derived.parentSlot]
			if !exists {
				return plan, fmt.Errorf("%w: Quran footnote parent slot %s is absent",
					errInvalidQuranUnitPlan, item.derived.parentSlot)
			}
			parentID = &id
		}

		if item.matched != nil {
			if !sameOptionalInt(item.matched.Unit.PageID, item.derived.pageNumber) ||
				item.matched.Unit.Position != item.derived.position ||
				!sameParent(item.matched.Unit.ParentUnitID, parentID) ||
				!item.matched.Binding.SourceUpdatedAt.Equal(item.derived.sourceUpdatedAt) {
				plan.Updates = append(plan.Updates, entity.QuranUnitPlanUpdate{
					ID:              item.unitID,
					PageID:          item.derived.pageNumber,
					Position:        item.derived.position,
					ParentUnitID:    parentID,
					SourceUpdatedAt: item.derived.sourceUpdatedAt,
				})
				plan.Report.Updated++
			}

			continue
		}

		mint := mintQuranUnit(source.SurahID, item, parentID)
		item.mintIndex = len(plan.Mints)
		plan.Mints = append(plan.Mints, mint)
		plan.Report.Minted++
	}

	for i := range snapshot.Active {
		record := &snapshot.Active[i]
		if _, exists := desiredSlots[quranBindingSlotKey(&record.Binding)]; exists {
			continue
		}
		if record.Binding.Role == entity.QuranUnitRolePrimaryText {
			return plan, fmt.Errorf("surah %d ayah %d primary source disappeared: %w",
				source.SurahID, record.Binding.AyahNumber, entity.ErrQuranPrimaryTextDrift)
		}
		plan.Retires = append(plan.Retires, entity.UnitPlanRetire{
			ID: record.Unit.ID, Lifecycle: entity.UnitLifecycleTombstoned,
		})
		plan.Report.Tombstoned++
	}

	return plan, nil
}

func sameOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

//nolint:funlen,wsl_v5 // explicit unit and binding field mapping keeps deterministic identity inputs auditable
func mintQuranUnit(
	surahID int,
	planned *plannedQuranUnit,
	parentID *string,
) entity.QuranUnitMint {
	d := &planned.derived

	detail := map[string]any{
		"ayah_key":  fmt.Sprintf("%d:%d", surahID, d.ayahNumber),
		"role":      d.role,
		"source_id": d.sourceID,
	}
	if d.footnoteKey != "" {
		detail["footnote_key"] = d.footnoteKey
		detail["footnote_link"] = entity.FootnoteLinkMarker
	}

	var marker *string
	if d.marker != "" {
		value := d.marker
		marker = &value
	}

	unit := entity.CitableUnit{
		ID:                   planned.unitID,
		Corpus:               entity.UnitCorpusQuran,
		PageID:               d.pageNumber,
		Kind:                 d.kind,
		Ordinal:              planned.ordinal,
		Position:             d.position,
		ParentUnitID:         parentID,
		Anchor:               QuranAnchorFor(surahID, d.ayahNumber, planned.ordinal),
		Marker:               marker,
		Text:                 d.text,
		TextNormalized:       searchtext.Normalize(d.text),
		NormalizationVersion: searchtext.ProfileVersion,
		ContentHash:          d.contentHash,
		Occurrence:           planned.occurrence,
		Language:             d.language,
		ProvenanceClass:      entity.ProvenanceClassSource,
		ProvenanceDetail:     detail,
		Lifecycle:            entity.UnitLifecycleActive,
	}

	binding := entity.QuranCitableUnitBinding{
		UnitID:          planned.unitID,
		SurahID:         surahID,
		AyahNumber:      d.ayahNumber,
		Ordinal:         planned.ordinal,
		Role:            d.role,
		SourceUpdatedAt: d.sourceUpdatedAt,
	}
	if d.role == entity.QuranUnitRoleTranslation || d.role == entity.QuranUnitRoleFootnote {
		sourceID := d.sourceID
		binding.TranslationSourceID = &sourceID
	}

	if d.role == entity.QuranUnitRoleTransliteration {
		sourceID := d.sourceID
		binding.TransliterationSourceID = &sourceID
	}
	if d.footnoteKey != "" {
		key := d.footnoteKey
		binding.FootnoteKey = &key
	}

	return entity.QuranUnitMint{Unit: unit, Binding: binding}
}

func quranSlotKey(ayahNumber int, role, sourceID, footnoteKey string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", ayahNumber, role, sourceID, footnoteKey)
}

//nolint:wsl_v5 // optional source identities collapse into one deterministic slot key
func quranBindingSlotKey(binding *entity.QuranCitableUnitBinding) string {
	sourceID := "qpc-hafs"
	if binding.TranslationSourceID != nil {
		sourceID = *binding.TranslationSourceID
	}

	if binding.TransliterationSourceID != nil {
		sourceID = *binding.TransliterationSourceID
	}
	footnoteKey := ""
	if binding.FootnoteKey != nil {
		footnoteKey = *binding.FootnoteKey
	}

	return quranSlotKey(binding.AyahNumber, binding.Role, sourceID, footnoteKey)
}

package persistent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	quranEditorialWriterSetting = "quran-editorial-service"
	quranEditorialRevisionKeep  = 50
)

const quranSurahEditorialColumns = `
surah_id, lang, status, meta_title, meta_description, arti_nama,
keutamaan_html, asbabun_nuzul_html, pokok_kandungan_html,
author_name, reviewed_by, reviewed_at, license_status, checksum, metadata,
updated_by::text, created_at, updated_at, published_at`

const quranAyahEditorialColumns = `
surah_id, ayah_number, ayah_key, lang, status, meta_title, meta_description,
intisari_html, keutamaan_html, faq, tafsir_range,
author_name, reviewed_by, reviewed_at, license_status, checksum, metadata,
updated_by::text, created_at, updated_at, published_at`

var _ repo.QuranEditorialRepo = (*EditorialRepo)(nil)

// QuranSurahMetadataUpdate carries the language-independent fields owned by
// the surah editorial importer. Nil values preserve the stored value.
type QuranSurahMetadataUpdate struct {
	SurahID            int
	Slug               *string
	ChronologicalOrder *int
	RukuCount          *int
}

// QuranSurahEditorialPatch preserves the importer's legacy partial-update
// contract. Nil content/provenance fields keep the current draft (or published
// baseline); LicenseStatus is applied only to a new resource or when
// LicenseOverride is true.
type QuranSurahEditorialPatch struct {
	SurahID            int
	Lang               string
	MetaTitle          *string
	MetaDescription    *string
	ArtiNama           *string
	KeutamaanHTML      *string
	AsbabunNuzulHTML   *string
	PokokKandunganHTML *string
	AuthorName         *string
	ReviewedBy         *string
	ReviewedAt         *time.Time
	LicenseStatus      string
	LicenseOverride    bool
}

// QuranAyahEditorialPatch is the atomic partial-update form used by the ayah
// importer. FAQ changes only when FAQProvided is true; metadata is deliberately
// absent because the source format does not own it.
type QuranAyahEditorialPatch struct {
	SurahID         int
	AyahNumber      int
	Lang            string
	MetaTitle       *string
	MetaDescription *string
	IntisariHTML    *string
	KeutamaanHTML   *string
	FAQ             []entity.QuranAyahEditorialFAQ
	FAQProvided     bool
	TafsirRange     *string
	AuthorName      *string
	ReviewedBy      *string
	ReviewedAt      *time.Time
	LicenseStatus   string
	LicenseOverride bool
}

// GetSurahEditorialWorkspace returns draft and published rows for one logical
// resource. An editorial resource with neither row does not yet exist.
func (r *EditorialRepo) GetSurahEditorialWorkspace(
	ctx context.Context,
	surahID int,
	lang string,
) (entity.QuranSurahEditorialWorkspace, error) {
	workspace, err := querySurahEditorialWorkspace(ctx, r.Pool, surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.GetSurahEditorialWorkspace: %w", err,
		)
	}

	if workspace.Draft == nil && workspace.Published == nil {
		return entity.QuranSurahEditorialWorkspace{}, entity.ErrDraftNotFound
	}

	return workspace, nil
}

// SaveSurahEditorialDraft creates or replaces the complete draft snapshot.
// A nil expected timestamp is the explicit If-Match:* escape hatch.
//
//nolint:dupl,gocritic // symmetric resource flow; value parameter is fixed by the repo interface
func (r *EditorialRepo) SaveSurahEditorialDraft(
	ctx context.Context,
	actorID string,
	edit entity.QuranSurahEditorialEdit,
	expected *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.SaveSurahEditorialDraft begin: %w", err,
		)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, markerErr
	}

	workspace, _, err := saveSurahEditorialDraftTx(ctx, tx, actorID, &edit, expected, origin)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.SaveSurahEditorialDraft: %w", err,
		)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.SaveSurahEditorialDraft commit: %w", err,
		)
	}

	return workspace, nil
}

// PublishSurahEditorialDraft copies the current permitted draft into the
// published row while retaining the draft as the editor workspace.
func (r *EditorialRepo) PublishSurahEditorialDraft(
	ctx context.Context,
	actorID string,
	surahID int,
	lang string,
	expected *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.PublishSurahEditorialDraft begin: %w", err,
		)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, markerErr
	}

	workspace, _, err := publishSurahEditorialDraftTx(
		ctx, tx, actorID, surahID, lang, expected, origin,
	)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.PublishSurahEditorialDraft: %w", err,
		)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.PublishSurahEditorialDraft commit: %w", err,
		)
	}

	return workspace, nil
}

// RestoreSurahEditorialRevision restores an exact historical snapshot into a
// draft. It never changes the published row.
func (r *EditorialRepo) RestoreSurahEditorialRevision(
	ctx context.Context,
	actorID string,
	surahID int,
	lang,
	revisionID string,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.RestoreSurahEditorialRevision begin: %w", err,
		)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, markerErr
	}

	if lockErr := lockQuranSurah(ctx, tx, surahID); lockErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, lockErr
	}

	workspace, err := querySurahEditorialWorkspace(ctx, tx, surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf("load workspace: %w", err)
	}

	if expectedErr := ensureSurahEditorialExpected(workspace, expected); expectedErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, expectedErr
	}

	revision, err := getQuranEditorialRevisionTx(
		ctx, tx, revisionID, entity.QuranEditorialAssetSurah, surahID, nil, lang,
	)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, err
	}

	var restored entity.QuranSurahEditorialEdit
	if decodeErr := json.Unmarshal(revision.Snapshot, &restored); decodeErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf("decode revision snapshot: %w", decodeErr)
	}

	restored.SurahID = surahID
	restored.Lang = lang
	restored.Status = entity.EditStatusDraft

	workspace, _, err = saveSurahEditorialDraftLockedTx(
		ctx, tx, actorID, &restored, workspace, entity.EditOriginRestore,
	)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf("restore draft: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.QuranSurahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.RestoreSurahEditorialRevision commit: %w", err,
		)
	}

	return workspace, nil
}

// GetAyahEditorialWorkspace returns draft and published rows for one ayah.
func (r *EditorialRepo) GetAyahEditorialWorkspace(
	ctx context.Context,
	ayahKey,
	lang string,
) (entity.QuranAyahEditorialWorkspace, error) {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(ayahKey)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrInvalidAyahKey
	}

	workspace, err := queryAyahEditorialWorkspace(ctx, r.Pool, surahID, ayahNumber, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.GetAyahEditorialWorkspace: %w", err,
		)
	}

	if workspace.Draft == nil && workspace.Published == nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrDraftNotFound
	}

	return workspace, nil
}

// SaveAyahEditorialDraft creates or replaces the complete ayah draft.
//
//nolint:dupl,gocritic // symmetric resource flow; value parameter is fixed by the repo interface
func (r *EditorialRepo) SaveAyahEditorialDraft(
	ctx context.Context,
	actorID string,
	edit entity.QuranAyahEditorialEdit,
	expected *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.SaveAyahEditorialDraft begin: %w", err,
		)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, markerErr
	}

	workspace, _, err := saveAyahEditorialDraftTx(ctx, tx, actorID, &edit, expected, origin)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.SaveAyahEditorialDraft: %w", err,
		)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.SaveAyahEditorialDraft commit: %w", err,
		)
	}

	return workspace, nil
}

// PublishAyahEditorialDraft copies the current permitted draft into the
// published row while retaining the draft.
func (r *EditorialRepo) PublishAyahEditorialDraft(
	ctx context.Context,
	actorID,
	ayahKey,
	lang string,
	expected *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, error) {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(ayahKey)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrInvalidAyahKey
	}

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.PublishAyahEditorialDraft begin: %w", err,
		)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, markerErr
	}

	workspace, _, err := publishAyahEditorialDraftTx(
		ctx, tx, actorID, surahID, ayahNumber, lang, expected, origin,
	)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.PublishAyahEditorialDraft: %w", err,
		)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.PublishAyahEditorialDraft commit: %w", err,
		)
	}

	return workspace, nil
}

// RestoreAyahEditorialRevision restores an exact historical snapshot into a
// draft. It never changes the published row.
//
//nolint:cyclop,funlen,gocyclo // linear transaction flow with explicit domain-error exits
func (r *EditorialRepo) RestoreAyahEditorialRevision(
	ctx context.Context,
	actorID,
	ayahKey,
	lang,
	revisionID string,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(ayahKey)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, entity.ErrInvalidAyahKey
	}

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.RestoreAyahEditorialRevision begin: %w", err,
		)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, markerErr
	}

	if lockErr := lockQuranAyah(ctx, tx, surahID, ayahNumber); lockErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, lockErr
	}

	workspace, err := queryAyahEditorialWorkspace(ctx, tx, surahID, ayahNumber, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf("load workspace: %w", err)
	}

	if expectedErr := ensureAyahEditorialExpected(workspace, expected); expectedErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, expectedErr
	}

	revision, err := getQuranEditorialRevisionTx(
		ctx, tx, revisionID, entity.QuranEditorialAssetAyah, surahID, &ayahNumber, lang,
	)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, err
	}

	var restored entity.QuranAyahEditorialEdit
	if decodeErr := json.Unmarshal(revision.Snapshot, &restored); decodeErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf("decode revision snapshot: %w", decodeErr)
	}

	restored.SurahID = surahID
	restored.AyahNumber = ayahNumber
	restored.AyahKey = quranutil.AyahKey(surahID, ayahNumber)
	restored.Lang = lang
	restored.Status = entity.EditStatusDraft

	workspace, _, err = saveAyahEditorialDraftLockedTx(
		ctx, tx, actorID, &restored, workspace, entity.EditOriginRestore,
	)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf("restore draft: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.QuranAyahEditorialWorkspace{}, fmt.Errorf(
			"EditorialRepo.RestoreAyahEditorialRevision commit: %w", err,
		)
	}

	return workspace, nil
}

// ListQuranEditorialRevisions returns one resource's retained history newest
// first. The caller clamps pagination before reaching the repository.
func (r *EditorialRepo) ListQuranEditorialRevisions(
	ctx context.Context,
	filter repo.QuranEditorialRevisionFilter,
) ([]entity.QuranEditorialRevision, int, error) {
	where := `
WHERE resource_type = $1
  AND surah_id = $2
  AND COALESCE(ayah_number, 0) = COALESCE($3::integer, 0)
  AND lang = $4`

	var total int
	if err := r.Pool.QueryRow(
		ctx,
		`SELECT count(*) FROM quran_editorial_revisions `+where,
		filter.AssetType,
		filter.SurahID,
		filter.AyahNumber,
		filter.Lang,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranEditorialRevisions count: %w", err)
	}

	rows, err := r.Pool.Query(
		ctx, `
SELECT id::text, resource_type, surah_id, ayah_number,
       CASE WHEN ayah_number IS NULL THEN NULL
            ELSE surah_id::text || ':' || ayah_number::text END,
       lang, version, status, actor_id::text, origin, snapshot, created_at
FROM quran_editorial_revisions `+where+`
ORDER BY version DESC
LIMIT $5 OFFSET $6`,
		filter.AssetType,
		filter.SurahID,
		filter.AyahNumber,
		filter.Lang,
		filter.Limit,
		filter.Offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranEditorialRevisions query: %w", err)
	}
	defer rows.Close()

	revisions := make([]entity.QuranEditorialRevision, 0)

	for rows.Next() {
		revision, scanErr := scanQuranEditorialRevision(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("EditorialRepo.ListQuranEditorialRevisions scan: %w", scanErr)
		}

		revisions = append(revisions, revision)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranEditorialRevisions rows: %w", err)
	}

	return revisions, total, nil
}

// ImportSurahEditorialBatch applies one complete importer run atomically. The
// default path writes draft only. Explicit publish preflights every effective
// draft before publishing any row and only then applies public surah metadata.
//
//nolint:cyclop,funlen,gocognit,gocyclo // one transaction spells out save, preflight, publish, metadata
func (r *EditorialRepo) ImportSurahEditorialBatch(
	ctx context.Context,
	patches []QuranSurahEditorialPatch,
	metadata []QuranSurahMetadataUpdate,
	publish bool,
) (changed, published int, err error) {
	patches = append([]QuranSurahEditorialPatch(nil), patches...)

	metadata = append([]QuranSurahMetadataUpdate(nil), metadata...)

	if err := sortAndValidateSurahImport(patches, metadata); err != nil {
		return 0, 0, err
	}

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("EditorialRepo.ImportSurahEditorialBatch begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return 0, 0, markerErr
	}

	if lockErr := lockSurahImportParents(ctx, tx, patches, metadata, publish); lockErr != nil {
		return 0, 0, lockErr
	}

	workspaces := make([]entity.QuranSurahEditorialWorkspace, len(patches))

	for i := range patches {
		workspace, didChange, saveErr := applySurahEditorialImportPatchTx(ctx, tx, &patches[i])
		if saveErr != nil {
			return 0, 0, fmt.Errorf("save surah %d %s: %w", patches[i].SurahID, patches[i].Lang, saveErr)
		}

		workspaces[i] = workspace

		if didChange {
			changed++
		}
	}

	if !publish {
		if err = tx.Commit(ctx); err != nil {
			return 0, 0, fmt.Errorf("EditorialRepo.ImportSurahEditorialBatch commit: %w", err)
		}

		return changed, 0, nil
	}

	for i := range workspaces {
		if workspaces[i].Draft == nil ||
			workspaces[i].Draft.LicenseStatus != entity.LicenseStatusPermitted {
			return 0, 0, entity.ErrLicenseNotPermitted
		}
	}

	for i := range patches {
		_, didPublish, publishErr := publishSurahEditorialDraftTx(
			ctx, tx, "", patches[i].SurahID, patches[i].Lang, nil, entity.EditOriginImport,
		)
		if publishErr != nil {
			return 0, 0, fmt.Errorf(
				"publish surah %d %s: %w", patches[i].SurahID, patches[i].Lang, publishErr,
			)
		}

		if didPublish {
			published++
		}
	}

	for i := range metadata {
		if err = applyQuranSurahMetadataTx(ctx, tx, metadata[i]); err != nil {
			return 0, 0, fmt.Errorf("update surah %d metadata: %w", metadata[i].SurahID, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("EditorialRepo.ImportSurahEditorialBatch commit: %w", err)
	}

	return changed, published, nil
}

// ImportAyahEditorialBatch applies one complete ayah importer run atomically.
// Publishing is an explicit, all-or-nothing second phase after license preflight.
//
//nolint:cyclop,funlen,gocognit,gocyclo // one transaction intentionally spells out save, preflight, publish
func (r *EditorialRepo) ImportAyahEditorialBatch(
	ctx context.Context,
	patches []QuranAyahEditorialPatch,
	publish bool,
) (changed, published int, err error) {
	patches = append([]QuranAyahEditorialPatch(nil), patches...)

	if err := sortAndValidateAyahImport(patches); err != nil {
		return 0, 0, err
	}

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("EditorialRepo.ImportAyahEditorialBatch begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return 0, 0, markerErr
	}

	workspaces := make([]entity.QuranAyahEditorialWorkspace, len(patches))

	for i := range patches {
		workspace, didChange, saveErr := applyAyahEditorialImportPatchTx(ctx, tx, &patches[i])
		if saveErr != nil {
			return 0, 0, fmt.Errorf(
				"save ayah %s %s: %w",
				quranutil.AyahKey(patches[i].SurahID, patches[i].AyahNumber),
				patches[i].Lang,
				saveErr,
			)
		}

		workspaces[i] = workspace

		if didChange {
			changed++
		}
	}

	if !publish {
		if err = tx.Commit(ctx); err != nil {
			return 0, 0, fmt.Errorf("EditorialRepo.ImportAyahEditorialBatch commit: %w", err)
		}

		return changed, 0, nil
	}

	for i := range workspaces {
		if workspaces[i].Draft == nil ||
			workspaces[i].Draft.LicenseStatus != entity.LicenseStatusPermitted {
			return 0, 0, entity.ErrLicenseNotPermitted
		}
	}

	for i := range patches {
		_, didPublish, publishErr := publishAyahEditorialDraftTx(
			ctx,
			tx,
			"",
			patches[i].SurahID,
			patches[i].AyahNumber,
			patches[i].Lang,
			nil,
			entity.EditOriginImport,
		)
		if publishErr != nil {
			return 0, 0, fmt.Errorf(
				"publish ayah %s %s: %w",
				quranutil.AyahKey(patches[i].SurahID, patches[i].AyahNumber),
				patches[i].Lang,
				publishErr,
			)
		}

		if didPublish {
			published++
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("EditorialRepo.ImportAyahEditorialBatch commit: %w", err)
	}

	return changed, published, nil
}

func markQuranEditorialWriter(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SET LOCAL surau.quran_editorial_writer = '`+quranEditorialWriterSetting+`'`); err != nil {
		return fmt.Errorf("set Quran editorial writer marker: %w", err)
	}

	return nil
}

func applySurahEditorialImportPatchTx(
	ctx context.Context,
	tx pgx.Tx,
	patch *QuranSurahEditorialPatch,
) (entity.QuranSurahEditorialWorkspace, bool, error) {
	if err := lockQuranSurah(ctx, tx, patch.SurahID); err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, err
	}

	workspace, err := querySurahEditorialWorkspace(ctx, tx, patch.SurahID, patch.Lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, fmt.Errorf("load workspace: %w", err)
	}

	edit := mergeSurahEditorialImportPatch(workspace, patch)

	return saveSurahEditorialDraftLockedTx(
		ctx, tx, "", &edit, workspace, entity.EditOriginImport,
	)
}

func applyAyahEditorialImportPatchTx(
	ctx context.Context,
	tx pgx.Tx,
	patch *QuranAyahEditorialPatch,
) (entity.QuranAyahEditorialWorkspace, bool, error) {
	if err := lockQuranAyah(ctx, tx, patch.SurahID, patch.AyahNumber); err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, err
	}

	workspace, err := queryAyahEditorialWorkspace(
		ctx, tx, patch.SurahID, patch.AyahNumber, patch.Lang,
	)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, fmt.Errorf("load workspace: %w", err)
	}

	edit := mergeAyahEditorialImportPatch(workspace, patch)

	return saveAyahEditorialDraftLockedTx(
		ctx, tx, "", &edit, workspace, entity.EditOriginImport,
	)
}

func mergeSurahEditorialImportPatch(
	workspace entity.QuranSurahEditorialWorkspace,
	patch *QuranSurahEditorialPatch,
) entity.QuranSurahEditorialEdit {
	edit, existed := baseSurahEditorialEdit(workspace)
	edit.SurahID = patch.SurahID
	edit.Lang = patch.Lang
	edit.Status = entity.EditStatusDraft
	overlayString(&edit.MetaTitle, patch.MetaTitle)
	overlayString(&edit.MetaDescription, patch.MetaDescription)
	overlayString(&edit.ArtiNama, patch.ArtiNama)
	overlayString(&edit.Keutamaan, patch.KeutamaanHTML)
	overlayString(&edit.AsbabunNuzul, patch.AsbabunNuzulHTML)
	overlayString(&edit.PokokKandungan, patch.PokokKandunganHTML)
	overlayString(&edit.AuthorName, patch.AuthorName)
	overlayString(&edit.ReviewedBy, patch.ReviewedBy)
	overlayTime(&edit.ReviewedAt, patch.ReviewedAt)
	applyImportLicense(&edit.LicenseStatus, existed, patch.LicenseStatus, patch.LicenseOverride)

	return edit
}

func mergeAyahEditorialImportPatch(
	workspace entity.QuranAyahEditorialWorkspace,
	patch *QuranAyahEditorialPatch,
) entity.QuranAyahEditorialEdit {
	edit, existed := baseAyahEditorialEdit(workspace)
	edit.SurahID = patch.SurahID
	edit.AyahNumber = patch.AyahNumber
	edit.AyahKey = quranutil.AyahKey(patch.SurahID, patch.AyahNumber)
	edit.Lang = patch.Lang
	edit.Status = entity.EditStatusDraft
	overlayString(&edit.MetaTitle, patch.MetaTitle)
	overlayString(&edit.MetaDescription, patch.MetaDescription)
	overlayString(&edit.Intisari, patch.IntisariHTML)
	overlayString(&edit.Keutamaan, patch.KeutamaanHTML)
	overlayString(&edit.TafsirRange, patch.TafsirRange)
	overlayString(&edit.AuthorName, patch.AuthorName)
	overlayString(&edit.ReviewedBy, patch.ReviewedBy)
	overlayTime(&edit.ReviewedAt, patch.ReviewedAt)

	if patch.FAQProvided {
		edit.FAQ = append([]entity.QuranAyahEditorialFAQ(nil), patch.FAQ...)
	}

	applyImportLicense(&edit.LicenseStatus, existed, patch.LicenseStatus, patch.LicenseOverride)

	return edit
}

func baseSurahEditorialEdit(
	workspace entity.QuranSurahEditorialWorkspace,
) (entity.QuranSurahEditorialEdit, bool) {
	if workspace.Draft != nil {
		return *workspace.Draft, true
	}

	if workspace.Published != nil {
		return *workspace.Published, true
	}

	return entity.QuranSurahEditorialEdit{}, false
}

func baseAyahEditorialEdit(
	workspace entity.QuranAyahEditorialWorkspace,
) (entity.QuranAyahEditorialEdit, bool) {
	if workspace.Draft != nil {
		return *workspace.Draft, true
	}

	if workspace.Published != nil {
		return *workspace.Published, true
	}

	return entity.QuranAyahEditorialEdit{}, false
}

func overlayString(target **string, patch *string) {
	if patch != nil {
		*target = patch
	}
}

func overlayTime(target **time.Time, patch *time.Time) {
	if patch != nil {
		*target = patch
	}
}

func applyImportLicense(target *string, existed bool, status string, override bool) {
	if status == "" {
		status = entity.LicenseStatusNeedsReview
	}

	if !existed || override {
		*target = status
	}
}

func saveSurahEditorialDraftTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	edit *entity.QuranSurahEditorialEdit,
	expected *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, bool, error) {
	if err := lockQuranSurah(ctx, tx, edit.SurahID); err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, err
	}

	workspace, err := querySurahEditorialWorkspace(ctx, tx, edit.SurahID, edit.Lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, fmt.Errorf("load workspace: %w", err)
	}

	if expectedErr := ensureSurahEditorialExpected(workspace, expected); expectedErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, expectedErr
	}

	return saveSurahEditorialDraftLockedTx(ctx, tx, actorID, edit, workspace, origin)
}

//nolint:funlen // one full-row SQL mapping keeps snapshot writes auditable
func saveSurahEditorialDraftLockedTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	edit *entity.QuranSurahEditorialEdit,
	workspace entity.QuranSurahEditorialWorkspace,
	origin string,
) (entity.QuranSurahEditorialWorkspace, bool, error) {
	prepared, err := prepareSurahEditorialEdit(edit)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, err
	}

	if workspace.Draft != nil && equalSurahEditorialContent(workspace.Draft, &prepared) {
		return workspace, false, nil
	}

	var previous *time.Time
	if current := workspace.CurrentUpdatedAt(); !current.IsZero() {
		previous = &current
	}

	saved, err := scanQuranSurahEditorialEdit(tx.QueryRow(
		ctx, `
INSERT INTO quran_surah_editorial (
    surah_id, lang, status, meta_title, meta_description, arti_nama,
    keutamaan_html, asbabun_nuzul_html, pokok_kandungan_html,
    author_name, reviewed_by, reviewed_at, license_status, checksum, metadata,
    updated_by, created_at, updated_at, published_at
)
VALUES (
    $1, $2, 'draft', $3, $4, $5, $6, $7, $8,
    $9, $10, $11, $12, $13, $14::jsonb,
    NULLIF($15, '')::uuid, clock_timestamp(),
    GREATEST(clock_timestamp(), COALESCE($16::timestamptz, '-infinity') + interval '1 microsecond'),
    NULL
)
ON CONFLICT (surah_id, lang, status) DO UPDATE SET
    meta_title = EXCLUDED.meta_title,
    meta_description = EXCLUDED.meta_description,
    arti_nama = EXCLUDED.arti_nama,
    keutamaan_html = EXCLUDED.keutamaan_html,
    asbabun_nuzul_html = EXCLUDED.asbabun_nuzul_html,
    pokok_kandungan_html = EXCLUDED.pokok_kandungan_html,
    author_name = EXCLUDED.author_name,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = EXCLUDED.metadata,
    updated_by = EXCLUDED.updated_by,
    updated_at = EXCLUDED.updated_at,
    published_at = NULL
RETURNING `+quranSurahEditorialColumns,
		prepared.SurahID,
		prepared.Lang,
		prepared.MetaTitle,
		prepared.MetaDescription,
		prepared.ArtiNama,
		prepared.Keutamaan,
		prepared.AsbabunNuzul,
		prepared.PokokKandungan,
		prepared.AuthorName,
		prepared.ReviewedBy,
		prepared.ReviewedAt,
		prepared.LicenseStatus,
		prepared.Checksum,
		string(prepared.Metadata),
		actorID,
		previous,
	))
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, mapQuranEditorialWriteError(err)
	}

	if revisionErr := insertQuranEditorialRevision(
		ctx,
		tx,
		entity.QuranEditorialAssetSurah,
		saved.SurahID,
		nil,
		saved.Lang,
		saved.Status,
		actorID,
		origin,
	); revisionErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, revisionErr
	}

	workspace.Draft = &saved

	return workspace, true, nil
}

func publishSurahEditorialDraftTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	surahID int,
	lang string,
	expected *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, bool, error) {
	if err := lockQuranSurah(ctx, tx, surahID); err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, err
	}

	workspace, err := querySurahEditorialWorkspace(ctx, tx, surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, fmt.Errorf("load workspace: %w", err)
	}

	if expectedErr := ensureSurahEditorialExpected(workspace, expected); expectedErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, expectedErr
	}

	if workspace.Draft == nil {
		return entity.QuranSurahEditorialWorkspace{}, false, entity.ErrDraftNotFound
	}

	if workspace.Draft.LicenseStatus != entity.LicenseStatusPermitted {
		return entity.QuranSurahEditorialWorkspace{}, false, entity.ErrLicenseNotPermitted
	}

	if workspace.Published != nil && equalSurahEditorialContent(workspace.Published, workspace.Draft) {
		return workspace, false, nil
	}

	published, err := writePublishedSurahEditorialTx(ctx, tx, actorID, workspace)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, err
	}

	if revisionErr := insertQuranEditorialRevision(
		ctx,
		tx,
		entity.QuranEditorialAssetSurah,
		surahID,
		nil,
		lang,
		entity.EditStatusPublished,
		actorID,
		origin,
	); revisionErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, false, revisionErr
	}

	workspace.Published = &published
	workspace.Draft.UpdatedAt = published.UpdatedAt
	workspace.Draft.UpdatedBy = published.UpdatedBy

	return workspace, true, nil
}

//nolint:funlen // one full-row SQL mapping keeps publish copying auditable
func writePublishedSurahEditorialTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	workspace entity.QuranSurahEditorialWorkspace,
) (entity.QuranSurahEditorialEdit, error) {
	draft := workspace.Draft
	previous := workspace.CurrentUpdatedAt()

	if workspace.Published != nil && workspace.Published.UpdatedAt.After(previous) {
		previous = workspace.Published.UpdatedAt
	}

	var token time.Time
	if err := tx.QueryRow(ctx, `
SELECT GREATEST(clock_timestamp(), $1::timestamptz + interval '1 microsecond')`, previous).Scan(&token); err != nil {
		return entity.QuranSurahEditorialEdit{}, fmt.Errorf("allocate publish token: %w", err)
	}

	if _, err := tx.Exec(
		ctx, `
UPDATE quran_surah_editorial
SET updated_by = NULLIF($3, '')::uuid,
    updated_at = $4
WHERE surah_id = $1 AND lang = $2 AND status = 'draft'`,
		draft.SurahID, draft.Lang, actorID, token,
	); err != nil {
		return entity.QuranSurahEditorialEdit{}, fmt.Errorf("advance draft token: %w", err)
	}

	published, err := scanQuranSurahEditorialEdit(tx.QueryRow(
		ctx, `
INSERT INTO quran_surah_editorial (
    surah_id, lang, status, meta_title, meta_description, arti_nama,
    keutamaan_html, asbabun_nuzul_html, pokok_kandungan_html,
    author_name, reviewed_by, reviewed_at, license_status, checksum, metadata,
    updated_by, created_at, updated_at, published_at
)
VALUES (
    $1, $2, 'published', $3, $4, $5, $6, $7, $8,
    $9, $10, $11, $12, $13, $14::jsonb,
    NULLIF($15, '')::uuid, clock_timestamp(), $16, $16
)
ON CONFLICT (surah_id, lang, status) DO UPDATE SET
    meta_title = EXCLUDED.meta_title,
    meta_description = EXCLUDED.meta_description,
    arti_nama = EXCLUDED.arti_nama,
    keutamaan_html = EXCLUDED.keutamaan_html,
    asbabun_nuzul_html = EXCLUDED.asbabun_nuzul_html,
    pokok_kandungan_html = EXCLUDED.pokok_kandungan_html,
    author_name = EXCLUDED.author_name,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = EXCLUDED.metadata,
    updated_by = EXCLUDED.updated_by,
    updated_at = EXCLUDED.updated_at,
    published_at = EXCLUDED.published_at
RETURNING `+quranSurahEditorialColumns,
		draft.SurahID,
		draft.Lang,
		draft.MetaTitle,
		draft.MetaDescription,
		draft.ArtiNama,
		draft.Keutamaan,
		draft.AsbabunNuzul,
		draft.PokokKandungan,
		draft.AuthorName,
		draft.ReviewedBy,
		draft.ReviewedAt,
		draft.LicenseStatus,
		draft.Checksum,
		string(draft.Metadata),
		actorID,
		token,
	))
	if err != nil {
		return entity.QuranSurahEditorialEdit{}, mapQuranEditorialWriteError(err)
	}

	return published, nil
}

func saveAyahEditorialDraftTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	edit *entity.QuranAyahEditorialEdit,
	expected *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, bool, error) {
	if err := lockQuranAyah(ctx, tx, edit.SurahID, edit.AyahNumber); err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, err
	}

	workspace, err := queryAyahEditorialWorkspace(ctx, tx, edit.SurahID, edit.AyahNumber, edit.Lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, fmt.Errorf("load workspace: %w", err)
	}

	if expectedErr := ensureAyahEditorialExpected(workspace, expected); expectedErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, expectedErr
	}

	return saveAyahEditorialDraftLockedTx(ctx, tx, actorID, edit, workspace, origin)
}

//nolint:funlen // one full-row SQL mapping keeps snapshot writes auditable
func saveAyahEditorialDraftLockedTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	edit *entity.QuranAyahEditorialEdit,
	workspace entity.QuranAyahEditorialWorkspace,
	origin string,
) (entity.QuranAyahEditorialWorkspace, bool, error) {
	prepared, err := prepareAyahEditorialEdit(edit)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, err
	}

	if workspace.Draft != nil && equalAyahEditorialContent(workspace.Draft, &prepared) {
		return workspace, false, nil
	}

	var previous *time.Time
	if current := workspace.CurrentUpdatedAt(); !current.IsZero() {
		previous = &current
	}

	faq, err := json.Marshal(prepared.FAQ)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, fmt.Errorf("marshal FAQ: %w", err)
	}

	saved, err := scanQuranAyahEditorialEdit(tx.QueryRow(
		ctx, `
INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang, status,
    meta_title, meta_description, intisari_html, keutamaan_html,
    faq, tafsir_range, author_name, reviewed_by, reviewed_at,
    license_status, checksum, metadata, updated_by, created_at, updated_at, published_at
)
VALUES (
    $1, $2, $3, $4, 'draft', $5, $6, $7, $8,
    $9::jsonb, $10, $11, $12, $13, $14, $15, $16::jsonb,
    NULLIF($17, '')::uuid, clock_timestamp(),
    GREATEST(clock_timestamp(), COALESCE($18::timestamptz, '-infinity') + interval '1 microsecond'),
    NULL
)
ON CONFLICT (surah_id, ayah_number, lang, status) DO UPDATE SET
    ayah_key = EXCLUDED.ayah_key,
    meta_title = EXCLUDED.meta_title,
    meta_description = EXCLUDED.meta_description,
    intisari_html = EXCLUDED.intisari_html,
    keutamaan_html = EXCLUDED.keutamaan_html,
    faq = EXCLUDED.faq,
    tafsir_range = EXCLUDED.tafsir_range,
    author_name = EXCLUDED.author_name,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = EXCLUDED.metadata,
    updated_by = EXCLUDED.updated_by,
    updated_at = EXCLUDED.updated_at,
    published_at = NULL
RETURNING `+quranAyahEditorialColumns,
		prepared.SurahID,
		prepared.AyahNumber,
		prepared.AyahKey,
		prepared.Lang,
		prepared.MetaTitle,
		prepared.MetaDescription,
		prepared.Intisari,
		prepared.Keutamaan,
		string(faq),
		prepared.TafsirRange,
		prepared.AuthorName,
		prepared.ReviewedBy,
		prepared.ReviewedAt,
		prepared.LicenseStatus,
		prepared.Checksum,
		string(prepared.Metadata),
		actorID,
		previous,
	))
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, mapQuranEditorialWriteError(err)
	}

	ayahNumber := saved.AyahNumber
	if revisionErr := insertQuranEditorialRevision(
		ctx,
		tx,
		entity.QuranEditorialAssetAyah,
		saved.SurahID,
		&ayahNumber,
		saved.Lang,
		saved.Status,
		actorID,
		origin,
	); revisionErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, revisionErr
	}

	workspace.Draft = &saved

	return workspace, true, nil
}

func publishAyahEditorialDraftTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	surahID,
	ayahNumber int,
	lang string,
	expected *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, bool, error) {
	if err := lockQuranAyah(ctx, tx, surahID, ayahNumber); err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, err
	}

	workspace, err := queryAyahEditorialWorkspace(ctx, tx, surahID, ayahNumber, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, fmt.Errorf("load workspace: %w", err)
	}

	if expectedErr := ensureAyahEditorialExpected(workspace, expected); expectedErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, expectedErr
	}

	if workspace.Draft == nil {
		return entity.QuranAyahEditorialWorkspace{}, false, entity.ErrDraftNotFound
	}

	if workspace.Draft.LicenseStatus != entity.LicenseStatusPermitted {
		return entity.QuranAyahEditorialWorkspace{}, false, entity.ErrLicenseNotPermitted
	}

	if workspace.Published != nil && equalAyahEditorialContent(workspace.Published, workspace.Draft) {
		return workspace, false, nil
	}

	published, err := writePublishedAyahEditorialTx(ctx, tx, actorID, workspace)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, err
	}

	if revisionErr := insertQuranEditorialRevision(
		ctx,
		tx,
		entity.QuranEditorialAssetAyah,
		surahID,
		&ayahNumber,
		lang,
		entity.EditStatusPublished,
		actorID,
		origin,
	); revisionErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, false, revisionErr
	}

	workspace.Published = &published
	workspace.Draft.UpdatedAt = published.UpdatedAt
	workspace.Draft.UpdatedBy = published.UpdatedBy

	return workspace, true, nil
}

//nolint:funlen // one full-row SQL mapping keeps publish copying auditable
func writePublishedAyahEditorialTx(
	ctx context.Context,
	tx pgx.Tx,
	actorID string,
	workspace entity.QuranAyahEditorialWorkspace,
) (entity.QuranAyahEditorialEdit, error) {
	draft := workspace.Draft

	previous := workspace.CurrentUpdatedAt()

	if workspace.Published != nil && workspace.Published.UpdatedAt.After(previous) {
		previous = workspace.Published.UpdatedAt
	}

	var token time.Time
	if err := tx.QueryRow(ctx, `
SELECT GREATEST(clock_timestamp(), $1::timestamptz + interval '1 microsecond')`, previous).Scan(&token); err != nil {
		return entity.QuranAyahEditorialEdit{}, fmt.Errorf("allocate publish token: %w", err)
	}

	if _, err := tx.Exec(
		ctx, `
UPDATE quran_ayah_editorial
SET updated_by = NULLIF($4, '')::uuid,
    updated_at = $5
WHERE surah_id = $1 AND ayah_number = $2 AND lang = $3 AND status = 'draft'`,
		draft.SurahID, draft.AyahNumber, draft.Lang, actorID, token,
	); err != nil {
		return entity.QuranAyahEditorialEdit{}, fmt.Errorf("advance draft token: %w", err)
	}

	faq, err := json.Marshal(draft.FAQ)
	if err != nil {
		return entity.QuranAyahEditorialEdit{}, fmt.Errorf("marshal FAQ: %w", err)
	}

	published, err := scanQuranAyahEditorialEdit(tx.QueryRow(
		ctx, `
INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang, status,
    meta_title, meta_description, intisari_html, keutamaan_html,
    faq, tafsir_range, author_name, reviewed_by, reviewed_at,
    license_status, checksum, metadata, updated_by, created_at, updated_at, published_at
)
VALUES (
    $1, $2, $3, $4, 'published', $5, $6, $7, $8,
    $9::jsonb, $10, $11, $12, $13, $14, $15, $16::jsonb,
    NULLIF($17, '')::uuid, clock_timestamp(), $18, $18
)
ON CONFLICT (surah_id, ayah_number, lang, status) DO UPDATE SET
    ayah_key = EXCLUDED.ayah_key,
    meta_title = EXCLUDED.meta_title,
    meta_description = EXCLUDED.meta_description,
    intisari_html = EXCLUDED.intisari_html,
    keutamaan_html = EXCLUDED.keutamaan_html,
    faq = EXCLUDED.faq,
    tafsir_range = EXCLUDED.tafsir_range,
    author_name = EXCLUDED.author_name,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = EXCLUDED.metadata,
    updated_by = EXCLUDED.updated_by,
    updated_at = EXCLUDED.updated_at,
    published_at = EXCLUDED.published_at
RETURNING `+quranAyahEditorialColumns,
		draft.SurahID,
		draft.AyahNumber,
		draft.AyahKey,
		draft.Lang,
		draft.MetaTitle,
		draft.MetaDescription,
		draft.Intisari,
		draft.Keutamaan,
		string(faq),
		draft.TafsirRange,
		draft.AuthorName,
		draft.ReviewedBy,
		draft.ReviewedAt,
		draft.LicenseStatus,
		draft.Checksum,
		string(draft.Metadata),
		actorID,
		token,
	))
	if err != nil {
		return entity.QuranAyahEditorialEdit{}, mapQuranEditorialWriteError(err)
	}

	return published, nil
}

func lockQuranSurah(ctx context.Context, tx pgx.Tx, surahID int) error {
	var locked int
	if err := tx.QueryRow(ctx, `
SELECT surah_id FROM quran_surahs WHERE surah_id = $1 FOR UPDATE`, surahID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ErrQuranSurahNotFound
		}

		return fmt.Errorf("lock Quran surah: %w", err)
	}

	return nil
}

// lockSurahImportParents acquires the union of editorial and publish-metadata
// parents in ascending order. The later per-resource helpers deliberately lock
// again (a no-op for the same transaction), while this global ordering prevents
// two overlapping importer batches from deadlocking on different input shapes.
func lockSurahImportParents(
	ctx context.Context,
	tx pgx.Tx,
	patches []QuranSurahEditorialPatch,
	metadata []QuranSurahMetadataUpdate,
	publish bool,
) error {
	ids := make(map[int]struct{}, len(patches)+len(metadata))
	for i := range patches {
		ids[patches[i].SurahID] = struct{}{}
	}

	if publish {
		for i := range metadata {
			ids[metadata[i].SurahID] = struct{}{}
		}
	}

	ordered := make([]int, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}

	sort.Ints(ordered)

	for _, id := range ordered {
		if err := lockQuranSurah(ctx, tx, id); err != nil {
			return err
		}
	}

	return nil
}

func lockQuranAyah(ctx context.Context, tx pgx.Tx, surahID, ayahNumber int) error {
	var locked int
	if err := tx.QueryRow(ctx, `
SELECT ayah_number
FROM quran_ayahs
WHERE surah_id = $1 AND ayah_number = $2
FOR UPDATE`, surahID, ayahNumber).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ErrQuranAyahNotFound
		}

		return fmt.Errorf("lock Quran ayah: %w", err)
	}

	return nil
}

func querySurahEditorialWorkspace(
	ctx context.Context,
	q productionQuerier,
	surahID int,
	lang string,
) (entity.QuranSurahEditorialWorkspace, error) {
	rows, err := q.Query(ctx, `
SELECT `+quranSurahEditorialColumns+`
FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = $2
ORDER BY CASE status WHEN 'draft' THEN 0 ELSE 1 END`, surahID, lang)
	if err != nil {
		return entity.QuranSurahEditorialWorkspace{}, err
	}
	defer rows.Close()

	var workspace entity.QuranSurahEditorialWorkspace

	for rows.Next() {
		edit, scanErr := scanQuranSurahEditorialEdit(rows)
		if scanErr != nil {
			return entity.QuranSurahEditorialWorkspace{}, scanErr
		}

		switch edit.Status {
		case entity.EditStatusDraft:
			workspace.Draft = &edit
		case entity.EditStatusPublished:
			workspace.Published = &edit
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return entity.QuranSurahEditorialWorkspace{}, rowsErr
	}

	return workspace, nil
}

func queryAyahEditorialWorkspace(
	ctx context.Context,
	q productionQuerier,
	surahID,
	ayahNumber int,
	lang string,
) (entity.QuranAyahEditorialWorkspace, error) {
	rows, err := q.Query(ctx, `
SELECT `+quranAyahEditorialColumns+`
FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang = $3
ORDER BY CASE status WHEN 'draft' THEN 0 ELSE 1 END`, surahID, ayahNumber, lang)
	if err != nil {
		return entity.QuranAyahEditorialWorkspace{}, err
	}
	defer rows.Close()

	var workspace entity.QuranAyahEditorialWorkspace

	for rows.Next() {
		edit, scanErr := scanQuranAyahEditorialEdit(rows)
		if scanErr != nil {
			return entity.QuranAyahEditorialWorkspace{}, scanErr
		}

		switch edit.Status {
		case entity.EditStatusDraft:
			workspace.Draft = &edit
		case entity.EditStatusPublished:
			workspace.Published = &edit
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return entity.QuranAyahEditorialWorkspace{}, rowsErr
	}

	return workspace, nil
}

func ensureSurahEditorialExpected(
	workspace entity.QuranSurahEditorialWorkspace,
	expected *time.Time,
) error {
	if expected == nil {
		return nil
	}

	current := workspace.CurrentUpdatedAt()
	if current.IsZero() || !current.Equal(*expected) {
		return entity.ErrPreconditionFailed
	}

	return nil
}

func ensureAyahEditorialExpected(
	workspace entity.QuranAyahEditorialWorkspace,
	expected *time.Time,
) error {
	if expected == nil {
		return nil
	}

	current := workspace.CurrentUpdatedAt()
	if current.IsZero() || !current.Equal(*expected) {
		return entity.ErrPreconditionFailed
	}

	return nil
}

//nolint:funlen // append, dedupe, and bounded pruning intentionally share the caller transaction
func insertQuranEditorialRevision(
	ctx context.Context,
	tx pgx.Tx,
	assetType string,
	surahID int,
	ayahNumber *int,
	lang,
	status,
	actorID,
	origin string,
) error {
	if origin == "" {
		origin = entity.EditOriginREST
	}

	table := "quran_surah_editorial"
	where := "surah_id = $1 AND $2::integer IS NULL AND lang = $3 AND status = $4"

	if assetType == entity.QuranEditorialAssetAyah {
		table = "quran_ayah_editorial"
		where = "surah_id = $1 AND ayah_number = $2 AND lang = $3 AND status = $4"
	}

	var snapshot []byte
	if err := tx.QueryRow(
		ctx,
		"SELECT to_jsonb(editorial) FROM "+table+" editorial WHERE "+where,
		surahID,
		ayahNumber,
		lang,
		status,
	).Scan(&snapshot); err != nil {
		return fmt.Errorf("snapshot Quran editorial row: %w", err)
	}

	var version int

	err := tx.QueryRow(
		ctx, `
WITH latest AS (
    SELECT version, snapshot
    FROM quran_editorial_revisions
    WHERE resource_type = $1
      AND surah_id = $2
      AND COALESCE(ayah_number, 0) = COALESCE($3::integer, 0)
      AND lang = $4
    ORDER BY version DESC
    LIMIT 1
)
INSERT INTO quran_editorial_revisions (
    id, resource_type, surah_id, ayah_number, lang, status,
    version, actor_id, origin, snapshot, is_migration_baseline, created_at
)
SELECT $5, $1, $2, $3, $4, $6,
       COALESCE((SELECT version FROM latest), 0) + 1,
       NULLIF($7, '')::uuid, $8, $9::jsonb, false, clock_timestamp()
WHERE NOT EXISTS (SELECT 1 FROM latest WHERE snapshot = $9::jsonb)
RETURNING version`,
		assetType,
		surahID,
		ayahNumber,
		lang,
		uuid.New().String(),
		status,
		actorID,
		origin,
		string(snapshot),
	).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("insert Quran editorial revision: %w", err)
	}

	if _, err = tx.Exec(
		ctx, `
DELETE FROM quran_editorial_revisions
WHERE resource_type = $1
  AND surah_id = $2
  AND COALESCE(ayah_number, 0) = COALESCE($3::integer, 0)
  AND lang = $4
  AND version <= $5`,
		assetType,
		surahID,
		ayahNumber,
		lang,
		version-quranEditorialRevisionKeep,
	); err != nil {
		return fmt.Errorf("prune Quran editorial revisions: %w", err)
	}

	return nil
}

func getQuranEditorialRevisionTx(
	ctx context.Context,
	tx pgx.Tx,
	revisionID,
	assetType string,
	surahID int,
	ayahNumber *int,
	lang string,
) (entity.QuranEditorialRevision, error) {
	revision, err := scanQuranEditorialRevision(tx.QueryRow(ctx, `
SELECT id::text, resource_type, surah_id, ayah_number,
       CASE WHEN ayah_number IS NULL THEN NULL
            ELSE surah_id::text || ':' || ayah_number::text END,
       lang, version, status, actor_id::text, origin, snapshot, created_at
FROM quran_editorial_revisions
WHERE id = $1
  AND resource_type = $2
  AND surah_id = $3
  AND COALESCE(ayah_number, 0) = COALESCE($4::integer, 0)
  AND lang = $5`, revisionID, assetType, surahID, ayahNumber, lang))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranEditorialRevision{}, entity.ErrDraftNotFound
		}

		return entity.QuranEditorialRevision{}, fmt.Errorf("get Quran editorial revision: %w", err)
	}

	return revision, nil
}

func scanQuranSurahEditorialEdit(row rowScanner) (entity.QuranSurahEditorialEdit, error) {
	var (
		edit     entity.QuranSurahEditorialEdit
		metadata []byte
	)

	err := row.Scan(
		&edit.SurahID,
		&edit.Lang,
		&edit.Status,
		&edit.MetaTitle,
		&edit.MetaDescription,
		&edit.ArtiNama,
		&edit.Keutamaan,
		&edit.AsbabunNuzul,
		&edit.PokokKandungan,
		&edit.AuthorName,
		&edit.ReviewedBy,
		&edit.ReviewedAt,
		&edit.LicenseStatus,
		&edit.Checksum,
		&metadata,
		&edit.UpdatedBy,
		&edit.CreatedAt,
		&edit.UpdatedAt,
		&edit.PublishedAt,
	)
	if err != nil {
		return entity.QuranSurahEditorialEdit{}, err
	}

	edit.Metadata = entity.RawJSON(metadata)

	return edit, nil
}

func scanQuranAyahEditorialEdit(row rowScanner) (entity.QuranAyahEditorialEdit, error) {
	var (
		edit          entity.QuranAyahEditorialEdit
		faq, metadata []byte
	)

	err := row.Scan(
		&edit.SurahID,
		&edit.AyahNumber,
		&edit.AyahKey,
		&edit.Lang,
		&edit.Status,
		&edit.MetaTitle,
		&edit.MetaDescription,
		&edit.Intisari,
		&edit.Keutamaan,
		&faq,
		&edit.TafsirRange,
		&edit.AuthorName,
		&edit.ReviewedBy,
		&edit.ReviewedAt,
		&edit.LicenseStatus,
		&edit.Checksum,
		&metadata,
		&edit.UpdatedBy,
		&edit.CreatedAt,
		&edit.UpdatedAt,
		&edit.PublishedAt,
	)
	if err != nil {
		return entity.QuranAyahEditorialEdit{}, err
	}

	if err = json.Unmarshal(faq, &edit.FAQ); err != nil {
		return entity.QuranAyahEditorialEdit{}, fmt.Errorf("decode Quran editorial FAQ: %w", err)
	}

	if edit.FAQ == nil {
		edit.FAQ = make([]entity.QuranAyahEditorialFAQ, 0)
	}

	edit.Metadata = entity.RawJSON(metadata)

	return edit, nil
}

func scanQuranEditorialRevision(row rowScanner) (entity.QuranEditorialRevision, error) {
	var (
		revision entity.QuranEditorialRevision
		snapshot []byte
	)

	err := row.Scan(
		&revision.ID,
		&revision.AssetType,
		&revision.SurahID,
		&revision.AyahNumber,
		&revision.AyahKey,
		&revision.Lang,
		&revision.Version,
		&revision.Status,
		&revision.ActorID,
		&revision.Origin,
		&snapshot,
		&revision.CreatedAt,
	)
	if err != nil {
		return entity.QuranEditorialRevision{}, err
	}

	revision.Snapshot = entity.RawJSON(snapshot)

	return revision, nil
}

func prepareSurahEditorialEdit(
	input *entity.QuranSurahEditorialEdit,
) (entity.QuranSurahEditorialEdit, error) {
	edit := *input
	edit.Status = entity.EditStatusDraft
	edit.PublishedAt = nil

	edit.ReviewedAt = quranEditorialDatabaseTime(edit.ReviewedAt)
	if len(edit.Metadata) == 0 {
		edit.Metadata = entity.RawJSON(`{}`)
	}

	if !json.Valid(edit.Metadata) {
		return entity.QuranSurahEditorialEdit{}, entity.ErrInvalidQuranEditorial
	}

	checksum := quranSurahEditorialChecksum(&edit)
	edit.Checksum = &checksum

	return edit, nil
}

func prepareAyahEditorialEdit(
	input *entity.QuranAyahEditorialEdit,
) (entity.QuranAyahEditorialEdit, error) {
	edit := *input
	edit.Status = entity.EditStatusDraft
	edit.PublishedAt = nil
	edit.ReviewedAt = quranEditorialDatabaseTime(edit.ReviewedAt)

	edit.AyahKey = quranutil.AyahKey(edit.SurahID, edit.AyahNumber)
	if edit.FAQ == nil {
		edit.FAQ = make([]entity.QuranAyahEditorialFAQ, 0)
	}

	if len(edit.Metadata) == 0 {
		edit.Metadata = entity.RawJSON(`{}`)
	}

	if !json.Valid(edit.Metadata) {
		return entity.QuranAyahEditorialEdit{}, entity.ErrInvalidQuranEditorial
	}

	checksum, err := quranAyahEditorialChecksum(&edit)
	if err != nil {
		return entity.QuranAyahEditorialEdit{}, err
	}

	edit.Checksum = &checksum

	return edit, nil
}

func quranSurahEditorialChecksum(edit *entity.QuranSurahEditorialEdit) string {
	hash := sha256.New()

	for _, value := range []*string{
		edit.MetaTitle,
		edit.MetaDescription,
		edit.ArtiNama,
		edit.Keutamaan,
		edit.AsbabunNuzul,
		edit.PokokKandungan,
	} {
		if value != nil {
			_, _ = hash.Write([]byte(*value))
		}

		_, _ = hash.Write([]byte{0})
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func quranAyahEditorialChecksum(edit *entity.QuranAyahEditorialEdit) (string, error) {
	hash := sha256.New()
	writeOptional := func(value *string) {
		if value != nil {
			_, _ = hash.Write([]byte(*value))
		}

		_, _ = hash.Write([]byte{0})
	}

	writeOptional(edit.MetaTitle)
	writeOptional(edit.MetaDescription)
	writeOptional(edit.Intisari)
	writeOptional(edit.Keutamaan)

	faq, err := json.Marshal(edit.FAQ)
	if err != nil {
		return "", fmt.Errorf("marshal FAQ checksum: %w", err)
	}

	_, _ = hash.Write([]byte{1})
	_, _ = hash.Write(faq)
	_, _ = hash.Write([]byte{0})

	writeOptional(edit.TafsirRange)

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func equalSurahEditorialContent(
	left,
	right *entity.QuranSurahEditorialEdit,
) bool {
	leftMetadata, rightMetadata := left.Metadata, right.Metadata
	leftComparable, rightComparable := *left, *right
	normalizeSurahEditorialComparable(&leftComparable)
	normalizeSurahEditorialComparable(&rightComparable)

	return reflect.DeepEqual(leftComparable, rightComparable) && equalJSON(leftMetadata, rightMetadata)
}

func equalAyahEditorialContent(left, right *entity.QuranAyahEditorialEdit) bool {
	leftMetadata, rightMetadata := left.Metadata, right.Metadata
	leftComparable, rightComparable := *left, *right
	normalizeAyahEditorialComparable(&leftComparable)
	normalizeAyahEditorialComparable(&rightComparable)

	return reflect.DeepEqual(leftComparable, rightComparable) && equalJSON(leftMetadata, rightMetadata)
}

func normalizeSurahEditorialComparable(edit *entity.QuranSurahEditorialEdit) {
	edit.Status = ""
	edit.Checksum = nil
	edit.Metadata = nil
	edit.UpdatedBy = nil
	edit.CreatedAt = time.Time{}
	edit.UpdatedAt = time.Time{}
	edit.PublishedAt = nil
	normalizeComparableReviewedAt(&edit.ReviewedAt)
}

func normalizeAyahEditorialComparable(edit *entity.QuranAyahEditorialEdit) {
	edit.Status = ""
	edit.Checksum = nil
	edit.Metadata = nil
	edit.UpdatedBy = nil
	edit.CreatedAt = time.Time{}
	edit.UpdatedAt = time.Time{}
	edit.PublishedAt = nil
	normalizeComparableReviewedAt(&edit.ReviewedAt)
}

func normalizeComparableReviewedAt(value **time.Time) {
	if *value != nil {
		normalized := (*value).UTC().Truncate(time.Microsecond)
		*value = &normalized
	}
}

func quranEditorialDatabaseTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	normalized := value.UTC().Truncate(time.Microsecond)

	return &normalized
}

func equalJSON(left, right entity.RawJSON) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}

	return reflect.DeepEqual(leftValue, rightValue)
}

func mapQuranEditorialWriteError(err error) error {
	mapped := mapLicensePublishError(err)
	if errors.Is(mapped, entity.ErrLicenseNotPermitted) {
		return mapped
	}

	return err
}

func applyQuranSurahMetadataTx(
	ctx context.Context,
	tx pgx.Tx,
	update QuranSurahMetadataUpdate,
) error {
	if err := lockQuranSurah(ctx, tx, update.SurahID); err != nil {
		return err
	}

	_, err := tx.Exec(ctx, `
UPDATE quran_surahs
SET slug = COALESCE($2, slug),
    chronological_order = COALESCE($3, chronological_order),
    ruku_count = COALESCE($4, ruku_count),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE surah_id = $1
  AND (
      ($2::text IS NOT NULL AND slug IS DISTINCT FROM $2)
      OR ($3::integer IS NOT NULL AND chronological_order IS DISTINCT FROM $3)
      OR ($4::integer IS NOT NULL AND ruku_count IS DISTINCT FROM $4)
	  )`, update.SurahID, update.Slug, update.ChronologicalOrder, update.RukuCount)
	if err != nil {
		return err
	}

	return nil
}

func sortAndValidateSurahImport(
	patches []QuranSurahEditorialPatch,
	metadata []QuranSurahMetadataUpdate,
) error {
	sort.Slice(patches, func(i, j int) bool {
		if patches[i].SurahID == patches[j].SurahID {
			return patches[i].Lang < patches[j].Lang
		}

		return patches[i].SurahID < patches[j].SurahID
	})

	for i := 1; i < len(patches); i++ {
		if patches[i-1].SurahID == patches[i].SurahID && patches[i-1].Lang == patches[i].Lang {
			return fmt.Errorf("duplicate Quran surah editorial resource: %w", entity.ErrInvalidQuranEditorial)
		}
	}

	sort.Slice(metadata, func(i, j int) bool { return metadata[i].SurahID < metadata[j].SurahID })

	for i := 1; i < len(metadata); i++ {
		if metadata[i-1].SurahID == metadata[i].SurahID {
			return fmt.Errorf("duplicate Quran surah metadata resource: %w", entity.ErrInvalidQuranEditorial)
		}
	}

	return nil
}

func sortAndValidateAyahImport(patches []QuranAyahEditorialPatch) error {
	sort.Slice(patches, func(i, j int) bool {
		if patches[i].SurahID != patches[j].SurahID {
			return patches[i].SurahID < patches[j].SurahID
		}

		if patches[i].AyahNumber != patches[j].AyahNumber {
			return patches[i].AyahNumber < patches[j].AyahNumber
		}

		return patches[i].Lang < patches[j].Lang
	})

	for i := 1; i < len(patches); i++ {
		if patches[i-1].SurahID == patches[i].SurahID &&
			patches[i-1].AyahNumber == patches[i].AyahNumber &&
			patches[i-1].Lang == patches[i].Lang {
			return fmt.Errorf("duplicate Quran ayah editorial resource: %w", entity.ErrInvalidQuranEditorial)
		}
	}

	return nil
}

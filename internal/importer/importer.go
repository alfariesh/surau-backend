package importer

import (
	"context"
	"crypto/sha256"
	stdsql "database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alfariesh/surau-backend/internal/readerutil"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"
)

const (
	defaultFullImportMinFreeGB uint64 = 30
	bytesPerGiB                uint64 = 1024 * 1024 * 1024
)

// Options configure the raw database importer.
type Options struct {
	SourceDir     string
	PostgresURL   string
	ReleaseKey    string
	BookIDs       []int
	Limit         int
	SkipDiskCheck bool
	MinFreeGB     uint64
	// ApproveRemovalsRun is the id of a prior staged run whose recorded
	// removals may be applied as soft tombstones. Empty = stage-only mode:
	// removals are recorded for review, nothing is ever deleted or hidden.
	ApproveRemovalsRun string
}

// ErrRemovalDrift means the source changed between staging and approval: the
// freshly computed removal set is not covered by the staged one, so applying
// tombstones would hide rows the operator never reviewed.
var ErrRemovalDrift = errors.New("staged removals drifted from current source; re-run stage mode and review again")

// Stats describe an import run.
type Stats struct {
	RunID            string
	ReleaseKey       string
	TotalBooks       int
	ImportedBooks    int
	ImportedPages    int
	ImportedHeadings int
	SkippedFiles     int
	Errors           []string
	MasterChecksum   string
	StartedAt        time.Time
	FinishedAt       time.Time
	// Staged-diff bookkeeping (E4): removals recorded for review in stage
	// mode, and tombstones actually applied in approval mode.
	StagedRemovalPages    int
	StagedRemovalHeadings int
	TombstonedPages       int
	TombstonedHeadings    int
	// ApprovedStageRun echoes Options.ApproveRemovalsRun for provenance.
	ApprovedStageRun string
}

type masterBook struct {
	ID           int
	Name         string
	IsDeleted    bool
	CategoryID   *int
	Type         *int
	SourceDate   *string
	AuthorID     *int
	Printed      *int
	MinorRelease *int
	MajorRelease *int
	Bibliography *string
	Hint         *string
	PDFLinks     string
	Metadata     string
	skipWrite    bool
}

type masterCategory struct {
	ID           int
	Name         string
	DisplayOrder *int
	IsDeleted    bool
	skipWrite    bool
}

type masterAuthor struct {
	ID                             int
	Name                           string
	Biography                      *string
	DeathText                      *string
	DeathNumber                    *int
	IsDeleted                      bool
	NameSearch                     string
	NameSearchNormalizationVersion int
	skipWrite                      bool
}

type sourcePage struct {
	ID          int
	Part        *string
	PrintedPage *string
	Number      *string
	ContentHTML string
	ContentText string
	Services    string
	IsDeleted   bool
}

// Run imports master metadata and selected book content into PostgreSQL.
func Run(ctx context.Context, opts Options) (stats Stats, err error) {
	if opts.SourceDir == "" {
		opts.SourceDir = "/Users/macmini/Downloads/database"
	}

	if opts.MinFreeGB == 0 {
		opts.MinFreeGB = defaultFullImportMinFreeGB
	}

	if opts.ReleaseKey == "" {
		opts.ReleaseKey = time.Now().UTC().Format("20060102T150405Z")
	}

	if opts.PostgresURL == "" {
		return Stats{}, errors.New("postgres URL is required")
	}

	if err := preflightDisk(opts); err != nil {
		return Stats{}, err
	}

	startedAt := time.Now().UTC()
	checksum, err := masterChecksum(opts.SourceDir)
	if err != nil {
		return Stats{}, err
	}

	pool, err := pgxpool.New(ctx, opts.PostgresURL)
	if err != nil {
		return Stats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pool.Close()

	stats = Stats{
		RunID:            uuid.New().String(),
		ReleaseKey:       opts.ReleaseKey,
		MasterChecksum:   checksum,
		StartedAt:        startedAt,
		ApprovedStageRun: opts.ApproveRemovalsRun,
	}

	if err = upsertReleaseAndRun(ctx, pool, opts, stats); err != nil {
		return Stats{}, err
	}

	status := "success"
	defer func() {
		stats.FinishedAt = time.Now().UTC()
		_ = finishRun(context.Background(), pool, stats, status)
	}()

	master, err := openSQLite(filepath.Join(opts.SourceDir, "update", "master", "book.sqlite"))
	if err != nil {
		status = "failed"
		return stats, err
	}
	defer master.Close()

	authorsDB, err := openSQLite(filepath.Join(opts.SourceDir, "update", "master", "author.sqlite"))
	if err != nil {
		status = "failed"
		return stats, err
	}
	defer authorsDB.Close()

	categoriesDB, err := openSQLite(filepath.Join(opts.SourceDir, "update", "master", "category.sqlite"))
	if err != nil {
		status = "failed"
		return stats, err
	}
	defer categoriesDB.Close()

	books, err := importMasterMetadata(ctx, pool, categoriesDB, authorsDB, master)
	if err != nil {
		status = "failed"
		return stats, err
	}

	if opts.ApproveRemovalsRun != "" {
		if err = validateApprovalRun(ctx, pool, opts.ApproveRemovalsRun); err != nil {
			status = "failed"

			return stats, err
		}
	}

	candidates := selectBookCandidates(books, opts)
	stats.TotalBooks = len(candidates)

	for _, book := range candidates {
		pages, headings, err := readBookContent(opts.SourceDir, book.ID)
		if err != nil {
			stats.SkippedFiles++
			stats.Errors = append(stats.Errors, fmt.Sprintf("book %d: %v", book.ID, err))
			continue
		}

		outcome, err := importBookContent(ctx, pool, book.ID, pages, headings, bookImportParams{
			RunID:        stats.RunID,
			ReleaseKey:   stats.ReleaseKey,
			ApproveRunID: opts.ApproveRemovalsRun,
		})
		if err != nil {
			status = "failed"
			return stats, fmt.Errorf("importing book %d: %w", book.ID, err)
		}

		stats.ImportedBooks++
		stats.ImportedPages += outcome.LivePages
		stats.ImportedHeadings += outcome.LiveHeadings

		if opts.ApproveRemovalsRun == "" {
			stats.StagedRemovalPages += len(outcome.Pages.RemovedIDs)
			stats.StagedRemovalHeadings += len(outcome.Headings.RemovedIDs)
		} else {
			stats.TombstonedPages += outcome.TombstonedPages
			stats.TombstonedHeadings += outcome.TombstonedHeadings
		}
	}

	if len(stats.Errors) > 0 {
		status = "completed_with_errors"
	}

	return stats, nil
}

func preflightDisk(opts Options) error {
	if opts.SkipDiskCheck || opts.Limit > 0 || len(opts.BookIDs) > 0 {
		return nil
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(opts.SourceDir, &stat); err != nil {
		return fmt.Errorf("checking disk space: %w", err)
	}

	if opts.MinFreeGB > ^uint64(0)/bytesPerGiB {
		return fmt.Errorf("min free GiB is too large: %d", opts.MinFreeGB)
	}

	freeBytes := stat.Bavail * uint64(stat.Bsize)
	minBytes := opts.MinFreeGB * bytesPerGiB
	if freeBytes < minBytes {
		return fmt.Errorf("free disk is %.1fGiB, need at least %dGiB for full import; use --limit/--book-ids for sample or --skip-disk-check", float64(freeBytes)/(1024*1024*1024), opts.MinFreeGB)
	}

	return nil
}

func openSQLite(path string) (*stdsql.DB, error) {
	db, err := stdsql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite %s: %w", path, err)
	}

	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}

	return db, nil
}

func readMasterCategories(ctx context.Context, db *stdsql.DB) ([]masterCategory, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, is_deleted, "order", name FROM category`)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()

	categories := make([]masterCategory, 0)
	for rows.Next() {
		var category masterCategory
		var isDeleted stdsql.NullString
		var displayOrder stdsql.NullString
		var name stdsql.NullString

		if err = rows.Scan(&category.ID, &isDeleted, &displayOrder, &name); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}

		category.Name = nullStringValue(name)
		category.DisplayOrder = nullStringToInt(displayOrder)
		category.IsDeleted = rawBool(isDeleted)
		categories = append(categories, category)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}

	return categories, nil
}

func queueMasterCategories(batch *pgx.Batch, categories []masterCategory) {
	for i := range categories {
		category := &categories[i]
		if category.skipWrite {
			continue
		}
		batch.Queue(
			`
INSERT INTO categories (id, name, display_order, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    display_order = EXCLUDED.display_order,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now()
WHERE ROW(categories.name, categories.display_order, categories.is_deleted)
      IS DISTINCT FROM
      ROW(EXCLUDED.name, EXCLUDED.display_order, EXCLUDED.is_deleted)`,
			category.ID,
			category.Name,
			category.DisplayOrder,
			category.IsDeleted,
		)
	}
}

func readMasterAuthors(ctx context.Context, db *stdsql.DB) ([]masterAuthor, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, is_deleted, name, biography, death_text, death_number FROM author`)
	if err != nil {
		return nil, fmt.Errorf("query authors: %w", err)
	}
	defer rows.Close()

	authors := make([]masterAuthor, 0)
	for rows.Next() {
		var author masterAuthor
		var isDeleted stdsql.NullString
		var name stdsql.NullString
		var biography stdsql.NullString
		var deathText stdsql.NullString
		var deathNumber stdsql.NullString

		if err = rows.Scan(&author.ID, &isDeleted, &name, &biography, &deathText, &deathNumber); err != nil {
			return nil, fmt.Errorf("scan author: %w", err)
		}

		author.Name = nullStringValue(name)
		author.Biography = nullStringPtr(biography)
		author.DeathText = nullStringPtr(deathText)
		author.DeathNumber = nullStringToInt(deathNumber)
		author.IsDeleted = rawBool(isDeleted)
		author.NameSearch = searchtext.Normalize(author.Name)
		author.NameSearchNormalizationVersion = searchtext.ProfileVersion
		authors = append(authors, author)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authors: %w", err)
	}

	return authors, nil
}

func queueMasterAuthors(batch *pgx.Batch, authors []masterAuthor) {
	for i := range authors {
		author := &authors[i]
		if author.skipWrite {
			continue
		}
		// name_search and its profile version ride every author write (insert
		// AND conflict-update) so re-imports never leave a stale or unversioned
		// normalized form behind (F1-H / B-5).
		batch.Queue(
			`
INSERT INTO authors (
    id, name, biography, death_text, death_number, is_deleted, updated_at,
    name_search, name_search_normalization_version
)
VALUES ($1, $2, $3, $4, $5, $6, now(), $7, $8)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    death_number = EXCLUDED.death_number,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now(),
    name_search = EXCLUDED.name_search,
    name_search_normalization_version = EXCLUDED.name_search_normalization_version
WHERE ROW(
    authors.name, authors.biography, authors.death_text, authors.death_number,
    authors.is_deleted, authors.name_search, authors.name_search_normalization_version
) IS DISTINCT FROM ROW(
    EXCLUDED.name, EXCLUDED.biography, EXCLUDED.death_text, EXCLUDED.death_number,
    EXCLUDED.is_deleted, EXCLUDED.name_search, EXCLUDED.name_search_normalization_version
)`,
			author.ID,
			author.Name,
			author.Biography,
			author.DeathText,
			author.DeathNumber,
			author.IsDeleted,
			author.NameSearch,
			author.NameSearchNormalizationVersion,
		)
	}
}

//nolint:funlen // Scanner assignments mirror the fixed Shamela master schema field-for-field.
func readMasterBooks(ctx context.Context, db *stdsql.DB) ([]masterBook, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, name, is_deleted, category, type, date, author, printed,
       minor_release, major_release, bibliography, hint, pdf_links, metadata
FROM book`)
	if err != nil {
		return nil, fmt.Errorf("query books: %w", err)
	}
	defer rows.Close()

	books := make([]masterBook, 0)

	for rows.Next() {
		var b masterBook
		var isDeleted stdsql.NullString
		var category stdsql.NullString
		var bookType stdsql.NullString
		var sourceDate stdsql.NullString
		var author stdsql.NullString
		var printed stdsql.NullString
		var minorRelease stdsql.NullString
		var majorRelease stdsql.NullString
		var bibliography stdsql.NullString
		var hint stdsql.NullString
		var pdfLinks stdsql.NullString
		var metadata stdsql.NullString

		if err = rows.Scan(
			&b.ID,
			&b.Name,
			&isDeleted,
			&category,
			&bookType,
			&sourceDate,
			&author,
			&printed,
			&minorRelease,
			&majorRelease,
			&bibliography,
			&hint,
			&pdfLinks,
			&metadata,
		); err != nil {
			return nil, fmt.Errorf("scan book: %w", err)
		}

		b.IsDeleted = rawBool(isDeleted)
		b.CategoryID = nullStringToInt(category)
		b.Type = nullStringToInt(bookType)
		b.SourceDate = nullStringPtr(sourceDate)
		b.AuthorID = nullStringToInt(author)
		b.Printed = nullStringToInt(printed)
		b.MinorRelease = nullStringToInt(minorRelease)
		b.MajorRelease = nullStringToInt(majorRelease)
		b.Bibliography = nullStringPtr(bibliography)
		b.Hint = nullStringPtr(hint)
		b.PDFLinks = nullableJSON(pdfLinks)
		b.Metadata = nullableJSON(metadata)
		if metadataDate := metadataDate(b.Metadata); metadataDate != nil {
			b.SourceDate = metadataDate
		}

		books = append(books, b)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate books: %w", err)
	}

	return books, nil
}

func queueMasterBooks(batch *pgx.Batch, books []masterBook) {
	for i := range books {
		b := &books[i]
		if b.skipWrite {
			continue
		}
		batch.Queue(
			`
INSERT INTO books (
    id, name, category_id, author_id, type, printed, minor_release, major_release,
    bibliography, hint, pdf_links, metadata, source_date, is_deleted, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11, '')::jsonb, nullif($12, '')::jsonb, $13, $14, now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    category_id = EXCLUDED.category_id,
    author_id = EXCLUDED.author_id,
    type = EXCLUDED.type,
    printed = EXCLUDED.printed,
    minor_release = EXCLUDED.minor_release,
    major_release = EXCLUDED.major_release,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    pdf_links = EXCLUDED.pdf_links,
    metadata = EXCLUDED.metadata,
    source_date = EXCLUDED.source_date,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now()
WHERE ROW(
    books.name, books.category_id, books.author_id, books.type, books.printed,
    books.minor_release, books.major_release, books.bibliography, books.hint,
    books.pdf_links, books.metadata, books.source_date, books.is_deleted
) IS DISTINCT FROM ROW(
    EXCLUDED.name, EXCLUDED.category_id, EXCLUDED.author_id, EXCLUDED.type, EXCLUDED.printed,
    EXCLUDED.minor_release, EXCLUDED.major_release, EXCLUDED.bibliography, EXCLUDED.hint,
    EXCLUDED.pdf_links, EXCLUDED.metadata, EXCLUDED.source_date, EXCLUDED.is_deleted
)`,
			b.ID, b.Name, b.CategoryID, b.AuthorID, b.Type, b.Printed,
			b.MinorRelease, b.MajorRelease, b.Bibliography, b.Hint,
			b.PDFLinks, b.Metadata, b.SourceDate, b.IsDeleted,
		)
	}
}

// importMasterMetadata makes the three Shamela master snapshots one atomic
// publication decision. All source rows are parsed and every B-4 preflight is
// completed before the first PostgreSQL write. Shared author/category changes
// therefore cannot leak into a grandfathered public Edition when a later book
// metadata check rejects the same run.
func importMasterMetadata(
	ctx context.Context,
	pool *pgxpool.Pool,
	categoriesDB, authorsDB, booksDB *stdsql.DB,
) ([]masterBook, error) {
	categories, err := readMasterCategories(ctx, categoriesDB)
	if err != nil {
		return nil, err
	}

	authors, err := readMasterAuthors(ctx, authorsDB)
	if err != nil {
		return nil, err
	}

	books, err := readMasterBooks(ctx, booksDB)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin master metadata import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if gateErr := ensureMasterMetadataImportsPermitted(ctx, tx, categories, authors, books); gateErr != nil {
		return nil, gateErr
	}

	batch := &pgx.Batch{}
	queueMasterCategories(batch, categories)
	queueMasterAuthors(batch, authors)
	queueMasterBooks(batch, books)

	if err = execTxBatch(ctx, tx, batch); err != nil {
		return nil, fmt.Errorf("upsert master metadata: %w", mapLicenseGateError(err))
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit master metadata import: %w", mapLicenseGateError(err))
	}

	return books, nil
}

func selectBookCandidates(books []masterBook, opts Options) []masterBook {
	allowed := make(map[int]struct{}, len(opts.BookIDs))
	for _, id := range opts.BookIDs {
		allowed[id] = struct{}{}
	}

	candidates := make([]masterBook, 0)
	for _, book := range books {
		if book.IsDeleted || book.Type == nil || *book.Type != 1 {
			continue
		}

		if len(allowed) > 0 {
			if _, ok := allowed[book.ID]; !ok {
				continue
			}
		}

		candidates = append(candidates, book)
		if opts.Limit > 0 && len(candidates) >= opts.Limit {
			break
		}
	}

	return candidates
}

func readBookContent(sourceDir string, bookID int) ([]sourcePage, []readerutil.SourceHeading, error) {
	path := readerutil.ResolveBookDBPath(sourceDir, bookID)
	if _, err := os.Stat(path); err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}

	db, err := openSQLite(path)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	pages, err := readPages(db)
	if err != nil {
		return nil, nil, err
	}

	headings, err := readHeadings(db)
	if err != nil {
		return nil, nil, err
	}

	return pages, headings, nil
}

func readPages(db *stdsql.DB) ([]sourcePage, error) {
	hasDeleted, err := tableHasColumn(db, "page", "is_deleted")
	if err != nil {
		return nil, err
	}

	query := `SELECT id, content, part, page, number, services`
	if hasDeleted {
		query += `, is_deleted`
	}
	query += ` FROM page ORDER BY id`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query pages: %w", err)
	}
	defer rows.Close()

	pages := make([]sourcePage, 0)
	for rows.Next() {
		var page sourcePage
		var content stdsql.NullString
		var part stdsql.NullString
		var printedPage stdsql.NullString
		var number stdsql.NullString
		var services stdsql.NullString
		var isDeleted stdsql.NullString

		dest := []any{&page.ID, &content, &part, &printedPage, &number, &services}
		if hasDeleted {
			dest = append(dest, &isDeleted)
		}

		if err = rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan page: %w", err)
		}

		page.Part = nullStringPtr(part)
		page.PrintedPage = nullStringPtr(printedPage)
		page.Number = nullStringPtr(number)
		page.Services = nullableJSON(services)
		page.IsDeleted = hasDeleted && rawBool(isDeleted)
		page.ContentHTML, page.ContentText = readerutil.NormalizeContent(nullStringValue(content))

		pages = append(pages, page)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pages: %w", err)
	}

	return pages, nil
}

func readHeadings(db *stdsql.DB) ([]readerutil.SourceHeading, error) {
	hasDeleted, err := tableHasColumn(db, "title", "is_deleted")
	if err != nil {
		return nil, err
	}

	query := `SELECT id, content, page, parent`
	if hasDeleted {
		query += `, is_deleted`
	}
	query += ` FROM title ORDER BY id`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query headings: %w", err)
	}
	defer rows.Close()

	headings := make([]readerutil.SourceHeading, 0)
	for rows.Next() {
		var heading readerutil.SourceHeading
		var content stdsql.NullString
		var page stdsql.NullString
		var parent stdsql.NullString
		var isDeleted stdsql.NullString

		dest := []any{&heading.ID, &content, &page, &parent}
		if hasDeleted {
			dest = append(dest, &isDeleted)
		}

		if err = rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan heading: %w", err)
		}

		heading.Content = strings.TrimSpace(nullStringValue(content))
		heading.PageID = stringToInt(nullStringValue(page))
		heading.ParentID = stringToInt(nullStringValue(parent))
		heading.IsDeleted = hasDeleted && rawBool(isDeleted)

		headings = append(headings, heading)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate headings: %w", err)
	}

	return headings, nil
}

type bookImportParams struct {
	RunID        string
	ReleaseKey   string
	ApproveRunID string
}

type bookImportOutcome struct {
	LivePages          int
	LiveHeadings       int
	Pages              pageDiff
	Headings           headingDiff
	TombstonedPages    int
	TombstonedHeadings int
}

// importBookContent applies a source snapshot as a staged diff (E4 / K-0 D1):
// only added/changed/revived rows are written, and rows missing from the
// source are NEVER deleted — they are recorded for review (stage mode) or
// soft-tombstoned (approval mode, after a drift check against the staged
// diff). Editorial and user data therefore survive re-imports by construction.
func importBookContent(
	ctx context.Context,
	pool *pgxpool.Pool,
	bookID int,
	pages []sourcePage,
	headings []readerutil.SourceHeading,
	params bookImportParams,
) (bookImportOutcome, error) {
	if len(pages) == 0 {
		return bookImportOutcome{}, errors.New("book has no pages")
	}

	pageIDs := make(map[int]struct{}, len(pages))
	incomingPages := make([]sourcePage, 0, len(pages))
	lastPageID := 0

	for _, page := range pages {
		if page.IsDeleted {
			continue
		}

		pageIDs[page.ID] = struct{}{}
		incomingPages = append(incomingPages, page)

		if page.ID > lastPageID {
			lastPageID = page.ID
		}
	}

	filteredHeadings := make([]readerutil.SourceHeading, 0, len(headings))
	for _, heading := range headings {
		if heading.IsDeleted || heading.PageID == 0 {
			continue
		}

		if _, ok := pageIDs[heading.PageID]; !ok {
			continue
		}

		filteredHeadings = append(filteredHeadings, heading)
	}

	decorated := readerutil.DecorateHeadings(filteredHeadings)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return bookImportOutcome{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	currentPages, err := loadCurrentPages(ctx, tx, bookID)
	if err != nil {
		return bookImportOutcome{}, err
	}

	currentHeadings, err := loadCurrentHeadings(ctx, tx, bookID)
	if err != nil {
		return bookImportOutcome{}, err
	}

	outcome := bookImportOutcome{
		LivePages:    len(incomingPages),
		LiveHeadings: len(decorated),
		Pages:        diffPages(currentPages, incomingPages),
		Headings:     diffHeadings(currentHeadings, decorated),
	}

	changed := !outcome.Pages.empty() || !outcome.Headings.empty()
	if gateErr := ensureMaterialBookImportPermitted(ctx, tx, bookID, changed); gateErr != nil {
		return bookImportOutcome{}, gateErr
	}

	pageBatch := &pgx.Batch{}
	for _, page := range outcome.Pages.Upserts {
		pageBatch.Queue(
			`
INSERT INTO book_pages (book_id, page_id, part, printed_page, number, content_html, content_text, services, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, false, now())
ON CONFLICT (book_id, page_id) DO UPDATE SET
    part = EXCLUDED.part,
    printed_page = EXCLUDED.printed_page,
    number = EXCLUDED.number,
    content_html = EXCLUDED.content_html,
    content_text = EXCLUDED.content_text,
    services = EXCLUDED.services,
    is_deleted = false,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
			bookID,
			page.ID,
			page.Part,
			page.PrintedPage,
			page.Number,
			page.ContentHTML,
			page.ContentText,
			page.Services,
		)
	}

	if err = execTxBatch(ctx, tx, pageBatch); err != nil {
		return bookImportOutcome{}, fmt.Errorf("upsert pages: %w", mapLicenseGateError(err))
	}

	headingBatch := &pgx.Batch{}
	for _, heading := range outcome.Headings.Upserts {
		headingBatch.Queue(
			`
INSERT INTO book_headings (book_id, heading_id, parent_id, page_id, depth, ordinal, content, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, false, now())
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    parent_id = EXCLUDED.parent_id,
    page_id = EXCLUDED.page_id,
    depth = EXCLUDED.depth,
    ordinal = EXCLUDED.ordinal,
    content = EXCLUDED.content,
    is_deleted = false,
    deleted_at = NULL,
    delete_reason = NULL,
    updated_at = now()`,
			bookID,
			heading.ID,
			intPtrOrNil(heading.ParentID),
			heading.PageID,
			heading.Depth,
			heading.Ordinal,
			heading.Content,
		)
	}

	if err = execTxBatch(ctx, tx, headingBatch); err != nil {
		return bookImportOutcome{}, fmt.Errorf("upsert headings: %w", mapLicenseGateError(err))
	}

	hasRemovals := len(outcome.Pages.RemovedIDs) > 0 || len(outcome.Headings.RemovedIDs) > 0

	switch {
	case hasRemovals && params.ApproveRunID == "":
		if err := stageRemovals(ctx, tx, params.RunID, bookID, outcome.Pages.RemovedIDs, outcome.Headings.RemovedIDs); err != nil {
			return bookImportOutcome{}, err
		}
	case hasRemovals:
		outcome.TombstonedPages, outcome.TombstonedHeadings, err = applyApprovedRemovals(
			ctx, tx, bookID, params, outcome.Pages.RemovedIDs, outcome.Headings.RemovedIDs,
		)
		if err != nil {
			return bookImportOutcome{}, err
		}
	}

	if changed {
		ranges := readerutil.BuildHeadingRanges(bookID, lastPageID, decorated)

		rangeBatch := &pgx.Batch{}
		for _, headingRange := range ranges {
			rangeBatch.Queue(
				`
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id, start_anchor, end_anchor, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    start_page_id = EXCLUDED.start_page_id,
    end_page_id = EXCLUDED.end_page_id,
    start_anchor = EXCLUDED.start_anchor,
    end_anchor = EXCLUDED.end_anchor,
    updated_at = now()`,
				bookID,
				headingRange.HeadingID,
				headingRange.StartPageID,
				headingRange.EndPageID,
				emptyStringNil(headingRange.StartAnchor),
				emptyStringNil(headingRange.EndAnchor),
			)
		}

		if err = execTxBatch(ctx, tx, rangeBatch); err != nil {
			return bookImportOutcome{}, fmt.Errorf("upsert heading ranges: %w", err)
		}

		if _, err = tx.Exec(ctx, `UPDATE books SET has_content = true, updated_at = now() WHERE id = $1`, bookID); err != nil {
			return bookImportOutcome{}, fmt.Errorf("mark book content: %w", mapLicenseGateError(err))
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return bookImportOutcome{}, fmt.Errorf("commit tx: %w", mapLicenseGateError(err))
	}

	return outcome, nil
}

func loadCurrentPages(ctx context.Context, tx pgx.Tx, bookID int) (map[int]pageRecord, error) {
	rows, err := tx.Query(ctx, `
SELECT page_id, coalesce(part, ''), coalesce(printed_page, ''), coalesce(number, ''),
       content_html, content_text, coalesce(services::text, ''), is_deleted
FROM book_pages WHERE book_id = $1`, bookID)
	if err != nil {
		return nil, fmt.Errorf("load current pages: %w", err)
	}
	defer rows.Close()

	current := make(map[int]pageRecord)

	for rows.Next() {
		var (
			id     int
			record pageRecord
		)

		if err = rows.Scan(&id, &record.Part, &record.PrintedPage, &record.Number,
			&record.ContentHTML, &record.ContentText, &record.ServicesJSON, &record.IsDeleted); err != nil {
			return nil, fmt.Errorf("scan current page: %w", err)
		}

		current[id] = record
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current pages: %w", err)
	}

	return current, nil
}

func loadCurrentHeadings(ctx context.Context, tx pgx.Tx, bookID int) (map[int]headingRecord, error) {
	rows, err := tx.Query(ctx, `
SELECT heading_id, coalesce(parent_id, 0), page_id, depth, ordinal, content, is_deleted
FROM book_headings WHERE book_id = $1`, bookID)
	if err != nil {
		return nil, fmt.Errorf("load current headings: %w", err)
	}
	defer rows.Close()

	current := make(map[int]headingRecord)

	for rows.Next() {
		var (
			id     int
			record headingRecord
		)

		if err = rows.Scan(&id, &record.ParentID, &record.PageID, &record.Depth,
			&record.Ordinal, &record.Content, &record.IsDeleted); err != nil {
			return nil, fmt.Errorf("scan current heading: %w", err)
		}

		current[id] = record
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current headings: %w", err)
	}

	return current, nil
}

// stageRemovals records the rows that would disappear so the operator can
// review them; the rows themselves stay live and untouched.
func stageRemovals(ctx context.Context, tx pgx.Tx, runID string, bookID int, pageIDs, headingIDs []int) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO book_import_removal_stages (run_id, book_id, page_ids, heading_ids)
VALUES ($1, $2, $3, $4)
ON CONFLICT (run_id, book_id) DO UPDATE SET
    page_ids = EXCLUDED.page_ids,
    heading_ids = EXCLUDED.heading_ids`,
		runID, bookID, pageIDs, headingIDs); err != nil {
		return fmt.Errorf("stage removals: %w", err)
	}

	return nil
}

// applyApprovedRemovals turns the freshly computed removals into soft
// tombstones, but only when they are covered by the operator-reviewed staged
// diff — anything new since staging is drift and aborts the run.
func applyApprovedRemovals(
	ctx context.Context,
	tx pgx.Tx,
	bookID int,
	params bookImportParams,
	removedPageIDs, removedHeadingIDs []int,
) (tombstonedPages, tombstonedHeadings int, err error) {
	var stagedPages, stagedHeadings []int

	err = tx.QueryRow(ctx, `
SELECT page_ids, heading_ids FROM book_import_removal_stages
WHERE run_id = $1 AND book_id = $2`, params.ApproveRunID, bookID).Scan(&stagedPages, &stagedHeadings)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, fmt.Errorf("book %d has removals but no staged diff in run %s: %w", bookID, params.ApproveRunID, ErrRemovalDrift)
	}

	if err != nil {
		return 0, 0, fmt.Errorf("load staged removals: %w", err)
	}

	if !subsetOf(removedPageIDs, stagedPages) || !subsetOf(removedHeadingIDs, stagedHeadings) {
		return 0, 0, fmt.Errorf("book %d: %w", bookID, ErrRemovalDrift)
	}

	reason := "import:" + params.ReleaseKey

	pagesTag, err := tx.Exec(ctx, `
UPDATE book_pages SET is_deleted = true, deleted_at = now(), delete_reason = $3, updated_at = now()
WHERE book_id = $1 AND page_id = ANY($2) AND is_deleted = false`,
		bookID, removedPageIDs, reason)
	if err != nil {
		return 0, 0, fmt.Errorf("tombstone pages: %w", err)
	}

	headingsTag, err := tx.Exec(ctx, `
UPDATE book_headings SET is_deleted = true, deleted_at = now(), delete_reason = $3, updated_at = now()
WHERE book_id = $1 AND heading_id = ANY($2) AND is_deleted = false`,
		bookID, removedHeadingIDs, reason)
	if err != nil {
		return 0, 0, fmt.Errorf("tombstone headings: %w", err)
	}

	return int(pagesTag.RowsAffected()), int(headingsTag.RowsAffected()), nil
}

func validateApprovalRun(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	if _, err := uuid.Parse(runID); err != nil {
		return fmt.Errorf("invalid -approve-removals run id %q: %w", runID, err)
	}

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM import_runs WHERE id = $1)`, runID).Scan(&exists); err != nil {
		return fmt.Errorf("look up approval run: %w", err)
	}

	if !exists {
		return fmt.Errorf("approval run %s not found", runID)
	}

	return nil
}

func upsertReleaseAndRun(ctx context.Context, pool *pgxpool.Pool, opts Options, stats Stats) error {
	_, err := pool.Exec(
		ctx, `
INSERT INTO source_releases (release_key, source_dir, master_checksum, created_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (release_key) DO UPDATE SET
    source_dir = EXCLUDED.source_dir,
    master_checksum = EXCLUDED.master_checksum`,
		stats.ReleaseKey,
		opts.SourceDir,
		stats.MasterChecksum,
	)
	if err != nil {
		return fmt.Errorf("upsert source release: %w", err)
	}

	mode := "full"
	if opts.Limit > 0 || len(opts.BookIDs) > 0 {
		mode = "sample"
	}

	_, err = pool.Exec(
		ctx, `
INSERT INTO import_runs (id, release_key, mode, source_dir, status, started_at, master_checksum)
VALUES ($1, $2, $3, $4, 'running', $5, $6)`,
		stats.RunID,
		stats.ReleaseKey,
		mode,
		opts.SourceDir,
		stats.StartedAt,
		stats.MasterChecksum,
	)
	if err != nil {
		return fmt.Errorf("insert import run: %w", err)
	}

	return nil
}

func finishRun(ctx context.Context, pool *pgxpool.Pool, stats Stats, status string) error {
	errs, _ := json.Marshal(stats.Errors)

	_, err := pool.Exec(
		ctx, `
UPDATE import_runs SET
    status = $2,
    finished_at = $3,
    total_books = $4,
    imported_books = $5,
    imported_pages = $6,
    imported_headings = $7,
    skipped_files = $8,
    errors = nullif($9, 'null')::jsonb,
    staged_removal_pages = $10,
    staged_removal_headings = $11,
    tombstoned_pages = $12,
    tombstoned_headings = $13,
    approved_stage_run = nullif($14, '')::uuid
WHERE id = $1`,
		stats.RunID,
		status,
		stats.FinishedAt,
		stats.TotalBooks,
		stats.ImportedBooks,
		stats.ImportedPages,
		stats.ImportedHeadings,
		stats.SkippedFiles,
		string(errs),
		stats.StagedRemovalPages,
		stats.StagedRemovalHeadings,
		stats.TombstonedPages,
		stats.TombstonedHeadings,
		stats.ApprovedStageRun,
	)
	if err != nil {
		return fmt.Errorf("finish import run: %w", err)
	}

	return nil
}

func tableHasColumn(db *stdsql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType stdsql.NullString
		var notNull int
		var defaultValue stdsql.NullString
		var pk int

		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan pragma table_info(%s): %w", table, err)
		}

		if strings.EqualFold(name, column) {
			return true, nil
		}
	}

	if err = rows.Err(); err != nil {
		return false, fmt.Errorf("iterate pragma table_info(%s): %w", table, err)
	}

	return false, nil
}

func masterChecksum(sourceDir string) (string, error) {
	hash := sha256.New()
	for _, name := range []string{"author.sqlite", "book.sqlite", "category.sqlite"} {
		path := filepath.Join(sourceDir, "update", "master", name)
		file, err := os.Open(path) // #nosec G304 -- import CLI reads known DB filenames under an operator-supplied source directory.
		if err != nil {
			return "", fmt.Errorf("open %s: %w", path, err)
		}

		if _, err = io.Copy(hash, file); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("hash %s: %w", path, err)
		}

		if err = file.Close(); err != nil {
			return "", fmt.Errorf("close %s: %w", path, err)
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func execBatch(ctx context.Context, pool *pgxpool.Pool, batch *pgx.Batch) error {
	if batch.Len() == 0 {
		return nil
	}

	results := pool.SendBatch(ctx, batch)
	defer results.Close()

	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}

	return nil
}

func execTxBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch) error {
	if batch.Len() == 0 {
		return nil
	}

	results := tx.SendBatch(ctx, batch)
	defer results.Close()

	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}

	return nil
}

func nullStringValue(value stdsql.NullString) string {
	if !value.Valid {
		return ""
	}

	return strings.TrimSpace(value.String)
}

func nullStringPtr(value stdsql.NullString) *string {
	if !value.Valid {
		return nil
	}

	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func nullStringToInt(value stdsql.NullString) *int {
	if !value.Valid {
		return nil
	}

	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" {
		return nil
	}

	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return nil
	}

	return &parsed
}

func stringToInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}

	return parsed
}

func rawBool(value stdsql.NullString) bool {
	if !value.Valid {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(value.String)) {
	case "1", "true", "t", "yes", "y":
		return true
	default:
		return false
	}
}

func nullableJSON(value stdsql.NullString) string {
	if !value.Valid {
		return ""
	}

	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return ""
	}

	return trimmed
}

func metadataDate(metadata string) *string {
	if metadata == "" {
		return nil
	}

	var payload struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal([]byte(metadata), &payload); err != nil {
		return nil
	}

	payload.Date = strings.TrimSpace(payload.Date)
	if payload.Date == "" {
		return nil
	}

	return &payload.Date
}

func intPtrOrNil(value int) *int {
	if value == 0 {
		return nil
	}

	return &value
}

func emptyStringNil(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

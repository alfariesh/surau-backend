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

	"github.com/evrone/go-clean-template/internal/readerutil"
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
}

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
		RunID:          uuid.New().String(),
		ReleaseKey:     opts.ReleaseKey,
		MasterChecksum: checksum,
		StartedAt:      startedAt,
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

	if err = importCategories(ctx, pool, categoriesDB); err != nil {
		status = "failed"
		return stats, err
	}

	if err = importAuthors(ctx, pool, authorsDB); err != nil {
		status = "failed"
		return stats, err
	}

	books, err := importBooksMetadata(ctx, pool, master)
	if err != nil {
		status = "failed"
		return stats, err
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

		importedPages, importedHeadings, err := importBookContent(ctx, pool, book.ID, pages, headings)
		if err != nil {
			status = "failed"
			return stats, fmt.Errorf("importing book %d: %w", book.ID, err)
		}

		stats.ImportedBooks++
		stats.ImportedPages += importedPages
		stats.ImportedHeadings += importedHeadings
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

func importCategories(ctx context.Context, pool *pgxpool.Pool, db *stdsql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id, is_deleted, "order", name FROM category`)
	if err != nil {
		return fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()

	batch := &pgx.Batch{}
	for rows.Next() {
		var id int
		var isDeleted stdsql.NullString
		var displayOrder stdsql.NullString
		var name stdsql.NullString

		if err = rows.Scan(&id, &isDeleted, &displayOrder, &name); err != nil {
			return fmt.Errorf("scan category: %w", err)
		}

		batch.Queue(
			`
INSERT INTO categories (id, name, display_order, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    display_order = EXCLUDED.display_order,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now()`,
			id,
			nullStringValue(name),
			nullStringToInt(displayOrder),
			rawBool(isDeleted),
		)
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate categories: %w", err)
	}

	return execBatch(ctx, pool, batch)
}

func importAuthors(ctx context.Context, pool *pgxpool.Pool, db *stdsql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id, is_deleted, name, biography, death_text, death_number FROM author`)
	if err != nil {
		return fmt.Errorf("query authors: %w", err)
	}
	defer rows.Close()

	batch := &pgx.Batch{}
	for rows.Next() {
		var id int
		var isDeleted stdsql.NullString
		var name stdsql.NullString
		var biography stdsql.NullString
		var deathText stdsql.NullString
		var deathNumber stdsql.NullString

		if err = rows.Scan(&id, &isDeleted, &name, &biography, &deathText, &deathNumber); err != nil {
			return fmt.Errorf("scan author: %w", err)
		}

		batch.Queue(
			`
INSERT INTO authors (id, name, biography, death_text, death_number, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    death_number = EXCLUDED.death_number,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now()`,
			id,
			nullStringValue(name),
			nullStringPtr(biography),
			nullStringPtr(deathText),
			nullStringToInt(deathNumber),
			rawBool(isDeleted),
		)
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate authors: %w", err)
	}

	return execBatch(ctx, pool, batch)
}

func importBooksMetadata(ctx context.Context, pool *pgxpool.Pool, db *stdsql.DB) ([]masterBook, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, name, is_deleted, category, type, date, author, printed,
       minor_release, major_release, bibliography, hint, pdf_links, metadata
FROM book`)
	if err != nil {
		return nil, fmt.Errorf("query books: %w", err)
	}
	defer rows.Close()

	books := make([]masterBook, 0)
	batch := &pgx.Batch{}

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
    updated_at = now()`,
			b.ID,
			b.Name,
			b.CategoryID,
			b.AuthorID,
			b.Type,
			b.Printed,
			b.MinorRelease,
			b.MajorRelease,
			b.Bibliography,
			b.Hint,
			b.PDFLinks,
			b.Metadata,
			b.SourceDate,
			b.IsDeleted,
		)

		books = append(books, b)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate books: %w", err)
	}

	if err = execBatch(ctx, pool, batch); err != nil {
		return nil, err
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

func importBookContent(
	ctx context.Context,
	pool *pgxpool.Pool,
	bookID int,
	pages []sourcePage,
	headings []readerutil.SourceHeading,
) (int, int, error) {
	if len(pages) == 0 {
		return 0, 0, errors.New("book has no pages")
	}

	pageIDs := make(map[int]struct{}, len(pages))
	pageIDList := make([]int, 0, len(pages))
	lastPageID := 0
	for _, page := range pages {
		if page.IsDeleted {
			continue
		}

		pageIDs[page.ID] = struct{}{}
		pageIDList = append(pageIDList, page.ID)
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
	ranges := readerutil.BuildHeadingRanges(bookID, lastPageID, decorated)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	pageBatch := &pgx.Batch{}
	for _, page := range pages {
		pageBatch.Queue(
			`
INSERT INTO book_pages (book_id, page_id, part, printed_page, number, content_html, content_text, services, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, $9, now())
ON CONFLICT (book_id, page_id) DO UPDATE SET
    part = EXCLUDED.part,
    printed_page = EXCLUDED.printed_page,
    number = EXCLUDED.number,
    content_html = EXCLUDED.content_html,
    content_text = EXCLUDED.content_text,
    services = EXCLUDED.services,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now()`,
			bookID,
			page.ID,
			page.Part,
			page.PrintedPage,
			page.Number,
			page.ContentHTML,
			page.ContentText,
			page.Services,
			page.IsDeleted,
		)
	}

	if err = execTxBatch(ctx, tx, pageBatch); err != nil {
		return 0, 0, fmt.Errorf("upsert pages: %w", err)
	}

	if len(pageIDList) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM book_pages WHERE book_id = $1 AND NOT (page_id = ANY($2))`, bookID, pageIDList); err != nil {
			return 0, 0, fmt.Errorf("delete stale pages: %w", err)
		}
	}

	headingIDs := make([]int, 0, len(decorated))
	headingBatch := &pgx.Batch{}
	for _, heading := range decorated {
		parentID := intPtrOrNil(heading.ParentID)
		headingIDs = append(headingIDs, heading.ID)
		headingBatch.Queue(
			`
INSERT INTO book_headings (book_id, heading_id, parent_id, page_id, depth, ordinal, content, is_deleted, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    parent_id = EXCLUDED.parent_id,
    page_id = EXCLUDED.page_id,
    depth = EXCLUDED.depth,
    ordinal = EXCLUDED.ordinal,
    content = EXCLUDED.content,
    is_deleted = EXCLUDED.is_deleted,
    updated_at = now()`,
			bookID,
			heading.ID,
			parentID,
			heading.PageID,
			heading.Depth,
			heading.Ordinal,
			heading.Content,
			heading.IsDeleted,
		)
	}

	if err = execTxBatch(ctx, tx, headingBatch); err != nil {
		return 0, 0, fmt.Errorf("upsert headings: %w", err)
	}

	if len(headingIDs) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM book_headings WHERE book_id = $1 AND NOT (heading_id = ANY($2))`, bookID, headingIDs); err != nil {
			return 0, 0, fmt.Errorf("delete stale headings: %w", err)
		}
	}

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
		return 0, 0, fmt.Errorf("upsert heading ranges: %w", err)
	}

	if _, err = tx.Exec(ctx, `UPDATE books SET has_content = true, updated_at = now() WHERE id = $1`, bookID); err != nil {
		return 0, 0, fmt.Errorf("mark book content: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit tx: %w", err)
	}

	return len(pageIDList), len(decorated), nil
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
    errors = nullif($9, 'null')::jsonb
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

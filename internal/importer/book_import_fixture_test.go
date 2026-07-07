package importer

import (
	stdsql "database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fixturePage / fixtureHeading describe rows of the synthetic Shamela-style
// SQLite source a book-import test builds in t.TempDir().
type fixturePage struct {
	ID        int
	Content   string
	Part      string
	Page      string
	Number    string
	IsDeleted bool
}

type fixtureHeading struct {
	ID        int
	Content   string
	PageID    int
	ParentID  int
	IsDeleted bool
}

type fixtureBook struct {
	ID       int
	Name     string
	Pages    []fixturePage
	Headings []fixtureHeading
}

// writeBookSource builds a minimal raw source tree the importer can read:
// update/master/{book,author,category}.sqlite plus book/<id%1000>/<id>.db.
func writeBookSource(t *testing.T, dir string, books ...fixtureBook) {
	t.Helper()

	masterDir := filepath.Join(dir, "update", "master")
	if err := os.MkdirAll(masterDir, 0o750); err != nil {
		t.Fatalf("mkdir master: %v", err)
	}

	master := openFixtureSQLite(t, filepath.Join(masterDir, "book.sqlite"))
	mustExec(t, master, `CREATE TABLE book (
		id INTEGER PRIMARY KEY, name TEXT, is_deleted TEXT, category TEXT, type TEXT,
		date TEXT, author TEXT, printed TEXT, minor_release TEXT, major_release TEXT,
		bibliography TEXT, hint TEXT, pdf_links TEXT, metadata TEXT)`)
	for _, b := range books {
		mustExec(t, master,
			`INSERT INTO book (id, name, is_deleted, category, type, author) VALUES (?, ?, '0', '1', '1', '1')`,
			b.ID, b.Name)
	}
	closeFixtureSQLite(t, master)

	authors := openFixtureSQLite(t, filepath.Join(masterDir, "author.sqlite"))
	mustExec(t, authors, `CREATE TABLE author (
		id INTEGER PRIMARY KEY, is_deleted TEXT, name TEXT, biography TEXT, death_text TEXT, death_number TEXT)`)
	mustExec(t, authors, `INSERT INTO author (id, is_deleted, name) VALUES (1, '0', 'Test Author')`)
	closeFixtureSQLite(t, authors)

	categories := openFixtureSQLite(t, filepath.Join(masterDir, "category.sqlite"))
	mustExec(t, categories, `CREATE TABLE category (id INTEGER PRIMARY KEY, is_deleted TEXT, "order" TEXT, name TEXT)`)
	mustExec(t, categories, `INSERT INTO category (id, is_deleted, "order", name) VALUES (1, '0', '1', 'Test Category')`)
	closeFixtureSQLite(t, categories)

	for _, b := range books {
		bookDir := filepath.Join(dir, "book", fmt.Sprintf("%03d", b.ID%1000))
		if err := os.MkdirAll(bookDir, 0o750); err != nil {
			t.Fatalf("mkdir book dir: %v", err)
		}

		content := openFixtureSQLite(t, filepath.Join(bookDir, fmt.Sprintf("%d.db", b.ID)))
		mustExec(t, content, `CREATE TABLE page (
			id INTEGER PRIMARY KEY, content TEXT, part TEXT, page TEXT, number TEXT, services TEXT, is_deleted TEXT)`)
		mustExec(t, content, `CREATE TABLE title (
			id INTEGER PRIMARY KEY, content TEXT, page TEXT, parent TEXT, is_deleted TEXT)`)

		for _, p := range b.Pages {
			mustExec(t, content,
				`INSERT INTO page (id, content, part, page, number, services, is_deleted) VALUES (?, ?, ?, ?, ?, '', ?)`,
				p.ID, p.Content, p.Part, p.Page, p.Number, boolFixture(p.IsDeleted))
		}
		for _, h := range b.Headings {
			mustExec(t, content,
				`INSERT INTO title (id, content, page, parent, is_deleted) VALUES (?, ?, ?, ?, ?)`,
				h.ID, h.Content, fmt.Sprintf("%d", h.PageID), fmt.Sprintf("%d", h.ParentID), boolFixture(h.IsDeleted))
		}
		closeFixtureSQLite(t, content)
	}
}

func openFixtureSQLite(t *testing.T, path string) *stdsql.DB {
	t.Helper()

	db, err := stdsql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture sqlite %s: %v", path, err)
	}

	return db
}

func closeFixtureSQLite(t *testing.T, db *stdsql.DB) {
	t.Helper()

	if err := db.Close(); err != nil {
		t.Fatalf("close fixture sqlite: %v", err)
	}
}

func mustExec(t *testing.T, db *stdsql.DB, query string, args ...any) {
	t.Helper()

	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("fixture exec %q: %v", query, err)
	}
}

func boolFixture(deleted bool) string {
	if deleted {
		return "1"
	}

	return "0"
}

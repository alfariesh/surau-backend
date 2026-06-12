package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
)

const (
	productionFixtureCategoryID = 990101
	productionFixtureAuthorID   = 990101
	productionFixtureBookID     = 990101
	productionFixtureHeadingOne = 990101
	productionFixtureHeadingTwo = 990102
)

func TestEditorialProductionPublishFlow(t *testing.T) {
	seedProductionKitabFixture(t)

	adminToken := adminJWT(t)
	project := createProductionProject(t, adminToken, productionFixtureBookID, "en")

	candidates := getProductionCandidates(t, adminToken, "en", false)
	if candidates.Total != 1 || len(candidates.Candidates) != 1 {
		t.Fatalf("production candidates = %+v", candidates)
	}
	candidate := candidates.Candidates[0]
	if candidate.BookID != productionFixtureBookID || candidate.ExistingProjectID == nil || *candidate.ExistingProjectID != project.ID {
		t.Fatalf("candidate should expose existing project: %+v", candidate)
	}
	if candidate.HeadingCount != 2 || candidate.PageCount != 2 {
		t.Fatalf("candidate counts = %+v", candidate)
	}
	unstarted := getProductionCandidates(t, adminToken, "en", true)
	if unstarted.Total != 0 || len(unstarted.Candidates) != 0 {
		t.Fatalf("unstarted candidates should hide active project: %+v", unstarted)
	}

	workspace := getProductionWorkspace(t, adminToken, project.ID)
	if workspace.Book.ID != productionFixtureBookID || len(workspace.Headings) != 2 {
		t.Fatalf("initial workspace = %+v", workspace)
	}
	if workspace.Metadata.Exists {
		t.Fatalf("metadata should start missing: %+v", workspace.Metadata)
	}
	if workspace.Headings[0].Translation.Exists {
		t.Fatalf("translation should start missing: %+v", workspace.Headings[0].Translation)
	}

	initialCheck := getProductionPublishCheck(t, adminToken, project.ID)
	if initialCheck.CanPublish || initialCheck.MissingCount == 0 || len(initialCheck.BlockingErrors) == 0 {
		t.Fatalf("initial publish check should block publish: %+v", initialCheck)
	}

	putProductionDraft(t, adminToken, fmt.Sprintf("/v1/editorial/production-projects/%s/metadata-draft", project.ID),
		`{"display_title":"Old Production Book EN","bibliography":"Old production bibliography"}`)
	putProductionDraft(t, adminToken, fmt.Sprintf("/v1/editorial/production-projects/%s/metadata-draft", project.ID),
		`{"display_title":"Production Book EN","bibliography":"Production bibliography"}`)
	revisions := getProductionDraftRevisions(t, adminToken, project.ID, "book_metadata", nil)
	if revisions.Total != 2 || len(revisions.Revisions) != 2 {
		t.Fatalf("metadata revisions after two saves = %+v", revisions)
	}
	if revisions.Revisions[0].Version != 2 || revisions.Revisions[1].Version != 1 {
		t.Fatalf("metadata revisions should be ordered newest-first: %+v", revisions.Revisions)
	}
	restored := restoreProductionDraftRevision(t, adminToken, project.ID, revisions.Revisions[1].ID)
	if restored.Version != 3 {
		t.Fatalf("restored revision should create version 3: %+v", restored)
	}
	metadataDraft := getProductionMetadataDraft(t, adminToken, project.ID)
	if metadataDraft.DisplayTitle != "Old Production Book EN" {
		t.Fatalf("metadata restore did not restore old title: %+v", metadataDraft)
	}

	saveProductionDrafts(t, adminToken, project.ID)
	approveProductionDrafts(t, adminToken, project.ID)

	completeness := getProductionCompleteness(t, adminToken, project.ID)
	if !completeness.Ready || completeness.RequiredCount != 9 || completeness.MissingCount != 0 {
		t.Fatalf("completeness = %+v", completeness)
	}
	readyCheck := getProductionPublishCheck(t, adminToken, project.ID)
	if !readyCheck.CanPublish || readyCheck.MissingCount != 0 || len(readyCheck.BlockingErrors) != 0 {
		t.Fatalf("ready publish check should allow publish: %+v", readyCheck)
	}

	workspace = getProductionWorkspace(t, adminToken, project.ID)
	if !workspace.Metadata.Complete || workspace.Metadata.ReviewStatus == nil || *workspace.Metadata.ReviewStatus != "approved" {
		t.Fatalf("workspace metadata status = %+v", workspace.Metadata)
	}
	for _, heading := range workspace.Headings {
		if !heading.Translation.Complete || !heading.Summary.Complete || !heading.Audio.Complete {
			t.Fatalf("heading status should be complete: %+v", heading)
		}
	}

	published := publishProductionProject(t, adminToken, project.ID)
	if published.PublicationStatus != "published" || published.WorkflowStatus != "published" {
		t.Fatalf("published project = %+v", published)
	}

	workspace = getProductionWorkspace(t, adminToken, project.ID)
	if !workspace.Metadata.FinalExists {
		t.Fatalf("workspace metadata final flag = %+v", workspace.Metadata)
	}
	for _, heading := range workspace.Headings {
		if !heading.Translation.FinalExists || !heading.Summary.FinalExists || !heading.Audio.FinalExists {
			t.Fatalf("heading final flags should be true: %+v", heading)
		}
	}

	enRead := getProductionTOCRead(t, productionFixtureBookID, productionFixtureHeadingOne, "en")
	if enRead.Translation == nil || enRead.Translation.Content != "English production content 1" {
		t.Fatalf("published en read translation = %+v", enRead.Translation)
	}

	unpublished := unpublishProductionProject(t, adminToken, project.ID)
	if unpublished.PublicationStatus != "hidden" {
		t.Fatalf("unpublished project = %+v", unpublished)
	}

	hiddenRead := getProductionTOCRead(t, productionFixtureBookID, productionFixtureHeadingOne, "en")
	if hiddenRead.Translation != nil || !hiddenRead.TranslationMissing {
		t.Fatalf("unpublished en read should hide translation: %+v", hiddenRead)
	}

	activity := getProductionActivity(t, adminToken, project.ID)
	if activity.Total != 24 || len(activity.Events) != 24 {
		t.Fatalf("activity should contain full production timeline: %+v", activity)
	}
	if activity.Events[0].EventType != "production_project.unpublish" {
		t.Fatalf("latest activity should be unpublish: %+v", activity.Events[0])
	}
	assertProductionActivityContains(t, activity, "production_project.create", "")
	assertProductionActivityContains(t, activity, "production_asset.draft_save", "section_translation")
	assertProductionActivityContains(t, activity, "production_asset.draft_restore", "book_metadata")
	assertProductionActivityContains(t, activity, "production_asset.review", "heading_summary")
	assertProductionActivityContains(t, activity, "production_project.publish", "")
}

func seedProductionKitabFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin production fixture tx: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `DELETE FROM book_production_projects WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_metadata_translations WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM section_translations WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM section_audio WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_heading_summaries WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM author_translations WHERE author_id = $1`, productionFixtureAuthorID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM category_translations WHERE category_id = $1`, productionFixtureCategoryID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_heading_ranges WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_headings WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_pages WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_publications WHERE book_id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id = $1`, productionFixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM authors WHERE id = $1`, productionFixtureAuthorID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM categories WHERE id = $1`, productionFixtureCategoryID)

	execFixtureSQL(t, ctx, tx, `
INSERT INTO categories (id, name, display_order)
VALUES ($1, 'تصنيف الإنتاج', 1)`, productionFixtureCategoryID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO authors (id, name, biography, death_text, death_number)
VALUES ($1, 'مؤلف الإنتاج', 'سيرة الإنتاج', '1445 هـ', 1445)`, productionFixtureAuthorID)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO books (
    id, name, category_id, author_id, type, printed, minor_release, major_release,
    bibliography, hint, pdf_links, metadata, source_date, has_content
)
VALUES ($1, 'كتاب الإنتاج', $2, $3, 1, 1, 0, 1, 'مصدر الإنتاج', 'تلميح الإنتاج', '{}'::jsonb, '{}'::jsonb, '14450101', true)`,
		productionFixtureBookID,
		productionFixtureCategoryID,
		productionFixtureAuthorID,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_publications (book_id, status, featured, sort_order, published_at)
VALUES ($1, 'published', false, 10, now())`, productionFixtureBookID)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<article><p>نص الإنتاج الأول.</p></article>', 'نص الإنتاج الأول.'),
       ($1, 2, '<article><p>نص الإنتاج الثاني.</p></article>', 'نص الإنتاج الثاني.')`,
		productionFixtureBookID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, parent_id, page_id, depth, ordinal, content)
VALUES ($1, $2, NULL, 1, 0, 1, 'باب الإنتاج الأول'),
       ($1, $3, NULL, 2, 0, 2, 'باب الإنتاج الثاني')`,
		productionFixtureBookID,
		productionFixtureHeadingOne,
		productionFixtureHeadingTwo,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, $2, 1, 1),
       ($1, $3, 2, 2)`,
		productionFixtureBookID,
		productionFixtureHeadingOne,
		productionFixtureHeadingTwo,
	)

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit production fixture tx: %v", err)
	}
}

func createProductionProject(t *testing.T, token string, bookID int, lang string) productionProjectResponse {
	t.Helper()

	resp := doJSON(
		t,
		http.MethodPost,
		baseURL()+"/v1/editorial/production-projects",
		bytes.NewBufferString(fmt.Sprintf(`{"book_id":%d,"lang":%q,"requires_review":true,"requires_audio":true}`, bookID, lang)),
		token,
	)
	var project productionProjectResponse
	decodeAndClose(t, resp, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create production project expected 201, got %d: %+v", resp.StatusCode, project)
	}

	return project
}

func saveProductionDrafts(t *testing.T, token string, projectID string) {
	t.Helper()

	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/metadata-draft", projectID),
		`{"display_title":"Production Book EN","bibliography":"Production bibliography","hint":"Production hint","description":"Production description"}`)
	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/author-draft", projectID),
		`{"name":"Production Author EN","biography":"Production author biography","death_text":"1445 H"}`)
	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/category-draft", projectID),
		`{"name":"Production Category EN"}`)

	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/toc/%d/translation-draft", projectID, productionFixtureHeadingOne),
		`{"title":"Production chapter one","content":"English production content 1"}`)
	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/toc/%d/summary-draft", projectID, productionFixtureHeadingOne),
		`{"summary":"English production summary 1"}`)
	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/toc/%d/audio-draft", projectID, productionFixtureHeadingOne),
		`{"url":"https://example.test/production-1.mp3","narrator":"Narrator","duration_seconds":121,"mime_type":"audio/mpeg"}`)

	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/toc/%d/translation-draft", projectID, productionFixtureHeadingTwo),
		`{"title":"Production chapter two","content":"English production content 2"}`)
	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/toc/%d/summary-draft", projectID, productionFixtureHeadingTwo),
		`{"summary":"English production summary 2"}`)
	putProductionDraft(t, token, fmt.Sprintf("/v1/editorial/production-projects/%s/toc/%d/audio-draft", projectID, productionFixtureHeadingTwo),
		`{"url":"https://example.test/production-2.mp3","narrator":"Narrator","duration_seconds":122,"mime_type":"audio/mpeg"}`)
}

func putProductionDraft(t *testing.T, token string, path string, body string) {
	t.Helper()

	resp := doJSON(t, http.MethodPut, baseURL()+path, bytes.NewBufferString(body), token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s expected 200, got %d", path, resp.StatusCode)
	}
}

func approveProductionDrafts(t *testing.T, token string, projectID string) {
	t.Helper()

	for _, assetType := range []string{"book_metadata", "author_metadata", "category_metadata"} {
		reviewProductionAsset(t, token, projectID, assetType, nil)
	}
	for _, headingID := range []int{productionFixtureHeadingOne, productionFixtureHeadingTwo} {
		for _, assetType := range []string{"section_translation", "heading_summary", "section_audio"} {
			id := headingID
			reviewProductionAsset(t, token, projectID, assetType, &id)
		}
	}
}

func reviewProductionAsset(t *testing.T, token string, projectID string, assetType string, headingID *int) {
	t.Helper()

	headingFragment := "null"
	if headingID != nil {
		headingFragment = fmt.Sprintf("%d", *headingID)
	}
	resp := doJSON(
		t,
		http.MethodPost,
		fmt.Sprintf("%s/v1/editorial/production-projects/%s/review", baseURL(), projectID),
		bytes.NewBufferString(fmt.Sprintf(`{"asset_type":%q,"heading_id":%s,"decision":"approve","note":"integration approved"}`, assetType, headingFragment)),
		token,
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("review %s expected 204, got %d", assetType, resp.StatusCode)
	}
}

func getProductionCompleteness(t *testing.T, token string, projectID string) productionCompletenessResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/editorial/production-projects/%s/completeness", baseURL(), projectID), nil, token)
	var completeness productionCompletenessResponse
	decodeAndClose(t, resp, &completeness)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("completeness expected 200, got %d: %+v", resp.StatusCode, completeness)
	}

	return completeness
}

func getProductionPublishCheck(t *testing.T, token string, projectID string) productionPublishCheckResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/editorial/production-projects/%s/publish-check", baseURL(), projectID), nil, token)
	var check productionPublishCheckResponse
	decodeAndClose(t, resp, &check)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish check expected 200, got %d: %+v", resp.StatusCode, check)
	}

	return check
}

func getProductionWorkspace(t *testing.T, token string, projectID string) productionWorkspaceResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/editorial/production-projects/%s/workspace", baseURL(), projectID), nil, token)
	var workspace productionWorkspaceResponse
	decodeAndClose(t, resp, &workspace)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace expected 200, got %d: %+v", resp.StatusCode, workspace)
	}

	return workspace
}

func getProductionActivity(t *testing.T, token string, projectID string) productionActivityResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/editorial/production-projects/%s/activity?limit=100", baseURL(), projectID), nil, token)
	var activity productionActivityResponse
	decodeAndClose(t, resp, &activity)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activity expected 200, got %d: %+v", resp.StatusCode, activity)
	}

	return activity
}

func getProductionCandidates(t *testing.T, token string, lang string, unstarted bool) productionCandidateListResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/editorial/production-candidates?lang=%s&category_id=%d&unstarted=%t",
		baseURL(),
		lang,
		productionFixtureCategoryID,
		unstarted,
	), nil, token)
	var candidates productionCandidateListResponse
	decodeAndClose(t, resp, &candidates)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("production candidates expected 200, got %d: %+v", resp.StatusCode, candidates)
	}

	return candidates
}

func getProductionDraftRevisions(
	t *testing.T,
	token string,
	projectID string,
	assetType string,
	headingID *int,
) productionDraftRevisionListResponse {
	t.Helper()

	url := fmt.Sprintf(
		"%s/v1/editorial/production-projects/%s/draft-revisions?asset_type=%s",
		baseURL(),
		projectID,
		assetType,
	)
	if headingID != nil {
		url += fmt.Sprintf("&heading_id=%d", *headingID)
	}
	resp := doJSON(t, http.MethodGet, url, nil, token)
	var revisions productionDraftRevisionListResponse
	decodeAndClose(t, resp, &revisions)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("production draft revisions expected 200, got %d: %+v", resp.StatusCode, revisions)
	}

	return revisions
}

func restoreProductionDraftRevision(
	t *testing.T,
	token string,
	projectID string,
	revisionID string,
) productionDraftRevisionResponse {
	t.Helper()

	resp := doJSON(
		t,
		http.MethodPost,
		fmt.Sprintf("%s/v1/editorial/production-projects/%s/draft-revisions/%s/restore", baseURL(), projectID, revisionID),
		nil,
		token,
	)
	var revision productionDraftRevisionResponse
	decodeAndClose(t, resp, &revision)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("production draft restore expected 200, got %d: %+v", resp.StatusCode, revision)
	}

	return revision
}

func getProductionMetadataDraft(t *testing.T, token string, projectID string) productionMetadataDraftResponse {
	t.Helper()

	resp := doJSON(
		t,
		http.MethodGet,
		fmt.Sprintf("%s/v1/editorial/production-projects/%s/metadata-draft", baseURL(), projectID),
		nil,
		token,
	)
	var draft productionMetadataDraftResponse
	decodeAndClose(t, resp, &draft)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("production metadata draft expected 200, got %d: %+v", resp.StatusCode, draft)
	}

	return draft
}

func assertProductionActivityContains(t *testing.T, activity productionActivityResponse, eventType string, assetType string) {
	t.Helper()

	for _, event := range activity.Events {
		if event.EventType != eventType {
			continue
		}
		if assetType == "" || (event.AssetType != nil && *event.AssetType == assetType) {
			return
		}
	}

	t.Fatalf("activity missing %s/%s: %+v", eventType, assetType, activity.Events)
}

func publishProductionProject(t *testing.T, token string, projectID string) productionProjectResponse {
	t.Helper()

	resp := doJSON(t, http.MethodPost, fmt.Sprintf("%s/v1/editorial/production-projects/%s/publish", baseURL(), projectID), nil, token)
	var project productionProjectResponse
	decodeAndClose(t, resp, &project)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish expected 200, got %d: %+v", resp.StatusCode, project)
	}

	return project
}

func unpublishProductionProject(t *testing.T, token string, projectID string) productionProjectResponse {
	t.Helper()

	resp := doJSON(t, http.MethodPost, fmt.Sprintf("%s/v1/editorial/production-projects/%s/unpublish", baseURL(), projectID), nil, token)
	var project productionProjectResponse
	decodeAndClose(t, resp, &project)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unpublish expected 200, got %d: %+v", resp.StatusCode, project)
	}

	return project
}

func getProductionTOCRead(t *testing.T, bookID int, headingID int, lang string) tocReadResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/books/%d/toc/%d/read?lang=%s",
		baseURL(),
		bookID,
		headingID,
		lang,
	), nil, "")
	var read tocReadResponse
	decodeAndClose(t, resp, &read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get production toc read %s expected 200, got %d", lang, resp.StatusCode)
	}

	return read
}

type productionProjectResponse struct {
	ID                string `json:"id"`
	BookID            int    `json:"book_id"`
	Lang              string `json:"lang"`
	WorkflowStatus    string `json:"workflow_status"`
	PublicationStatus string `json:"publication_status"`
}

type productionCompletenessResponse struct {
	Ready         bool `json:"ready"`
	RequiredCount int  `json:"required_count"`
	MissingCount  int  `json:"missing_count"`
}

type productionPublishCheckResponse struct {
	Ready          bool                          `json:"ready"`
	CanPublish     bool                          `json:"can_publish"`
	RequiredCount  int                           `json:"required_count"`
	MissingCount   int                           `json:"missing_count"`
	BlockingErrors []productionPublishBlocker    `json:"blocking_errors"`
	Missing        []productionMissingAssetEntry `json:"missing"`
}

type productionPublishBlocker struct {
	Code      string `json:"code"`
	AssetType string `json:"asset_type"`
	Message   string `json:"message"`
}

type productionMissingAssetEntry struct {
	AssetType string `json:"asset_type"`
	Message   string `json:"message"`
}

type productionActivityResponse struct {
	Events []productionActivityEvent `json:"events"`
	Total  int                       `json:"total"`
}

type productionCandidateListResponse struct {
	Candidates []productionCandidateResponse `json:"candidates"`
	Total      int                           `json:"total"`
}

type productionCandidateResponse struct {
	BookID            int     `json:"book_id"`
	HeadingCount      int     `json:"heading_count"`
	PageCount         int     `json:"page_count"`
	ExistingProjectID *string `json:"existing_project_id"`
}

type productionActivityEvent struct {
	EventType string  `json:"event_type"`
	AssetType *string `json:"asset_type"`
}

type productionDraftRevisionListResponse struct {
	Revisions []productionDraftRevisionResponse `json:"revisions"`
	Total     int                               `json:"total"`
}

type productionDraftRevisionResponse struct {
	ID        string `json:"id"`
	AssetType string `json:"asset_type"`
	Version   int    `json:"version"`
}

type productionMetadataDraftResponse struct {
	DisplayTitle string `json:"display_title"`
}

type productionWorkspaceResponse struct {
	Project      productionProjectResponse      `json:"project"`
	Book         productionWorkspaceBook        `json:"book"`
	Completeness productionCompletenessResponse `json:"completeness"`
	Metadata     productionAssetStatus          `json:"metadata"`
	Headings     []productionWorkspaceHeading   `json:"headings"`
}

type productionWorkspaceBook struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	HasContent bool   `json:"has_content"`
}

type productionWorkspaceHeading struct {
	HeadingID   int                   `json:"heading_id"`
	Translation productionAssetStatus `json:"translation"`
	Summary     productionAssetStatus `json:"summary"`
	Audio       productionAssetStatus `json:"audio"`
}

type productionAssetStatus struct {
	AssetType    string  `json:"asset_type"`
	Required     bool    `json:"required"`
	Exists       bool    `json:"exists"`
	Complete     bool    `json:"complete"`
	ReviewStatus *string `json:"review_status"`
	FinalExists  bool    `json:"final_exists"`
}

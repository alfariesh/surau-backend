# Editorial Production Frontend Implementation Guide

This document is the implementation contract for the first frontend module for kitab translation production. It assumes there is no frontend yet, so it covers routes, screens, API calls, data models, permissions, state transitions, validation, refresh strategy, and edge cases.

The goal of the MVP is to let a small team run the complete workflow:

1. Pick a raw kitab from PostgreSQL.
2. Create a `book_id + lang` production project.
3. Edit metadata, author, category, per-TOC translation, per-TOC summary, and optional per-TOC audio URL.
4. Review drafts when required.
5. Check publish readiness.
6. Publish or unpublish as admin.
7. Track activity and restore draft revisions.

Audio upload and import/export are not required for the first frontend. Audio can start as a URL field because the backend already supports `section_audio` draft URLs.

## Backend Scope

Base REST prefix:

```text
/v1
```

Auth:

```http
Authorization: Bearer <jwt>
Content-Type: application/json
```

Supported production target languages:

```ts
type ProductionLang = "id" | "en";
```

Region tags sent to the backend, such as `en-US` or `id-ID`, normalize to `en` or `id`. Explicit unsupported languages return:

```json
{ "error": "unsupported language" }
```

## Roles

Official user roles:

```ts
type UserRole = "user" | "editor" | "admin";
```

Role behavior:

| Capability | user | editor | admin |
| --- | --- | --- | --- |
| Public reader | yes | yes | yes |
| Login/profile | yes | yes | yes |
| Production dashboard | no | yes | yes |
| Raw candidate picker | no | yes | yes |
| Production queue | no | yes | yes |
| Workspace read | no | yes | yes |
| Draft create/update/delete | no | yes | yes |
| Draft revision list/restore | no | yes | yes |
| Submit/approve/reject review | no | yes | yes |
| Publish check | no | yes | yes |
| Publish/unpublish | no | no | yes |
| Final asset soft delete | no | no | yes |
| Source Arabic edit draft | no | yes | yes |
| Source Arabic publish | no | no | yes |
| User role management | no | no | yes |

Frontend rule:

- Hide editorial routes for `user`.
- Show draft/review tools for `editor` and `admin`.
- Show publish, unpublish, final delete, and user role management only for `admin`.
- Do not rely only on hidden UI. Handle `403 {"error":"forbidden"}` everywhere.

Fetch current account after login:

```http
GET /v1/user/profile
```

Response includes `role`:

```ts
interface UserAccount {
  id: string;
  username: string;
  email: string;
  role: UserRole;
  email_verified: boolean;
  created_at: string;
  updated_at: string;
  profile: unknown;
  preferences: unknown;
  onboarding_required: boolean;
}
```

## Recommended Frontend Routes

Use these routes as the first module structure:

```text
/editorial
/editorial/production
/editorial/production/candidates
/editorial/production/projects
/editorial/production/projects/:projectId
/editorial/production/projects/:projectId/activity
/editorial/feedback
/editorial/source-edits
/admin/users
```

For MVP, `/editorial/production` can be the main page with dashboard cards, queue tabs, recent activity, and a "New project" action that opens the candidate picker.

## Navigation Model

Recommended left navigation:

- Production
- Raw Picker
- Queue
- Feedback
- Source Edits
- Admin, visible only to `admin`

Recommended production tabs:

- Needs Work
- Ready To Publish
- Published
- All Projects
- Candidates
- Activity

Keep the first screen operational, not a landing page.

## Core Data Types

Use these TypeScript types as the frontend source of truth. They mirror the backend JSON names.

```ts
export type ProductionWorkflowStatus =
  | "candidate"
  | "drafting"
  | "in_review"
  | "ready"
  | "published"
  | "archived";

export type ProductionPublicationStatus = "hidden" | "published" | "archived";

export type ProductionReviewStatus =
  | "draft"
  | "pending_review"
  | "approved"
  | "rejected";

export type ProductionReviewDecision = "submit" | "approve" | "reject";

export type ProductionAssetType =
  | "book_metadata"
  | "author_metadata"
  | "category_metadata"
  | "section_translation"
  | "heading_summary"
  | "section_audio";

export type ProductionEventType =
  | "production_project.create"
  | "production_project.update"
  | "production_asset.draft_save"
  | "production_asset.draft_delete"
  | "production_asset.draft_restore"
  | "production_asset.review"
  | "production_project.publish"
  | "production_project.unpublish"
  | "production_asset.final_delete";

export interface ApiError {
  error: string;
}
```

```ts
export interface BookProductionProject {
  id: string;
  book_id: number;
  lang: ProductionLang;
  workflow_status: ProductionWorkflowStatus;
  publication_status: ProductionPublicationStatus;
  requires_review: boolean;
  requires_audio: boolean;
  priority: number;
  owner_id?: string | null;
  notes?: string | null;
  created_by?: string | null;
  updated_by?: string | null;
  published_by?: string | null;
  created_at: string;
  updated_at: string;
  published_at?: string | null;
  archived_at?: string | null;
}

export interface BookProductionCandidate {
  book_id: number;
  name: string;
  category_id?: number | null;
  category_name?: string | null;
  author_id?: number | null;
  author_name?: string | null;
  has_content: boolean;
  heading_count: number;
  page_count: number;
  existing_project_id?: string | null;
  existing_workflow_status?: ProductionWorkflowStatus | null;
  existing_publication_status?: ProductionPublicationStatus | null;
  existing_project_updated_at?: string | null;
}

export interface ProductionAssetStatus {
  asset_type: ProductionAssetType;
  heading_id?: number | null;
  required: boolean;
  exists: boolean;
  complete: boolean;
  review_status?: ProductionReviewStatus | null;
  updated_at?: string | null;
  reviewed_at?: string | null;
  final_exists: boolean;
}

export interface BookProductionWorkspaceHeading {
  book_id: number;
  heading_id: number;
  parent_id?: number | null;
  page_id: number;
  depth: number;
  ordinal: number;
  source_title: string;
  translation: ProductionAssetStatus;
  summary: ProductionAssetStatus;
  audio: ProductionAssetStatus;
}

export interface BookProductionWorkspaceBook {
  id: number;
  name: string;
  category_id?: number | null;
  category_name?: string | null;
  author_id?: number | null;
  author_name?: string | null;
  has_content: boolean;
}

export interface BookProductionWorkspace {
  project: BookProductionProject;
  book: BookProductionWorkspaceBook;
  completeness: BookProductionCompleteness;
  metadata: ProductionAssetStatus;
  author?: ProductionAssetStatus | null;
  category?: ProductionAssetStatus | null;
  headings: BookProductionWorkspaceHeading[];
}
```

```ts
export interface BookProductionMissingAsset {
  asset_type: ProductionAssetType;
  heading_id?: number | null;
  message: string;
}

export interface BookProductionCompleteness {
  project: BookProductionProject;
  ready: boolean;
  required_count: number;
  complete_count: number;
  missing_count: number;
  missing: BookProductionMissingAsset[];
}

export interface BookProductionBlocking {
  code: string;
  asset_type?: ProductionAssetType | "";
  heading_id?: number | null;
  message: string;
}

export interface BookProductionPublishCheck {
  project: BookProductionProject;
  ready: boolean;
  can_publish: boolean;
  required_count: number;
  complete_count: number;
  missing_count: number;
  missing: BookProductionMissingAsset[];
  blocking_errors: BookProductionBlocking[];
}
```

```ts
export interface BookProductionEvent {
  id: string;
  project_id: string;
  actor_id?: string | null;
  event_type: ProductionEventType;
  asset_type?: ProductionAssetType | null;
  heading_id?: number | null;
  note?: string | null;
  payload?: Record<string, unknown> | null;
  created_at: string;
}

export interface BookProductionDashboard {
  lang: ProductionLang;
  candidate_count: number;
  active_project_count: number;
  needs_work_count: number;
  ready_to_publish_count: number;
  published_count: number;
  recent_events: BookProductionEvent[];
  recent_events_total: number;
}

export interface BookProductionDraftRevision {
  id: string;
  project_id: string;
  asset_type: ProductionAssetType;
  heading_id?: number | null;
  version: number;
  actor_id?: string | null;
  snapshot: Record<string, unknown>;
  created_at: string;
}
```

## Draft Data Types

Every draft response includes audit and review fields. Save payloads do not include `project_id`, `heading_id`, or review fields because the backend derives them.

```ts
export interface BookMetadataTranslationEdit {
  project_id: string;
  display_title: string;
  bibliography?: string | null;
  hint?: string | null;
  description?: string | null;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
  review_status: ProductionReviewStatus;
  review_note?: string | null;
  updated_by?: string | null;
  reviewed_by?: string | null;
  updated_at: string;
  reviewed_at?: string | null;
}

export interface SaveMetadataTranslationDraft {
  display_title: string;
  bibliography?: string | null;
  hint?: string | null;
  description?: string | null;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
}
```

```ts
export interface AuthorTranslationEdit {
  project_id: string;
  name: string;
  biography?: string | null;
  death_text?: string | null;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
  review_status: ProductionReviewStatus;
  review_note?: string | null;
  updated_by?: string | null;
  reviewed_by?: string | null;
  updated_at: string;
  reviewed_at?: string | null;
}

export interface SaveAuthorTranslationDraft {
  name: string;
  biography?: string | null;
  death_text?: string | null;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
}
```

```ts
export interface CategoryTranslationEdit {
  project_id: string;
  name: string;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
  review_status: ProductionReviewStatus;
  review_note?: string | null;
  updated_by?: string | null;
  reviewed_by?: string | null;
  updated_at: string;
  reviewed_at?: string | null;
}

export interface SaveCategoryTranslationDraft {
  name: string;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
}
```

```ts
export interface SectionTranslationEdit {
  project_id: string;
  heading_id: number;
  title?: string | null;
  content: string;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
  review_status: ProductionReviewStatus;
  review_note?: string | null;
  updated_by?: string | null;
  reviewed_by?: string | null;
  updated_at: string;
  reviewed_at?: string | null;
}

export interface SaveSectionTranslationDraft {
  title?: string | null;
  content: string;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
}
```

```ts
export interface HeadingSummaryEdit {
  project_id: string;
  heading_id: number;
  summary: string;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
  review_status: ProductionReviewStatus;
  review_note?: string | null;
  updated_by?: string | null;
  reviewed_by?: string | null;
  updated_at: string;
  reviewed_at?: string | null;
}

export interface SaveHeadingSummaryDraft {
  summary: string;
  source?: string | null;
  metadata?: Record<string, unknown> | null;
}
```

```ts
export interface SectionAudioEdit {
  project_id: string;
  heading_id: number;
  url: string;
  narrator?: string | null;
  duration_seconds?: number | null;
  mime_type?: string | null;
  metadata?: Record<string, unknown> | null;
  review_status: ProductionReviewStatus;
  review_note?: string | null;
  updated_by?: string | null;
  reviewed_by?: string | null;
  updated_at: string;
  reviewed_at?: string | null;
}

export interface SaveSectionAudioDraft {
  url: string;
  narrator?: string | null;
  duration_seconds?: number | null;
  mime_type?: string | null;
  metadata?: Record<string, unknown> | null;
}
```

## API Client Contract

Recommended fetch wrapper behavior:

1. Attach `Authorization` if logged in.
2. Parse JSON for non-204 responses.
3. For non-2xx, throw an `ApiError` with `status`, `error`, optional `code`, and optional `request_id`.
4. On `401`, route to login.
5. On `403`, show permission denied and hide the blocked action.
6. Do not special-case backend validation messages too much. They are stable enough for UX labels but not for business logic.

Example:

```ts
export async function api<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await fetch(`${API_BASE}/v1${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...init.headers,
    },
  });

  if (res.status === 204) return undefined as T;

  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw {
      status: res.status,
      error: body.message ?? body.error ?? "request failed",
      code: body.code,
      requestId: body.request_id,
    };
  }

  return body as T;
}
```

## Error Map

Common backend errors:

| Status | Body | Typical cause | Frontend action |
| --- | --- | --- | --- |
| 400 | `{"error":"invalid request body"}` | validation failed | Highlight form fields generically |
| 400 | `{"error":"unsupported language"}` | lang not `id` or `en` | Reset language selector |
| 400 | `{"error":"invalid status"}` | bad workflow/publication filter | Clear invalid filter |
| 400 | `{"error":"invalid asset_type"}` | wrong asset type | Developer bug or stale route |
| 400 | `{"error":"invalid production draft"}` | heading sent for scalar asset, or similar | Fix revision/review target |
| 401 | `{"error":"unauthorized"}` | missing/expired token | Login redirect |
| 403 | `{"error":"forbidden"}` | role lacks access | Show permission denied |
| 404 | `{"error":"production project not found"}` | project deleted/invalid id | Return to queue |
| 404 | `{"error":"draft not found"}` | GET/delete/review draft before save | Show empty draft state |
| 404 | `{"error":"heading not found"}` | invalid heading for project | Refresh workspace |
| 409 | `{"error":"production project already exists","existing_project_id":"..."}` | duplicate active `book_id + lang` | Link existing project |
| 409 | `{"error":"production project is not ready","blocking_errors":[...]}` | publish blocked | Show returned blockers |
| 412 | `{"error":"precondition failed"}` | stale `If-Match` on draft/project mutation | Refresh latest data and ask user to retry or merge |
| 500 | `{"error":"internal server error"}` | server/db issue | Toast and retry option |

## Optimistic Locking

GET responses for production projects and draft resources include `ETag` when the resource has `updated_at`. Save/delete draft mutations, `PATCH /production-projects/{id}`, and publish/unpublish accept `If-Match`.

Recommended FE behavior:

1. Store the `ETag` returned by GET for each open draft or project.
2. Send that value as `If-Match` on PUT, DELETE, PATCH, publish, and unpublish.
3. If the backend returns `412`, refetch the resource and show a stale-change conflict state.
4. For creating a brand-new draft after a `404 draft not found`, either omit `If-Match` for backward-compatible create or send `If-Match: *`.

## Endpoints By Screen

### 1. Auth Gate

Login:

```http
POST /v1/auth/login
```

Request:

```json
{
  "email": "editor@example.com",
  "password": "secret123"
}
```

Response:

```json
{
  "token": "eyJ..."
}
```

Then fetch:

```http
GET /v1/user/profile
```

Use `role` to route:

- `editor` -> `/editorial/production`
- `admin` -> `/editorial/production`
- `user` -> no editorial access

### 2. Production Dashboard

```http
GET /v1/editorial/production-dashboard?lang=id&activity_limit=20
```

Use for the top-level operational page.

Response:

```json
{
  "lang": "id",
  "candidate_count": 120,
  "active_project_count": 18,
  "needs_work_count": 12,
  "ready_to_publish_count": 3,
  "published_count": 25,
  "recent_events": [],
  "recent_events_total": 42
}
```

UI:

- Language segmented control: `id`, `en`.
- Cards:
  - Candidates
  - Active Projects
  - Needs Work
  - Ready To Publish
  - Published
- Recent activity list.
- Primary action: New Production Project.

Behavior:

- Clicking Candidates opens raw picker with `unstarted=true`.
- Clicking Needs Work opens queue with `needs_work=true`.
- Clicking Ready To Publish opens queue with `ready_to_publish=true`.
- Clicking Published opens queue with `publication_status=published`.

### 3. Global Activity

```http
GET /v1/editorial/production-activity?lang=id&limit=50&offset=0
```

Response:

```ts
interface ProductionEventList {
  events: BookProductionEvent[];
  total: number;
}
```

Use for:

- Dashboard recent activity.
- Activity page across all projects.
- Audit-style troubleshooting.

Event display labels:

| event_type | Label |
| --- | --- |
| `production_project.create` | Project created |
| `production_project.update` | Project updated |
| `production_asset.draft_save` | Draft saved |
| `production_asset.draft_delete` | Draft deleted |
| `production_asset.draft_restore` | Draft restored |
| `production_asset.review` | Review updated |
| `production_project.publish` | Project published |
| `production_project.unpublish` | Project unpublished |
| `production_asset.final_delete` | Final asset deleted |

For `production_asset.review`, check `payload.decision` and `payload.review_status` when present.

### 4. Raw Picker

```http
GET /v1/editorial/production-candidates?lang=id&q=&category_id=&author_id=&has_content=true&unstarted=true&limit=50&offset=0
```

Response:

```ts
interface ProductionCandidateList {
  candidates: BookProductionCandidate[];
  total: number;
}
```

Filters:

| Query | Type | Required | Notes |
| --- | --- | --- | --- |
| `lang` | `id|en` | yes | target production language |
| `q` | string | no | book title query |
| `category_id` | number | no | raw/current category |
| `author_id` | number | no | source author |
| `has_content` | boolean | no | usually `true` |
| `unstarted` | boolean | no | `true` hides active project for lang |
| `limit` | number | no | default 50, backend clamps |
| `offset` | number | no | default 0 |

Candidate row display:

- Title: `name`
- Author: `author_name` or empty
- Category: `category_name` or empty
- Content badge: `has_content`
- Counts: `heading_count`, `page_count`
- Existing project badge if `existing_project_id` exists

Create project:

```http
POST /v1/editorial/production-projects
```

Request:

```json
{
  "book_id": 797,
  "lang": "id",
  "requires_review": true,
  "requires_audio": false,
  "priority": 10,
  "owner_id": null,
  "notes": "Initial translation batch"
}
```

Defaults and validation:

- `requires_review` defaults to `true` if omitted.
- `requires_audio` defaults to `false` if omitted.
- `priority` minimum is `0`.
- `book_id` must have imported content/headings, otherwise backend returns `409 production project is not ready`.
- Duplicate active `book_id + lang` returns `409 production project already exists` with `existing_project_id` when the active project can be resolved.

After success:

- Navigate to `/editorial/production/projects/:id`.
- Refresh dashboard and queue caches.

### 5. Production Queue

```http
GET /v1/editorial/production-projects?book_id=&lang=id&workflow_status=&publication_status=&ready_to_publish=&needs_work=&limit=50&offset=0
```

Response:

```ts
interface ProductionProjectList {
  projects: BookProductionProject[];
  total: number;
}
```

Filters:

| Query | Type | Notes |
| --- | --- | --- |
| `book_id` | number | direct lookup |
| `lang` | `id|en` | optional |
| `workflow_status` | workflow enum | optional |
| `publication_status` | publication enum | optional |
| `ready_to_publish` | boolean | only active hidden projects that can publish |
| `needs_work` | boolean | only active hidden projects that cannot publish |
| `limit` | number | default 50 |
| `offset` | number | default 0 |

Important:

- `ready_to_publish=true` and `needs_work=true` together return `400 invalid status`.
- `ready_to_publish` and `needs_work` are only active when query value is `true`.
- `publication_status=published` is the safest Published tab filter.

Recommended queue tabs:

```text
Needs Work:          lang=<selected>&needs_work=true
Ready To Publish:   lang=<selected>&ready_to_publish=true
Published:          lang=<selected>&publication_status=published
Drafting/In Review: lang=<selected>&workflow_status=drafting|in_review
All:                lang=<selected>
```

Queue row fields:

- Book ID
- Lang
- Workflow status
- Publication status
- Requires review
- Requires audio
- Priority
- Owner
- Updated time
- Published time

Actions:

- Open workspace.
- Open publish-check.
- Admin publish if ready.
- Admin unpublish if published.

### 6. Workspace

Load:

```http
GET /v1/editorial/production-projects/{id}/workspace
```

Use this as the primary editor screen payload. It gives enough data to render:

- Project header
- Source book metadata
- Completion progress
- Scalar asset statuses: metadata, author, category
- TOC list with translation, summary, audio statuses

Do not call every individual draft endpoint on initial page load. Call individual draft endpoints only when an asset editor opens.

Workspace layout:

- Header:
  - book name
  - lang
  - workflow status
  - publication status
  - priority
  - requires review
  - requires audio
  - updated timestamp
- Left column:
  - TOC tree/list from `headings`
  - status chips for translation, summary, audio
- Main column:
  - selected asset editor
  - source title
  - draft form
- Right panel:
  - completeness
  - publish-check
  - revisions
  - project activity

Status chip logic:

| Field | Meaning |
| --- | --- |
| `required=false` | Optional, visually muted |
| `exists=false` | Not started |
| `exists=true`, `complete=false` | Started but incomplete |
| `complete=true`, `review_status=draft` | Complete draft |
| `review_status=pending_review` | Waiting review |
| `review_status=approved` | Approved |
| `review_status=rejected` | Rejected, needs edit |
| `final_exists=true` | Already published/final row exists |

### 7. Project Settings

Get one project:

```http
GET /v1/editorial/production-projects/{id}
```

Patch:

```http
PATCH /v1/editorial/production-projects/{id}
```

Request:

```json
{
  "workflow_status": "drafting",
  "requires_review": true,
  "requires_audio": false,
  "priority": 5,
  "owner_id": null,
  "notes": "Work from volume 1 first"
}
```

Use cases:

- Toggle `requires_audio`.
- Toggle `requires_review`.
- Change priority.
- Add notes.
- Archive a project via `workflow_status=archived`.

Do not expose `workflow_status=published` as a manual dropdown action. Publishing should happen through the publish endpoint.

After patch:

- Refresh workspace.
- Refresh dashboard/queue.

### 8. Scalar Draft Editors

Scalar assets do not use `heading_id`:

- `book_metadata`
- `author_metadata`
- `category_metadata`

Metadata draft:

```http
GET /v1/editorial/production-projects/{id}/metadata-draft
PUT /v1/editorial/production-projects/{id}/metadata-draft
DELETE /v1/editorial/production-projects/{id}/metadata-draft
```

Save request:

```json
{
  "display_title": "Translated Book Title",
  "bibliography": "Bibliography text",
  "hint": "Short hint",
  "description": "Long description",
  "source": "manual",
  "metadata": {}
}
```

Validation:

- `display_title` required, max 500.
- `bibliography`, `hint`, `description` max 10000.
- `source` max 255.

Author draft:

```http
GET /v1/editorial/production-projects/{id}/author-draft
PUT /v1/editorial/production-projects/{id}/author-draft
DELETE /v1/editorial/production-projects/{id}/author-draft
```

Save request:

```json
{
  "name": "Translated Author Name",
  "biography": "Biography",
  "death_text": "w. 676 H",
  "source": "manual",
  "metadata": {}
}
```

Validation:

- `name` required, max 500.
- `biography` max 20000.
- `death_text` max 255.
- `source` max 255.

Category draft:

```http
GET /v1/editorial/production-projects/{id}/category-draft
PUT /v1/editorial/production-projects/{id}/category-draft
DELETE /v1/editorial/production-projects/{id}/category-draft
```

Save request:

```json
{
  "name": "Translated Category",
  "source": "manual",
  "metadata": {}
}
```

Validation:

- `name` required, max 500.
- `source` max 255.

When GET returns `404 draft not found`, show an empty form. Do not treat it as a fatal workspace error.

When GET returns `200`, keep the response `ETag` and send it as `If-Match` on save/delete. A `412 precondition failed` means another editor changed the draft after this screen loaded.

### 9. TOC Draft Editors

TOC assets always require `heading_id`:

- `section_translation`
- `heading_summary`
- `section_audio`

Section translation:

```http
GET /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft
PUT /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft
```

Save request:

```json
{
  "title": "Translated section title",
  "content": "Translated section content",
  "source": "manual",
  "metadata": {}
}
```

Validation:

- `content` required.
- `title` max 1000.
- `source` max 255.

Heading summary:

```http
GET /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft
PUT /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft
```

Save request:

```json
{
  "summary": "Short or long summary",
  "source": "manual",
  "metadata": {}
}
```

Validation:

- `summary` required, max 20000.
- `source` max 255.

Section audio:

```http
GET /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft
PUT /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft
```

Save request:

```json
{
  "url": "https://cdn.example.com/audio/section.mp3",
  "narrator": "Narrator Name",
  "duration_seconds": 120,
  "mime_type": "audio/mpeg",
  "metadata": {}
}
```

Validation:

- `url` required, valid URL, max 2000.
- `narrator` max 255.
- `duration_seconds` min 0.
- `mime_type` max 255.

MVP audio UI:

- URL input
- Narrator input
- Duration input
- MIME type select or input
- Preview audio player if URL is present

Later audio upload can replace URL entry without changing the rest of the production workflow.

### 10. Draft Save Behavior

Backend behavior:

- Every successful draft save creates a draft revision.
- Every save resets `review_status` to `draft`.
- Save updates project workflow to `drafting`.
- Save emits a `production_asset.draft_save` activity event.
- Delete emits `production_asset.draft_delete`.
- Restore emits `production_asset.draft_restore`, resets current draft review status to `draft`, and creates a new revision.

Frontend behavior:

- Use explicit Save button for MVP.
- Autosave can be added later, but avoid saving on every keystroke because each save creates a revision.
- After save:
  - Update current draft form with response.
  - Refresh workspace status.
  - Refresh revisions for that asset.
  - Refresh publish-check/completeness if visible.
- After delete:
  - Clear form.
  - Refresh workspace.
  - Refresh publish-check/completeness.

Recommended save UX:

- Dirty state indicator.
- Disable Save while request in flight.
- Show saved timestamp from response `updated_at`.
- Warn before navigation if dirty.

### 11. Review

```http
POST /v1/editorial/production-projects/{id}/review
```

Request:

```json
{
  "asset_type": "section_translation",
  "heading_id": 123,
  "decision": "submit",
  "note": "Ready for review"
}
```

For scalar assets, omit `heading_id`:

```json
{
  "asset_type": "book_metadata",
  "decision": "approve",
  "note": "Looks good"
}
```

Rules:

- `asset_type` required.
- `decision` required: `submit`, `approve`, `reject`.
- `heading_id` required for `section_translation`, `heading_summary`, `section_audio`.
- `heading_id` ignored/cleared for scalar assets by backend.
- Note max 2000.
- Reviewing a missing draft returns `404 draft not found`.

Decision effects:

| decision | New review status | Project workflow effect |
| --- | --- | --- |
| `submit` | `pending_review` | project touched to `in_review` |
| `approve` | `approved` | project touched |
| `reject` | `rejected` | project touched |

Frontend actions:

- Show Submit when draft exists and is not pending/approved.
- Show Approve and Reject for editor/admin. There is no self-review restriction in v1.
- After review mutation, refresh the draft, workspace, publish-check, and activity.

### 12. Revisions

List revisions:

```http
GET /v1/editorial/production-projects/{id}/draft-revisions?asset_type=book_metadata&limit=50&offset=0
GET /v1/editorial/production-projects/{id}/draft-revisions?asset_type=section_translation&heading_id=123&limit=50&offset=0
```

Response:

```ts
interface ProductionDraftRevisionList {
  revisions: BookProductionDraftRevision[];
  total: number;
}
```

Target rules:

| Asset type | `heading_id` |
| --- | --- |
| `book_metadata` | must be omitted |
| `author_metadata` | must be omitted |
| `category_metadata` | must be omitted |
| `section_translation` | required |
| `heading_summary` | required |
| `section_audio` | required |

Restore:

```http
POST /v1/editorial/production-projects/{id}/draft-revisions/{revision_id}/restore
```

Response:

```ts
BookProductionDraftRevision
```

Restore behavior:

- Writes revision snapshot back into the active draft.
- Resets review status to `draft`.
- Creates a new revision with the restored content.
- Touches project workflow to `drafting`.
- Emits `production_asset.draft_restore`.

Revision UI:

- Show newest first.
- Show version number.
- Show created time.
- Show actor if known.
- Show snapshot preview or diff.
- Restore should require confirmation.
- After restore, refresh current draft, workspace, publish-check, revisions, and activity.

### 13. Completeness And Publish Check

Completeness:

```http
GET /v1/editorial/production-projects/{id}/completeness
```

Publish check:

```http
GET /v1/editorial/production-projects/{id}/publish-check
```

Use `publish-check` for publish UX. It includes `blocking_errors`.

Publish readiness rules:

- Source book must have `has_content=true`.
- Source book must have headings.
- Metadata translation draft is required.
- Author translation draft is required if source book has an author.
- Category translation draft is required if source book has a category.
- Section translation draft is required for every non-deleted heading.
- Heading summary draft is required for every non-deleted heading.
- Section audio draft is required for every non-deleted heading only when `requires_audio=true`.
- If `requires_review=true`, all required drafts must be `approved`.
- If `requires_review=false`, required drafts must exist and must not be `rejected`.

Publish panel UI:

- Progress: `complete_count / required_count`.
- Missing count.
- Missing list grouped by asset type.
- Jump from missing item to the relevant editor.
- Publish button visible only for admin.
- Publish button disabled unless `can_publish=true`.

Example blocker:

```json
{
  "code": "missing_required_asset",
  "asset_type": "heading_summary",
  "heading_id": 123,
  "message": "heading summary draft is missing"
}
```

### 14. Publish And Unpublish

Admin only.

Publish:

```http
POST /v1/editorial/production-projects/{id}/publish
```

Response:

```ts
BookProductionProject
```

Publish behavior:

- Backend re-validates readiness.
- Draft assets are upserted into final reader tables.
- Soft-delete flags on final rows are cleared.
- Project becomes published.
- Public reader starts exposing `lang=id|en` final assets for this book+lang.
- Activity event `production_project.publish` is recorded.

If publish fails with `409 production project is not ready`, the response mirrors publish-check fields and includes `blocking_errors`, so show those blockers directly. A separate publish-check fetch is only needed if the UI wants to refresh state again.

Publish and unpublish should send the current project `ETag` as `If-Match`. If the response is `412`, reload the project/workspace before trying again.

Unpublish:

```http
POST /v1/editorial/production-projects/{id}/unpublish
```

Behavior:

- Hides book+lang from public translated reader by project publication status.
- Does not delete final rows.
- Activity event `production_project.unpublish` is recorded.

After publish/unpublish:

- Refresh project.
- Refresh workspace.
- Refresh queue/dashboard.
- Optionally test reader URL:

```http
GET /v1/books/{book_id}/toc/{heading_id}/read?lang=id
```

### 15. Final Asset Soft Delete

Admin only. Use rarely, behind confirmation.

Project-scoped final assets:

```http
DELETE /v1/editorial/production-projects/{id}/final-assets/{asset_type}
```

Allowed `asset_type`:

- `book_metadata`
- `author_metadata`
- `category_metadata`

Optional body:

```json
{
  "reason": "Incorrect final metadata"
}
```

TOC-scoped final assets:

```http
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/final-assets/{asset_type}
```

Allowed `asset_type`:

- `section_translation`
- `heading_summary`
- `section_audio`

Behavior:

- Soft-deletes final rows.
- Hides the publication.
- Keeps audit/feedback history safe.
- Emits `production_asset.final_delete`.

### 16. Project Activity

Per-project:

```http
GET /v1/editorial/production-projects/{id}/activity?limit=50&offset=0
```

Use in workspace side panel or activity tab.

Global:

```http
GET /v1/editorial/production-activity?lang=id&limit=50&offset=0
```

Activity rendering:

- Sort is newest first from backend.
- Show event label.
- Show asset label if `asset_type` exists.
- Show heading jump link if `heading_id` exists.
- Show `note` if present.
- Show key payload values for review and project events.

### 17. Feedback Review

This is not required for first production editor, but it should be part of the editorial module navigation because it feeds translation quality work.

```http
GET /v1/editorial/translation-feedbacks?book_id=&heading_id=&lang=&vote=&status=&limit=&offset=
GET /v1/editorial/translation-feedbacks/summary?book_id=&heading_id=&lang=&vote=&status=&limit=
POST /v1/editorial/translation-feedbacks/{id}/resolve
POST /v1/editorial/translation-feedbacks/{id}/reopen
```

Use cases:

- List reader feedback for translations.
- Filter by book, heading, language, vote, status.
- Resolve/reopen feedback after correcting drafts or final assets.

### 18. Source Arabic Edit Endpoints

These are separate from translation production. They edit source/publication metadata, pages, and headings. Add this as a later tab unless source correction is needed immediately.

Editor/admin:

```http
GET /v1/editorial/books?q=&status=&category_id=&has_content=&limit=&offset=
GET /v1/editorial/books/{book_id}/metadata-draft
PUT /v1/editorial/books/{book_id}/metadata-draft
GET /v1/editorial/books/{book_id}/pages/{page_id}
PUT /v1/editorial/books/{book_id}/pages/{page_id}/draft
GET /v1/editorial/books/{book_id}/headings/{heading_id}/draft
PUT /v1/editorial/books/{book_id}/headings/{heading_id}/draft
```

Admin only:

```http
PUT /v1/editorial/books/{book_id}/publication
POST /v1/editorial/books/{book_id}/metadata-draft/publish
POST /v1/editorial/books/{book_id}/pages/{page_id}/publish
POST /v1/editorial/books/{book_id}/headings/{heading_id}/publish
POST /v1/editorial/collections/{slug}/items
```

Do not mix source edit drafts with production translation drafts in the same form state. They are different workflows.

## Screen Specifications

### Production Home

Data:

- `GET /production-dashboard?lang=...`
- `GET /production-projects?lang=...&needs_work=true`
- `GET /production-projects?lang=...&ready_to_publish=true`

Controls:

- Language selector.
- Queue tabs.
- New project button.
- Refresh button.

States:

- Loading dashboard.
- Empty queue.
- Permission denied.
- API error.

### Candidate Picker

Data:

- `GET /production-candidates`
- `POST /production-projects`

Controls:

- Search input with debounce.
- Has content toggle default `true`.
- Unstarted toggle default `true`.
- Category filter.
- Author filter.
- Pagination.

Create modal fields:

- `requires_review`, default true.
- `requires_audio`, default false.
- `priority`, default 0 or 10 depending UI preference.
- `notes`.

Edge cases:

- Existing project shown in candidate row when `unstarted=false`.
- Duplicate create returns `409`; use `existing_project_id` from the response to link to the active project.
- `heading_count=0` or `has_content=false` should disable create even before backend rejects.

### Queue

Data:

- `GET /production-projects`

Controls:

- Language.
- Workflow status.
- Publication status.
- Ready to publish.
- Needs work.
- Search by book id if needed.
- Pagination.

Row actions:

- Open.
- Publish-check.
- Admin publish.
- Admin unpublish.

### Workspace

Data on initial load:

- `GET /production-projects/{id}/workspace`
- Optional: `GET /production-projects/{id}/publish-check`
- Optional: `GET /production-projects/{id}/activity?limit=20`

Data on editor open:

- scalar GET or TOC GET for selected asset
- revisions GET for selected asset

Recommended layout:

- Header: project/book/status/actions.
- TOC sidebar: headings with status chips.
- Editor panel:
  - Metadata tab
  - Author tab if `workspace.author` exists
  - Category tab if `workspace.category` exists
  - Translation tab for selected heading
  - Summary tab for selected heading
  - Audio tab for selected heading
- Right rail:
  - Completeness
  - Publish-check
  - Revisions
  - Activity

Form strategy:

- Treat each asset as its own form.
- Keep dirty state per asset target:

```ts
type DraftTarget =
  | { asset_type: "book_metadata" | "author_metadata" | "category_metadata" }
  | { asset_type: "section_translation" | "heading_summary" | "section_audio"; heading_id: number };
```

### Review Panel

Data:

- current draft response
- workspace status

Actions:

- Submit
- Approve
- Reject with note

Rules:

- Disable actions when draft does not exist.
- After reject, keep the rejected draft visible and editable.
- On save after reject, status becomes `draft`.

### Publish Panel

Data:

- `GET /publish-check`

Actions:

- Admin Publish
- Admin Unpublish if published

Rules:

- Publish visible only for admin.
- Publish disabled unless `can_publish=true`.
- Editor can see blockers but cannot publish.
- Missing items should link to the target editor.

## Refresh And Cache Strategy

Suggested query keys:

```ts
["profile"]
["production-dashboard", lang]
["production-activity", lang, limit, offset]
["production-candidates", filters]
["production-projects", filters]
["production-workspace", projectId]
["production-project", projectId]
["production-publish-check", projectId]
["production-completeness", projectId]
["production-activity", projectId, limit, offset]
["production-draft", projectId, assetType, headingId ?? null]
["production-revisions", projectId, assetType, headingId ?? null]
```

Invalidate after mutations:

| Mutation | Invalidate |
| --- | --- |
| Create project | dashboard, candidates, projects |
| Patch project | dashboard, projects, project, workspace, activity |
| Save draft | workspace, draft, revisions, publish-check, completeness, activity, dashboard |
| Delete draft | workspace, draft, publish-check, completeness, activity, dashboard |
| Restore revision | workspace, draft, revisions, publish-check, completeness, activity, dashboard |
| Review | workspace, draft, publish-check, completeness, activity, dashboard, projects |
| Publish | dashboard, projects, project, workspace, publish-check, activity |
| Unpublish | dashboard, projects, project, workspace, publish-check, activity |
| Final delete | dashboard, projects, project, workspace, publish-check, activity |

Polling:

- Not required for MVP.
- Manual refresh is enough for a one-editor setup.
- If desired, poll dashboard/activity every 60 seconds.

## Validation Rules For Forms

Mirror backend validation in frontend for better UX:

| Form | Field | Rule |
| --- | --- | --- |
| Create project | `book_id` | required, min 1 |
| Create project | `lang` | `id` or `en` |
| Create project | `priority` | min 0 |
| Create project | `owner_id` | UUID if present |
| Create project | `notes` | max 10000 |
| Metadata | `display_title` | required, max 500 |
| Metadata | `bibliography` | max 10000 |
| Metadata | `hint` | max 10000 |
| Metadata | `description` | max 10000 |
| Metadata | `source` | max 255 |
| Author | `name` | required, max 500 |
| Author | `biography` | max 20000 |
| Author | `death_text` | max 255 |
| Author | `source` | max 255 |
| Category | `name` | required, max 500 |
| Category | `source` | max 255 |
| Translation | `title` | max 1000 |
| Translation | `content` | required |
| Translation | `source` | max 255 |
| Summary | `summary` | required, max 20000 |
| Summary | `source` | max 255 |
| Audio | `url` | required URL, max 2000 |
| Audio | `narrator` | max 255 |
| Audio | `duration_seconds` | min 0 |
| Audio | `mime_type` | max 255 |
| Review | `note` | max 2000 |
| Final delete | `reason` | max 2000 |

## UX Copy And Labels

Recommended labels:

| Backend value | UI label |
| --- | --- |
| `candidate` | Candidate |
| `drafting` | Drafting |
| `in_review` | In Review |
| `ready` | Ready |
| `published` | Published |
| `archived` | Archived |
| `hidden` | Hidden |
| `draft` | Draft |
| `pending_review` | Pending Review |
| `approved` | Approved |
| `rejected` | Rejected |
| `book_metadata` | Book Metadata |
| `author_metadata` | Author |
| `category_metadata` | Category |
| `section_translation` | Translation |
| `heading_summary` | Summary |
| `section_audio` | Audio |

Use concise toasts:

- Draft saved
- Draft deleted
- Revision restored
- Review submitted
- Draft approved
- Draft rejected
- Project published
- Project unpublished
- Publish blocked

## Public Reader Verification

After publish, the public reader should show translated assets:

```http
GET /v1/books/{book_id}?lang=id
GET /v1/books/{book_id}/toc?lang=id&include_audio=true
GET /v1/books/{book_id}/toc/{heading_id}/read?lang=id
GET /v1/books/{book_id}/toc/{heading_id}/playlist?lang=id
```

Before publish or after unpublish:

- Public reader falls back safely to source/Arabic behavior.
- Translation/audio/summary final assets for that book+lang are not exposed as published content.

Use this as a QA step, not as the editor source of truth. The editor source of truth is `workspace` plus draft endpoints.

## Implementation Order

Recommended build order for the first frontend:

1. Auth login and role gate.
2. Production dashboard with language selector.
3. Queue list with `needs_work`, `ready_to_publish`, `published`.
4. Raw picker and create project.
5. Workspace shell with TOC and status chips.
6. Metadata, author, category draft forms.
7. Translation and summary draft forms per heading.
8. Audio URL draft form per heading.
9. Review actions.
10. Publish-check panel.
11. Admin publish/unpublish.
12. Revision list and restore.
13. Project/global activity.
14. Feedback review tab.
15. Source edit tab if needed.

This order gives a usable vertical slice by step 8, then adds safety and admin operations.

## MVP Acceptance Checklist

Auth and routing:

- User can log in.
- App fetches `/user/profile`.
- `user` cannot open editorial module.
- `editor` can open production module but cannot see publish buttons.
- `admin` can see publish/admin actions.

Dashboard:

- Language switch updates counts.
- Cards link to correct filtered queues.
- Recent events render.

Raw picker:

- Search works.
- `has_content` and `unstarted` filters work.
- Existing project state appears when not filtering unstarted.
- Create project navigates to workspace.
- Duplicate project error is handled.

Queue:

- Needs Work tab uses `needs_work=true`.
- Ready tab uses `ready_to_publish=true`.
- Published tab uses `publication_status=published`.
- Pagination works.

Workspace:

- Project header renders.
- TOC headings render in order with indentation from `depth`.
- Metadata/author/category statuses render.
- Translation/summary/audio statuses render per heading.
- Missing draft GET shows empty form, not fatal error.

Drafts:

- Metadata save works.
- Author save works when author exists.
- Category save works when category exists.
- Translation save works per heading.
- Summary save works per heading.
- Audio URL save works per heading.
- Delete draft clears UI.
- Save creates a visible revision.

Review:

- Submit sets pending review.
- Approve sets approved.
- Reject sets rejected and keeps note.
- Save after rejection resets to draft.

Publish:

- Publish-check shows blockers.
- Missing item links jump to correct editor.
- Admin publish is disabled until `can_publish=true`.
- Publish succeeds when complete.
- Public reader shows translated content after publish.
- Unpublish hides translated publication.

Revisions:

- Revisions list newest first.
- Restore requires confirmation.
- Restore changes current draft.
- Restore creates a new revision.

Activity:

- Project activity shows saves/reviews/publish.
- Global activity shows cross-project events.

## Known Non-MVP Gaps

These are intentionally not blockers for the first frontend:

- Audio binary upload/presigned storage. Current MVP uses audio URL drafts.
- Production import/export backup. Current safety comes from draft revisions.
- Assignment/workload management. Current team is small, so priority and owner fields are enough.
- Self-review restriction. Backend v1 allows any editor/admin to approve.
- Real-time collaboration. Manual refresh is enough for one editor.

## Useful API Summary

Editor/admin:

```http
GET    /v1/editorial/production-dashboard
GET    /v1/editorial/production-activity
GET    /v1/editorial/production-candidates
POST   /v1/editorial/production-projects
GET    /v1/editorial/production-projects
GET    /v1/editorial/production-projects/{id}
PATCH  /v1/editorial/production-projects/{id}
GET    /v1/editorial/production-projects/{id}/workspace
GET    /v1/editorial/production-projects/{id}/completeness
GET    /v1/editorial/production-projects/{id}/publish-check
GET    /v1/editorial/production-projects/{id}/activity
GET    /v1/editorial/production-projects/{id}/draft-revisions
POST   /v1/editorial/production-projects/{id}/draft-revisions/{revision_id}/restore
GET    /v1/editorial/production-projects/{id}/metadata-draft
PUT    /v1/editorial/production-projects/{id}/metadata-draft
DELETE /v1/editorial/production-projects/{id}/metadata-draft
GET    /v1/editorial/production-projects/{id}/author-draft
PUT    /v1/editorial/production-projects/{id}/author-draft
DELETE /v1/editorial/production-projects/{id}/author-draft
GET    /v1/editorial/production-projects/{id}/category-draft
PUT    /v1/editorial/production-projects/{id}/category-draft
DELETE /v1/editorial/production-projects/{id}/category-draft
GET    /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft
PUT    /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft
GET    /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft
PUT    /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft
GET    /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft
PUT    /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft
POST   /v1/editorial/production-projects/{id}/review
```

Admin only:

```http
POST   /v1/editorial/production-projects/{id}/publish
POST   /v1/editorial/production-projects/{id}/unpublish
DELETE /v1/editorial/production-projects/{id}/final-assets/{asset_type}
DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/final-assets/{asset_type}
GET    /v1/admin/users
GET    /v1/admin/users?role=editor
GET    /v1/admin/users/{id}
GET    /v1/admin/users/{id}/activity
PATCH  /v1/admin/users/role
```

Related editorial:

```http
GET  /v1/editorial/translation-feedbacks
GET  /v1/editorial/translation-feedbacks/summary
POST /v1/editorial/translation-feedbacks/{id}/resolve
POST /v1/editorial/translation-feedbacks/{id}/reopen
GET  /v1/editorial/reader/missing-assets
GET  /v1/editorial/quran/missing-assets
```

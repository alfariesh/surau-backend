# Realtime Collaborative Editing (Yjs + Hocuspocus)

Google-Docs-style collaboration for the kitab **page editor**. Multiple
editors share one CRDT document per page; merges are automatic and conflict
free; presence (remote cursors) comes built in.

```
Browser A ─┐                       ┌────────────────────┐
Browser B ─┼─ wss /collab ────────▶│   collab-server    │── binary Yjs state ──▶ collab_documents (PG)
Browser C ─┘   (Yjs sync +         │ (Node/Hocuspocus)  │
               awareness)          └─────────┬──────────┘
                                             │ debounced HTML sync
                                             ▼ X-Internal-Token
                                   ┌────────────────────┐
                                   │  Go app /internal  │── sanitize → book_page_edits (draft)
                                   │                    │           → admin_audit_logs
                                   │                    │           → book_source_edit_revisions (origin=collab)
                                   └────────────────────┘
```

**Design rule: Go stays the single write path for editorial drafts.** The
collab-server only owns `collab_documents` (binary CRDT state). Draft HTML
always flows through `PUT /internal/collab/...` so sanitization
(`readerutil.NormalizeContent`), `content_text` extraction, audit logs and
revision history behave identically for REST and collaborative editing.
Publishing is untouched: the existing admin endpoint
`POST /v1/editorial/books/{book_id}/pages/{page_id}/publish` (If-Match
required) promotes the latest draft, which the collab flush keeps current.

## Components

| Piece | Where | Role |
|---|---|---|
| collab-server | `collab-server/` | Hocuspocus **v4** websocket server (TipTap **v3** schema), port 8090 |
| Yjs persistence | `collab_documents` table | Binary document state, survives restarts |
| Auth bridge | `GET /v1/auth/introspect` | Validates the user's access token + live session, returns role |
| Draft bridge | `GET/PUT /internal/collab/books/{id}/pages/{id}/draft` | Seed source / merged-HTML sink, service-token guarded |
| Demo client | `collab-server/demo/index.html` | Verification tool (two-browser test), not the product UI |

## Document naming

`page:{book_id}:{page_id}` — one document per kitab page draft.
`production-section:{project_id}:{heading_id}` is reserved for the translation
workspace (not implemented; the parser rejects it until then).

## Lifecycle

1. **Connect** — client opens `wss://<host>/collab` with the document name and
   the user's normal **access token**. `onAuthenticate` calls
   `/v1/auth/introspect`; only `editor`/`admin` roles may connect. Because
   introspection hits the Go session layer, revoked sessions and
   `token_version` bumps are enforced — no JWT secret is shared with Node.
2. **Seed** — first load with no stored Yjs state fetches the current draft
   (or raw page) from the internal API and converts HTML → ProseMirror →
   Y.Doc. The pre-collab draft is already protected as a source-edit revision.
3. **Edit** — Yjs handles merging and awareness. Nothing leaves the process
   until the debounce window (3s, max 10s) closes.
4. **Persist** — on each debounced store: (a) binary state upserts into
   `collab_documents`; (b) the Y.Doc renders to HTML and `PUT`s into the Go
   draft pipeline with `origin=collab`, actor = last mutating editor,
   contributors = everyone connected. Go-side failures retry with backoff; the
   binary state is never lost, so the next flush self-heals.
5. **Token expiry** — access tokens live ~15m. The server closes the
   connection shortly after `exp` (+30s grace); the provider reconnects with a
   fresh token from its `token` callback. Local Yjs state survives, so the
   editor never loses work.
6. **Publish** — unchanged admin flow. The UI should wait for
   `provider.hasUnsyncedChanges === false` plus one debounce window before
   calling publish, so the latest keystrokes are in the draft row.

## Frontend contract (build the editor against this)

Versions: `@hocuspocus/provider` **^4.1.1** (must match the server major) and
TipTap **^3.26** packages.

```ts
import { HocuspocusProvider } from "@hocuspocus/provider";
import { Editor } from "@tiptap/core";
import Collaboration from "@tiptap/extension-collaboration";
import CollaborationCaret from "@tiptap/extension-collaboration-caret"; // v3 rename of CollaborationCursor
// surauExtensions: copy the exact list from collab-server/src/schema.ts —
// StarterKit({undoRedo:false, strike:false, link:false}) (v3 bundles Link+
// Underline; link is replaced by SurauLink which adds a[name] and drops
// rel/target), Sub/Superscript, the Surau* table overrides (clean renders
// without style/colgroup/colspan="1" noise), Div, Section, DefinitionList/
// Term/Description, Small, Cite, SpanMark, GlobalAttributes(class,
// data-marker, data-type, dir, id, lang, title).

const provider = new HocuspocusProvider({
  url: "wss://<host>/collab",
  name: `page:${bookId}:${pageId}`,
  token: async () => getFreshAccessToken(), // re-invoked on every (re)connect
});

const editor = new Editor({
  extensions: [
    ...surauExtensions,
    Collaboration.configure({ document: provider.document }), // replaces undo/redo
    CollaborationCaret.configure({ provider, user: { name, color } }),
  ],
});
```

Rules:

- **Schema parity is non-negotiable.** Client and server must configure the
  same extensions (`collab-server/src/schema.ts` is authoritative). A mismatch
  corrupts how documents render for everyone.
- **Never enable TipTap/ProseMirror history** (`undoRedo: false` in StarterKit
  v3) — y-prosemirror provides collaborative undo (`Collaboration` does this
  for you).
- The `token` option must be a callback returning a fresh access token; the
  provider calls it on each reconnect (including the post-expiry one).
- REST `PUT /v1/editorial/books/{id}/pages/{id}/draft` still exists for
  scripts/fallback and now **requires If-Match** — a stale ETag gets `412`, a
  missing header `428`, `If-Match: *` is the explicit last-write-wins escape
  hatch. While a page is being edited collaboratively, treat the collab
  document as the source of truth and do not write via REST.

## Known round-trip normalizations

Legacy markup converges to a stable semantic equivalent on the **first** collab
save of a page (verified by `collab-server/test/roundtrip.test.ts`; the
pre-collab content is kept as a revision):

- `<b>`→`<strong>`, `<i>`→`<em>`
- bare text inside `<div>`/`<section>`/`<blockquote>`/`<dd>` gains a `<p>` wrapper
- `<thead>`/`<tfoot>` wrappers flatten into `<tbody>` (rows and `<th>` survive)
- `<s>` (not in the sanitizer allowlist) is dropped
- tables render without TipTap v3's injected `style`/`colgroup`/`colspan="1"`
  noise (the Surau* table overrides emit the sanitizer-clean shape directly)

After the first pass the output is a fixpoint — subsequent saves only change
what users actually edited.

## Operations

- **Env (Go app):** `COLLAB_ENABLED=true`; `COLLAB_SERVICE_TOKEN` hanya T1
  compatibility satu rilis yang di-hash sekali ke registry A-2.
- **Env (collab-server):** `COLLAB_PORT`, `COLLAB_PG_URL_FILE`,
  `COLLAB_GO_API_URL`, `COLLAB_SERVICE_TOKEN_FILE`,
  `COLLAB_DEBOUNCE_MS=3000`,
  `COLLAB_DEBOUNCE_MAX_MS=10000`, `COLLAB_TOKEN_GRACE_MS=30000`,
  `COLLAB_LOG_LEVEL=info`.
- **Production deploy:** the collab service is opt-in behind a compose profile.
  Set `COLLAB_ENABLED=true`, mount root-owned secret files, then
  `docker compose --env-file .env.production -f docker-compose.prod.yml
  --profile collab up -d --build app collab` (the deploy-vps workflow does this
  automatically when `COLLAB_ENABLED=true`).
- **Routing (dev):** nginx proxies `/collab` with websocket upgrade
  (`proxy_read_timeout 1h`) and hard-404s `/internal` — the internal API is
  for the private network only.
- **Routing (prod, Caddy):** mirror both rules. Caddy upgrades websockets
  automatically:

  ```caddyfile
  api.example.org {
      @internal path /internal/*
      respond @internal 404

      handle_path /collab* {
          reverse_proxy 127.0.0.1:8090
      }

      reverse_proxy 127.0.0.1:8080
  }
  ```

  `:8080` adalah target bootstrap. Workflow deploy memvalidasi lalu menulis
  ulang tepat baris API terakhir ke slot aktif `:18080`/`:18081`; route collab
  `:8090` tidak diubah.

  Note `handle_path` strips the `/collab` prefix before proxying — the
  collab-server serves the websocket at its root. Clients connect to
  `wss://api.example.org/collab`.
- **Health:** `GET :8090/healthz` (200 = Postgres + scoped `collab-server`
  `whoami` OK).
- **Rotation:** token and DSN files reload tanpa restart. Kandidat token harus
  lolos `whoami`; kandidat pool harus lolos permission-probe `collab_documents`.
  Kandidat gagal tidak mengganti credential aktif. Prosedur A/B dan bukti
  container tidak restart ada di
  [`service-identity-rotation.md`](service-identity-rotation.md).
- **Crash safety:** verified — killing the collab container mid-edit loses at
  most the current debounce window; on restart, documents reload from
  `collab_documents` (and at final-connection close Hocuspocus flushes).
- **Scaling:** one instance handles thousands of concurrent connections. The
  horizontal path is `@hocuspocus/extension-redis` (instances sync via Redis
  pub/sub, no sticky sessions needed) — add a Redis service and the extension
  when needed; nothing else changes. Hocuspocus v4 additionally brings store
  retry on failure, ordered message processing per connection, and graceful
  shutdown that flushes pending debounced stores.

## Local verification

```bash
docker compose up --build -d db app collab
cd collab-server && npm test                      # schema round-trip + docname
TOKEN=<editor JWT> node scripts/e2e.mjs           # 2 clients converge → draft row lands
open demo/index.html                              # interactive two-browser check
```

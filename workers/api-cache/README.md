# Surau API Cache Worker

Cloudflare Worker proxy for Surau API traffic. The Go backend remains the source of truth; this Worker adds a local Cache API layer plus Workers KV as a persistent global cache for safe public `GET` responses, and edge rate limits for expensive public endpoints.

## Setup

Create a KV namespace and copy the namespace ID into `wrangler.jsonc`.

```sh
npx wrangler kv namespace create PUBLIC_API_CACHE
```

Set production values in `wrangler.jsonc` before deploy, especially `ORIGIN_BASE_URL` and the `PUBLIC_API_CACHE` namespace ID.

The production route is `api.surau.org/*`. Point `origin-api.surau.org` at the VPS/backend and make sure that hostname is not routed back through this Worker.

## Commands

```sh
npm install
npm test
npm run typecheck
npm run deploy
```

## Cache Policy

- Fresh TTL: `300s`
- Stale TTL: `86400s`
- Bypasses authenticated requests, cookies, searches, admin/editorial/auth/user paths, non-GET methods, non-JSON responses, non-200 responses, and large responses above `MAX_CACHE_BYTES`.
- Always bypasses `/v1/books*`, `/v1/anchors*`, and `/v1/cross-references*`. These license-sensitive routes must reach the backend visibility gate on every request and retain the origin's `Cache-Control: public, max-age=0, must-revalidate`, ETag, and Last-Modified headers.
- Uses `CACHE_VERSION` for mass invalidation without `KV.list()` or bulk deletes.

## Edge Rate Limits

- `POST /v1/books/:book_id/rag`: `10/min`
- RAG daily quota: valid JWT users get `50/day`; guests and invalid Bearer tokens get `100/IP/day`.
- Auth/email-sensitive POST endpoints: `10/min`
- `POST /v1/books/:book_id/toc/:heading_id/translation-feedback`: `30/min`
- Public search GET endpoints with `q`: `60/min`
- Durable Object `EDGE_RATE_LIMITER` enforces the window before origin fetch; Workers Rate Limiting bindings remain as an additional Cloudflare-native guard.
- Blocked requests return `429`, `Retry-After: 60`, `X-Surau-Cache: BYPASS`, and `X-Surau-RateLimit: BLOCKED`.
- Daily quota blocks also include `X-Surau-RateLimit-Policy: rag-daily` and reset at UTC midnight.

## JWT Verification and Key Rotation

Set `JWT_KEYSET` as a Worker secret with the same JSON keyset installed on the backend:

```sh
npx wrangler secret put JWT_KEYSET
```

The value uses the strict version-1 shape below. Key IDs are 1–64 characters from
`A-Z`, `a-z`, `0-9`, `_`, or `-`; every secret must be at least 32 bytes.

```json
{
  "version": 1,
  "active_kid": "2026-07-new",
  "legacy_kid": "2026-01-old",
  "keys": {
    "2026-01-old": "<old-secret-at-least-32-bytes>",
    "2026-07-new": "<new-secret-at-least-32-bytes>"
  }
}
```

- During overlap, deploy the Worker keyset containing old+new keys before the backend starts issuing `kid=new` tokens. Tokens with either exact `kid` remain user-identified; living tokens without `kid` use only `legacy_kid`.
- At retirement, remove `legacy_kid` and the old key only after the backend's longest-lived old access token has expired. Old and no-`kid` tokens then safely receive guest/IP quota.
- An unknown/non-string `kid`, wrong signature, or malformed configured keyset fails closed to guest/IP quota. If `JWT_KEYSET` exists but is invalid, the Worker never falls back to `JWT_SECRET`.
- `JWT_SECRET` remains a temporary compatibility fallback only when `JWT_KEYSET` is completely absent. Delete it after all environments have installed a valid keyset.

Keep keyset values in Wrangler secrets, never in `wrangler.jsonc`, source control, logs, or drill artifacts. `active_kid` is retained in the shared schema for backend/Worker parity; this Worker verifies tokens but never issues them.

RAG responses also carry the additive operational header
`X-Surau-JWT-Identity: user|guest`. The rotation drill uses it to prove the
edge verifier changed in the safe order. Product clients must ignore this
header; authorization remains the backend's responsibility and the header is
not a role/profile contract.

## AI Gateway

The Go backend is already OpenAI-compatible and calls `{RAG_LLM_BASE_URL}/chat/completions`. To observe RAG LLM cost and latency through Cloudflare AI Gateway, create a gateway and point `RAG_LLM_BASE_URL` at the provider path:

- OpenAI provider: `https://gateway.ai.cloudflare.com/v1/{account_id}/{gateway_id}/openai`
- Custom OpenAI-compatible provider: `https://gateway.ai.cloudflare.com/v1/{account_id}/{gateway_id}/{custom_provider_slug}/v1`

Keep `RAG_LLM_API_KEY` as the upstream provider key unless credentials are stored in Cloudflare provider configs.

## Production Smoke

```sh
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/quran/surahs?lang=id'
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/quran/surahs?lang=id'
curl -sS -D - -o /dev/null -H 'Authorization: Bearer test' 'https://api.surau.org/v1/quran/surahs?lang=id'
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/quran/search?q=rahman'
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/books/797?lang=id'
curl -sS -D - -o /dev/null -X POST 'https://api.surau.org/v1/books/797/rag?lang=id' \
  -H 'Content-Type: application/json' \
  --data '{"question":"Apa isi bab ini?"}'
```

Expect the first public request to return `X-Surau-Cache: MISS`, the second to return `L1-HIT` or `KV-HIT`, and authenticated/search requests to return `BYPASS`.
The kitab request must return `X-Surau-Cache: BYPASS` and `Cache-Control: public, max-age=0, must-revalidate` on every call.
Expensive endpoints that pass the limiter include `X-Surau-RateLimit: PASS`; blocked requests return `429`.

## Quran Search Browser Access

`GET /v1/quran/search` may be called directly by Surau's browser clients so the edge limiter keys
requests by the reader's IP instead of a shared frontend-server IP. The Worker grants CORS only to
the exact origins listed in `QURAN_SEARCH_ALLOWED_ORIGINS` (comma-separated), never sends
`Access-Control-Allow-Credentials`, and handles `OPTIONS` without forwarding it to the origin.
Production is restricted to `https://surau.org` and `https://www.surau.org`; staging must configure
its own explicit allowlist.

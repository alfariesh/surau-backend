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

Set `JWT_SECRET` as a Worker secret matching the backend JWT secret. Without it, the Worker safely treats all RAG traffic as guest/IP quota.

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

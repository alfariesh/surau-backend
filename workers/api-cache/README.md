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
- Uses `CACHE_VERSION` for mass invalidation without `KV.list()` or bulk deletes.

## Edge Rate Limits

- `POST /v1/books/:book_id/rag`: `10/min`
- Auth/email-sensitive POST endpoints: `10/min`
- `POST /v1/books/:book_id/toc/:heading_id/translation-feedback`: `30/min`
- Public search GET endpoints with `q`: `60/min`
- Durable Object `EDGE_RATE_LIMITER` enforces the window before origin fetch; Workers Rate Limiting bindings remain as an additional Cloudflare-native guard.
- Blocked requests return `429`, `Retry-After: 60`, `X-Surau-Cache: BYPASS`, and `X-Surau-RateLimit: BLOCKED`.

## Production Smoke

```sh
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/quran/surahs?lang=id'
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/quran/surahs?lang=id'
curl -sS -D - -o /dev/null -H 'Authorization: Bearer test' 'https://api.surau.org/v1/quran/surahs?lang=id'
curl -sS -D - -o /dev/null 'https://api.surau.org/v1/quran/search?q=rahman'
curl -sS -D - -o /dev/null -X POST 'https://api.surau.org/v1/books/797/rag?lang=id' \
  -H 'Content-Type: application/json' \
  --data '{"question":"Apa isi bab ini?"}'
```

Expect the first public request to return `X-Surau-Cache: MISS`, the second to return `L1-HIT` or `KV-HIT`, and authenticated/search requests to return `BYPASS`.
Expensive endpoints that pass the limiter include `X-Surau-RateLimit: PASS`; blocked requests return `429`.

export interface Env {
  PUBLIC_API_CACHE: KVNamespace;
  EDGE_RATE_LIMITER: DurableObjectNamespace;
  RAG_RATE_LIMITER: RateLimit;
  AUTH_EDGE_RATE_LIMITER: RateLimit;
  FEEDBACK_RATE_LIMITER: RateLimit;
  SEARCH_RATE_LIMITER: RateLimit;
  ORIGIN_BASE_URL: string;
  CACHE_VERSION?: string;
  CACHE_FRESH_TTL_SECONDS?: string;
  CACHE_STALE_TTL_SECONDS?: string;
  MAX_CACHE_BYTES?: string;
  RAG_DAILY_QUOTA_ENABLED?: string;
  RAG_DAILY_GUEST_LIMIT?: string;
  RAG_DAILY_USER_LIMIT?: string;
  JWT_SECRET?: string;
  JWT_ISSUER?: string;
  JWT_AUDIENCE?: string;
}

export type CacheStatus = "BYPASS" | "L1-HIT" | "KV-HIT" | "MISS" | "STALE";
type RateLimitStatus = "PASS" | "BLOCKED";
type RateLimitBindingName =
  | "RAG_RATE_LIMITER"
  | "AUTH_EDGE_RATE_LIMITER"
  | "FEEDBACK_RATE_LIMITER"
  | "SEARCH_RATE_LIMITER";

interface CacheConfig {
  version: string;
  freshTtlSeconds: number;
  staleTtlSeconds: number;
  maxCacheBytes: number;
  originBaseURL: URL;
}

interface CacheEntry {
  status: number;
  headers: Record<string, string>;
  body: string;
  cachedAt: number;
  freshTtlSeconds: number;
  staleTtlSeconds: number;
}

interface CacheDecision {
  cacheable: boolean;
  reason: string;
}

interface EdgeRateLimitDecision {
  bindingName: RateLimitBindingName;
  group: string;
  limit: number;
  retryAfterSeconds: number;
}

interface RagDailyQuotaDecision {
  key: string;
  limit: number;
  retryAfterSeconds: number;
}

interface EdgeRateLimitCheck {
  blocked: boolean;
  decision: EdgeRateLimitDecision;
}

interface RagDailyQuotaCheck {
  blocked: boolean;
  decision: RagDailyQuotaDecision;
}

interface EdgeRateLimiterRequest {
  limit: number;
  resetAtMilliseconds?: number;
  windowSeconds: number;
}

interface EdgeRateLimiterOutcome {
  success: boolean;
  retryAfterSeconds: number;
}

const DEFAULT_CACHE_VERSION = "1";
const DEFAULT_FRESH_TTL_SECONDS = 300;
const DEFAULT_STALE_TTL_SECONDS = 86400;
const DEFAULT_MAX_CACHE_BYTES = 2_000_000;
const DEFAULT_RAG_DAILY_GUEST_LIMIT = 100;
const DEFAULT_RAG_DAILY_USER_LIMIT = 50;
const DEFAULT_JWT_ISSUER = "surau-backend";
const DEFAULT_JWT_AUDIENCE = "surau-api";
const CACHE_STATUS_HEADER = "X-Surau-Cache";
const RATE_LIMIT_STATUS_HEADER = "X-Surau-RateLimit";
const RATE_LIMIT_POLICY_HEADER = "X-Surau-RateLimit-Policy";
const RATE_LIMIT_RETRY_AFTER_SECONDS = 60;
const ORIGIN_LOOP_STATUS = 508;

const BYPASS_PREFIXES = [
  "/metrics",
  "/healthz",
  "/readyz",
  "/swagger",
  "/v1/auth",
  "/v1/me",
  "/v1/user",
  "/v1/editorial",
  "/v1/admin",
  "/v1/email"
];

const TRACKING_PARAMS = new Set(["fbclid", "gclid", "gbraid", "wbraid", "mc_cid", "mc_eid"]);

const AUTH_EDGE_RATE_LIMIT_GROUPS: Record<string, string> = {
  "/v1/auth/register": "auth:register",
  "/v1/auth/resend-verification": "auth:resend-verification",
  "/v1/auth/forgot-password": "auth:forgot-password",
  "/v1/auth/reset-password": "auth:reset-password",
  "/v1/auth/change-email/request": "auth:change-email",
  "/v1/auth/change-email/verify": "auth:change-email",
  "/v1/auth/delete-account": "auth:delete-account"
};

const PRESERVED_RESPONSE_HEADERS = [
  "cache-control",
  "content-type",
  "etag",
  "last-modified",
  "vary"
];

const textEncoder = new TextEncoder();

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    return handleRequest(request, env, ctx);
  }
} satisfies ExportedHandler<Env>;

export class EdgeRateLimiter {
  constructor(private readonly state: DurableObjectState) {
    this.state.blockConcurrencyWhile(async () => {
      this.state.storage.sql.exec(`
        CREATE TABLE IF NOT EXISTS rate_limit_counter (
          id INTEGER PRIMARY KEY CHECK (id = 1),
          count INTEGER NOT NULL,
          window_started_at INTEGER NOT NULL
        )
      `);
    });
  }

  async fetch(request: Request): Promise<Response> {
    if (request.method !== "POST") {
      return new Response("method not allowed", { status: 405 });
    }

    const input = (await request.json()) as Partial<EdgeRateLimiterRequest>;
    const limit = positiveInt(String(input.limit ?? ""), 1);
    const windowSeconds = positiveInt(String(input.windowSeconds ?? ""), RATE_LIMIT_RETRY_AFTER_SECONDS);
    const resetAtMilliseconds =
      typeof input.resetAtMilliseconds === "number" && Number.isFinite(input.resetAtMilliseconds)
        ? input.resetAtMilliseconds
        : Date.now() + windowSeconds * 1000;
    const outcome = await this.check(limit, resetAtMilliseconds);

    return new Response(JSON.stringify(outcome), {
      headers: {
        "Content-Type": "application/json; charset=utf-8"
      }
    });
  }

  async alarm(): Promise<void> {
    await this.state.storage.deleteAll();
  }

  private async check(limit: number, resetAtMilliseconds: number): Promise<EdgeRateLimiterOutcome> {
    const now = Date.now();
    const current = this.state.storage.sql
      .exec<{ count: number; window_started_at: number }>(
        "SELECT count, window_started_at FROM rate_limit_counter WHERE id = 1"
      )
      .toArray()[0];

    if (!current || now >= current.window_started_at) {
      this.state.storage.sql.exec(
        "INSERT OR REPLACE INTO rate_limit_counter (id, count, window_started_at) VALUES (1, 1, ?)",
        resetAtMilliseconds
      );
      await this.state.storage.setAlarm(resetAtMilliseconds + 1000);

      return {
        success: true,
        retryAfterSeconds: Math.max(1, Math.ceil((resetAtMilliseconds - now) / 1000))
      };
    }

    const count = current.count + 1;
    this.state.storage.sql.exec("UPDATE rate_limit_counter SET count = ? WHERE id = 1", count);
    await this.state.storage.setAlarm(current.window_started_at + 1000);

    return {
      success: count <= limit,
      retryAfterSeconds: Math.max(1, Math.ceil((current.window_started_at - now) / 1000))
    };
  }
}

export async function handleRequest(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
  const config = cacheConfig(env);
  const rateLimitCheck = await checkEdgeRateLimit(request, env);
  if (rateLimitCheck?.blocked) {
    return edgeRateLimitResponse(rateLimitCheck.decision);
  }

  const ragDailyQuotaCheck = await checkRagDailyQuota(request, env);
  if (ragDailyQuotaCheck?.blocked) {
    return ragDailyQuotaResponse(ragDailyQuotaCheck.decision);
  }

  const response = await handleCacheRequest(request, env, ctx, config);

  if (rateLimitCheck || ragDailyQuotaCheck) {
    return responseWithRateLimitStatus(response, "PASS");
  }

  return response;
}

async function handleCacheRequest(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
  config: CacheConfig
): Promise<Response> {
  const decision = cacheDecision(request);

  if (!decision.cacheable) {
    return fetchOrigin(request, config, "BYPASS");
  }

  const normalizedURL = normalizedCacheURL(request.url);
  const cacheRequest = new Request(normalizedURL.toString(), { method: "GET" });
  const cacheControl = publicCacheControl(config);
  const l1 = await caches.default.match(cacheRequest);

  if (l1) {
    return responseWithCacheStatus(l1, "L1-HIT", request, cacheControl);
  }

  const kvKey = cacheKey(normalizedURL, config.version);
  const kvEntry = await readKVEntry(env.PUBLIC_API_CACHE, kvKey);
  const now = Date.now();

  if (kvEntry) {
    if (entryFresh(kvEntry, now)) {
      const response = responseFromEntry(kvEntry);
      ctx.waitUntil(caches.default.put(cacheRequest, response.clone()));

      return responseWithCacheStatus(response, "KV-HIT", request, cacheControl);
    }

    if (entryStale(kvEntry, now)) {
      ctx.waitUntil(revalidate(request, env, config, normalizedURL, cacheRequest, kvKey));

      return responseWithCacheStatus(responseFromEntry(kvEntry), "STALE", request, cacheControl);
    }
  }

  return fetchOriginAndCache(request, env, config, normalizedURL, cacheRequest, kvKey);
}

export function edgeRateLimitDecision(request: Request): EdgeRateLimitDecision | null {
  const url = new URL(request.url);
  const path = stripTrailingSlash(url.pathname);
  const method = request.method.toUpperCase();

  if (isRagRequestPath(method, path)) {
    return edgeRateLimitDecisionFor("RAG_RATE_LIMITER", "rag");
  }

  if (method === "POST" && /^\/v1\/books\/\d+\/toc\/\d+\/translation-feedback$/.test(path)) {
    return edgeRateLimitDecisionFor("FEEDBACK_RATE_LIMITER", "feedback");
  }

  if (method === "POST" && AUTH_EDGE_RATE_LIMIT_GROUPS[path]) {
    return edgeRateLimitDecisionFor("AUTH_EDGE_RATE_LIMITER", AUTH_EDGE_RATE_LIMIT_GROUPS[path]);
  }

  if (
    method === "GET" &&
    (path === "/v1/quran/search" ||
      path.startsWith("/v1/quran/search/") ||
      (url.searchParams.has("q") && allowedPublicPath(url)))
  ) {
    return edgeRateLimitDecisionFor("SEARCH_RATE_LIMITER", "search");
  }

  return null;
}

export function isRagRequest(request: Request): boolean {
  const url = new URL(request.url);

  return isRagRequestPath(request.method.toUpperCase(), stripTrailingSlash(url.pathname));
}

export async function edgeRateLimitKey(request: Request, group: string): Promise<string> {
  const bearer = bearerToken(request.headers.get("authorization"));
  if (bearer) {
    return `${group}:bearer:${await sha256Hex(bearer)}`;
  }

  return `${group}:ip:${clientIP(request)}`;
}

export function cacheDecision(request: Request): CacheDecision {
  const url = new URL(request.url);

  if (request.method !== "GET") {
    return { cacheable: false, reason: "method" };
  }

  if (request.headers.has("authorization") || request.headers.has("cookie")) {
    return { cacheable: false, reason: "credentials" };
  }

  if (url.searchParams.has("q")) {
    return { cacheable: false, reason: "search_query" };
  }

  if (BYPASS_PREFIXES.some((prefix) => url.pathname === prefix || url.pathname.startsWith(`${prefix}/`))) {
    return { cacheable: false, reason: "protected_or_operational_path" };
  }

  if (url.pathname === "/v1/quran/search" || url.pathname.startsWith("/v1/quran/search/")) {
    return { cacheable: false, reason: "quran_search" };
  }

  if (!allowedPublicPath(url)) {
    return { cacheable: false, reason: "not_allowlisted" };
  }

  return { cacheable: true, reason: "allowlisted" };
}

export function cacheKey(url: URL, version: string): string {
  return `v1:${version}:GET:${url.pathname}?${url.searchParams.toString()}`;
}

export function normalizedCacheURL(input: string): URL {
  const url = new URL(input);
  const params: Array<[string, string]> = [];

  for (const [key, value] of url.searchParams) {
    if (isTrackingParam(key)) {
      continue;
    }

    params.push([key, value]);
  }

  params.sort(([leftKey, leftValue], [rightKey, rightValue]) => {
    if (leftKey === rightKey) {
      return leftValue.localeCompare(rightValue);
    }

    return leftKey.localeCompare(rightKey);
  });

  url.search = "";
  for (const [key, value] of params) {
    url.searchParams.append(key, value);
  }

  return url;
}

function allowedPublicPath(url: URL): boolean {
  const path = stripTrailingSlash(url.pathname);

  if (path === "/v1/categories" || path === "/v1/authors" || path === "/v1/books") {
    return true;
  }

  if (path === "/v1/quran/recitations" || path === "/v1/quran/translation-sources") {
    return true;
  }

  if (path === "/v1/quran/juz" || /^\/v1\/quran\/juz\/\d+\/ayahs$/.test(path)) {
    return true;
  }

  if (path === "/v1/quran/hizbs" || /^\/v1\/quran\/hizbs\/\d+\/ayahs$/.test(path)) {
    return true;
  }

  if (
    path === "/v1/quran/surahs" ||
    /^\/v1\/quran\/surahs\/\d+$/.test(path) ||
    /^\/v1\/quran\/surahs\/\d+\/audio$/.test(path) ||
    /^\/v1\/quran\/surahs\/\d+\/ayahs$/.test(path)
  ) {
    return true;
  }

  if (/^\/v1\/quran\/ayahs\/\d+:\d+$/.test(path)) {
    return true;
  }

  if (/^\/v1\/books\/\d+$/.test(path)) {
    return true;
  }

  if (/^\/v1\/books\/\d+\/pages(\/\d+)?$/.test(path)) {
    return true;
  }

  if (/^\/v1\/books\/\d+\/headings$/.test(path)) {
    return true;
  }

  if (/^\/v1\/books\/\d+\/sections\/\d+$/.test(path)) {
    return true;
  }

  if (
    /^\/v1\/books\/\d+\/toc$/.test(path) ||
    /^\/v1\/books\/\d+\/toc\/\d+\/read$/.test(path) ||
    /^\/v1\/books\/\d+\/toc\/\d+\/playlist$/.test(path)
  ) {
    return true;
  }

  if (/^\/v1\/books\/\d+\/quran-references$/.test(path)) {
    return !url.searchParams.has("status") || url.searchParams.get("status") === "approved";
  }

  return false;
}

async function fetchOriginAndCache(
  request: Request,
  env: Env,
  config: CacheConfig,
  normalizedURL: URL,
  cacheRequest: Request,
  kvKey: string
): Promise<Response> {
  const originResponse = await fetchOrigin(request, config, "MISS", normalizedURL);

  if (!cacheableOriginResponse(originResponse)) {
    return originResponse;
  }

  const body = await originResponse.clone().text();
  if (textEncoder.encode(body).byteLength > config.maxCacheBytes) {
    return responseWithCacheStatus(new Response(body, originResponse), "MISS", request);
  }

  const entry = cacheEntryFromResponse(originResponse, body, config);
  await writeCaches(env.PUBLIC_API_CACHE, kvKey, cacheRequest, entry);

  return responseWithCacheStatus(responseFromEntry(entry), "MISS", request, publicCacheControl(config));
}

async function revalidate(
  request: Request,
  env: Env,
  config: CacheConfig,
  normalizedURL: URL,
  cacheRequest: Request,
  kvKey: string
): Promise<void> {
  try {
    const originResponse = await fetchOrigin(request, config, "MISS", normalizedURL);
    if (!cacheableOriginResponse(originResponse)) {
      return;
    }

    const body = await originResponse.clone().text();
    if (textEncoder.encode(body).byteLength > config.maxCacheBytes) {
      return;
    }

    await writeCaches(
      env.PUBLIC_API_CACHE,
      kvKey,
      cacheRequest,
      cacheEntryFromResponse(originResponse, body, config)
    );
  } catch (error) {
    console.warn("surau api cache revalidation failed", error);
  }
}

async function fetchOrigin(
  request: Request,
  config: CacheConfig,
  cacheStatus: CacheStatus,
  normalizedURL = normalizedCacheURL(request.url)
): Promise<Response> {
  if (config.originBaseURL.host === new URL(request.url).host) {
    return new Response("origin loop detected", {
      status: ORIGIN_LOOP_STATUS,
      headers: {
        [CACHE_STATUS_HEADER]: "BYPASS",
        "Cache-Control": "no-store",
        "Content-Type": "text/plain; charset=utf-8"
      }
    });
  }

  const originURL = new URL(normalizedURL.pathname + normalizedURL.search, config.originBaseURL);
  const headers = new Headers(request.headers);
  headers.delete("host");
  headers.set("X-Forwarded-Host", new URL(request.url).host);
  headers.set("X-Surau-Worker", "api-cache");

  const originRequestInit: RequestInit & { duplex?: "half" } = {
    headers,
    method: request.method,
    redirect: "manual"
  };

  if (request.body && request.method !== "GET" && request.method !== "HEAD") {
    originRequestInit.body = request.body;
    originRequestInit.duplex = "half";
  }

  const response = await fetch(new Request(originURL.toString(), originRequestInit));

  return responseWithCacheStatus(response, cacheStatus, request);
}

async function checkEdgeRateLimit(request: Request, env: Env): Promise<EdgeRateLimitCheck | null> {
  const decision = edgeRateLimitDecision(request);
  if (!decision) {
    return null;
  }

  try {
    const limiter = env[decision.bindingName];
    const key = await edgeRateLimitKey(request, decision.group);
    const durableOutcome = await checkDurableEdgeRateLimit(env.EDGE_RATE_LIMITER, key, decision);
    if (durableOutcome && !durableOutcome.success) {
      return {
        blocked: true,
        decision: {
          ...decision,
          retryAfterSeconds: durableOutcome.retryAfterSeconds
        }
      };
    }

    const outcome = await limiter.limit({ key });

    return {
      blocked: !outcome.success,
      decision
    };
  } catch (error) {
    console.warn("surau edge rate limit check failed open", error);

    return {
      blocked: false,
      decision
    };
  }
}

async function checkRagDailyQuota(request: Request, env: Env): Promise<RagDailyQuotaCheck | null> {
  if (!isRagRequest(request) || !envFlag(env.RAG_DAILY_QUOTA_ENABLED, true)) {
    return null;
  }

  try {
    const decision = await ragDailyQuotaDecision(request, env);
    const outcome = await checkDurableEdgeRateLimit(env.EDGE_RATE_LIMITER, decision.key, {
      bindingName: "RAG_RATE_LIMITER",
      group: "rag-daily",
      limit: decision.limit,
      retryAfterSeconds: decision.retryAfterSeconds
    });

    return {
      blocked: outcome ? !outcome.success : false,
      decision: {
        ...decision,
        retryAfterSeconds: outcome?.retryAfterSeconds ?? decision.retryAfterSeconds
      }
    };
  } catch (error) {
    console.warn("surau rag daily quota check failed open", error);

    return null;
  }
}

async function ragDailyQuotaDecision(request: Request, env: Env): Promise<RagDailyQuotaDecision> {
  const now = new Date();
  const dateKey = utcDateKey(now);
  const retryAfterSeconds = secondsUntilNextUTCMidnight(now);
  const userID = await verifiedJWTSubject(request, env);

  if (userID) {
    return {
      key: `rag-daily:user:${userID}:${dateKey}`,
      limit: positiveInt(env.RAG_DAILY_USER_LIMIT, DEFAULT_RAG_DAILY_USER_LIMIT),
      retryAfterSeconds
    };
  }

  return {
    key: `rag-daily:ip:${clientIP(request)}:${dateKey}`,
    limit: positiveInt(env.RAG_DAILY_GUEST_LIMIT, DEFAULT_RAG_DAILY_GUEST_LIMIT),
    retryAfterSeconds
  };
}

async function checkDurableEdgeRateLimit(
  namespace: DurableObjectNamespace,
  key: string,
  decision: EdgeRateLimitDecision
): Promise<EdgeRateLimiterOutcome | null> {
  const id = namespace.idFromName(key);
  const stub = namespace.get(id);
  const response = await stub.fetch("https://surau.internal/rate-limit", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      limit: decision.limit,
      resetAtMilliseconds: Date.now() + decision.retryAfterSeconds * 1000,
      windowSeconds: decision.retryAfterSeconds
    })
  });

  if (!response.ok) {
    return null;
  }

  const outcome = (await response.json()) as Partial<EdgeRateLimiterOutcome>;
  if (typeof outcome.success !== "boolean" || typeof outcome.retryAfterSeconds !== "number") {
    return null;
  }

  return {
    success: outcome.success,
    retryAfterSeconds: positiveInt(outcome.retryAfterSeconds.toString(), decision.retryAfterSeconds)
  };
}

function edgeRateLimitDecisionFor(bindingName: RateLimitBindingName, group: string): EdgeRateLimitDecision {
  return {
    bindingName,
    group,
    limit: edgeRateLimitLimit(bindingName),
    retryAfterSeconds: RATE_LIMIT_RETRY_AFTER_SECONDS
  };
}

function edgeRateLimitLimit(bindingName: RateLimitBindingName): number {
  switch (bindingName) {
    case "RAG_RATE_LIMITER":
    case "AUTH_EDGE_RATE_LIMITER":
      return 10;
    case "FEEDBACK_RATE_LIMITER":
      return 30;
    case "SEARCH_RATE_LIMITER":
      return 60;
  }
}

function edgeRateLimitResponse(decision: EdgeRateLimitDecision): Response {
  return new Response(
    JSON.stringify({
      error: "edge rate limit exceeded",
      code: "EDGE_RATE_LIMITED"
    }),
    {
      status: 429,
      headers: {
        [CACHE_STATUS_HEADER]: "BYPASS",
        [RATE_LIMIT_STATUS_HEADER]: "BLOCKED",
        "Cache-Control": "no-store",
        "Content-Type": "application/json; charset=utf-8",
        "Retry-After": decision.retryAfterSeconds.toString()
      }
    }
  );
}

function ragDailyQuotaResponse(decision: RagDailyQuotaDecision): Response {
  return new Response(
    JSON.stringify({
      error: "rag daily quota exceeded",
      code: "RAG_DAILY_QUOTA_EXCEEDED"
    }),
    {
      status: 429,
      headers: {
        [CACHE_STATUS_HEADER]: "BYPASS",
        [RATE_LIMIT_STATUS_HEADER]: "BLOCKED",
        [RATE_LIMIT_POLICY_HEADER]: "rag-daily",
        "Cache-Control": "no-store",
        "Content-Type": "application/json; charset=utf-8",
        "Retry-After": decision.retryAfterSeconds.toString()
      }
    }
  );
}

async function writeCaches(
  namespace: KVNamespace,
  key: string,
  cacheRequest: Request,
  entry: CacheEntry
): Promise<void> {
  await namespace.put(key, JSON.stringify(entry), {
    expirationTtl: entry.freshTtlSeconds + entry.staleTtlSeconds
  });
  await caches.default.put(cacheRequest, responseFromEntry(entry));
}

async function readKVEntry(namespace: KVNamespace, key: string): Promise<CacheEntry | null> {
  const entry = await namespace.get<CacheEntry>(key, { type: "json" });
  if (!entry || typeof entry !== "object") {
    return null;
  }

  if (
    typeof entry.status !== "number" ||
    typeof entry.body !== "string" ||
    typeof entry.cachedAt !== "number" ||
    typeof entry.freshTtlSeconds !== "number" ||
    typeof entry.staleTtlSeconds !== "number" ||
    !entry.headers ||
    typeof entry.headers !== "object"
  ) {
    return null;
  }

  return entry;
}

function cacheEntryFromResponse(response: Response, body: string, config: CacheConfig): CacheEntry {
  const headers = preservedHeaders(response.headers, config);

  return {
    status: response.status,
    headers,
    body,
    cachedAt: Date.now(),
    freshTtlSeconds: config.freshTtlSeconds,
    staleTtlSeconds: config.staleTtlSeconds
  };
}

function responseFromEntry(entry: CacheEntry): Response {
  return new Response(entry.body, {
    status: entry.status,
    headers: entry.headers
  });
}

function responseWithCacheStatus(
  response: Response,
  cacheStatus: CacheStatus,
  request: Request,
  cacheControl?: string
): Response {
  const headers = new Headers(response.headers);
  headers.set(CACHE_STATUS_HEADER, cacheStatus);
  if (cacheControl) {
    headers.set("Cache-Control", cacheControl);
  }

  if (notModified(request, headers)) {
    return new Response(null, {
      status: 304,
      headers
    });
  }

  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers
  });
}

function responseWithRateLimitStatus(response: Response, rateLimitStatus: RateLimitStatus): Response {
  const headers = new Headers(response.headers);
  headers.set(RATE_LIMIT_STATUS_HEADER, rateLimitStatus);

  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers
  });
}

function preservedHeaders(headers: Headers, config: CacheConfig): Record<string, string> {
  const preserved: Record<string, string> = {};

  for (const header of PRESERVED_RESPONSE_HEADERS) {
    const value = headers.get(header);
    if (value) {
      preserved[header] = value;
    }
  }

  if (!preserved["content-type"]) {
    preserved["content-type"] = "application/json; charset=utf-8";
  }

  if (!preserved["cache-control"]) {
    preserved["cache-control"] = publicCacheControl(config);
  }

  return preserved;
}

function cacheableOriginResponse(response: Response): boolean {
  if (response.status !== 200) {
    return false;
  }

  return jsonContentType(response.headers.get("content-type"));
}

function jsonContentType(contentType: string | null): boolean {
  if (!contentType) {
    return false;
  }

  return /\bapplication\/json\b|\+json\b/i.test(contentType);
}

function entryFresh(entry: CacheEntry, now: number): boolean {
  return now - entry.cachedAt <= entry.freshTtlSeconds * 1000;
}

function entryStale(entry: CacheEntry, now: number): boolean {
  return now - entry.cachedAt <= (entry.freshTtlSeconds + entry.staleTtlSeconds) * 1000;
}

function notModified(request: Request, headers: Headers): boolean {
  const ifNoneMatch = request.headers.get("if-none-match");
  const etag = headers.get("etag");
  if (!ifNoneMatch || !etag) {
    return false;
  }

  return ifNoneMatch
    .split(",")
    .map((value) => value.trim())
    .some((candidate) => candidate === "*" || candidate === etag);
}

function cacheConfig(env: Env): CacheConfig {
  return {
    version: stringValue(env.CACHE_VERSION, DEFAULT_CACHE_VERSION),
    freshTtlSeconds: positiveInt(env.CACHE_FRESH_TTL_SECONDS, DEFAULT_FRESH_TTL_SECONDS),
    staleTtlSeconds: positiveInt(env.CACHE_STALE_TTL_SECONDS, DEFAULT_STALE_TTL_SECONDS),
    maxCacheBytes: positiveInt(env.MAX_CACHE_BYTES, DEFAULT_MAX_CACHE_BYTES),
    originBaseURL: new URL(env.ORIGIN_BASE_URL)
  };
}

function publicCacheControl(config: CacheConfig): string {
  return `public, max-age=${config.freshTtlSeconds}, stale-while-revalidate=${config.staleTtlSeconds}`;
}

function positiveInt(value: string | undefined, fallback: number): number {
  const parsed = Number.parseInt(value ?? "", 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return fallback;
  }

  return parsed;
}

function stringValue(value: string | undefined, fallback: string): string {
  const trimmed = value?.trim();

  return trimmed ? trimmed : fallback;
}

function isTrackingParam(key: string): boolean {
  const normalized = key.toLowerCase();

  return normalized.startsWith("utm_") || TRACKING_PARAMS.has(normalized);
}

function isRagRequestPath(method: string, path: string): boolean {
  return method === "POST" && /^\/v1\/books\/\d+\/rag$/.test(path);
}

function bearerToken(authorization: string | null): string | null {
  const match = authorization?.match(/^\s*Bearer\s+(.+?)\s*$/i);

  return match?.[1] ?? null;
}

function clientIP(request: Request): string {
  const cloudflareIP = request.headers.get("cf-connecting-ip")?.trim();
  if (cloudflareIP) {
    return cloudflareIP;
  }

  const forwardedIP = request.headers.get("x-forwarded-for")?.split(",")[0]?.trim();

  return forwardedIP || "unknown";
}

async function sha256Hex(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", textEncoder.encode(value));

  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
}

async function verifiedJWTSubject(request: Request, env: Env): Promise<string | null> {
  const token = bearerToken(request.headers.get("authorization"));
  const secret = env.JWT_SECRET?.trim();
  if (!token || !secret) {
    return null;
  }

  try {
    const parts = token.split(".");
    if (parts.length !== 3) {
      return null;
    }

    const header = JSON.parse(utf8FromBase64URL(parts[0] ?? "")) as { alg?: string };
    if (header.alg !== "HS256") {
      return null;
    }

    const signed = `${parts[0]}.${parts[1]}`;
    const signature = bytesFromBase64URL(parts[2] ?? "");
    const key = await crypto.subtle.importKey(
      "raw",
      textEncoder.encode(secret),
      {
        name: "HMAC",
        hash: "SHA-256"
      },
      false,
      ["verify"]
    );
    const valid = await crypto.subtle.verify("HMAC", key, signature, textEncoder.encode(signed));
    if (!valid) {
      return null;
    }

    const payload = JSON.parse(utf8FromBase64URL(parts[1] ?? "")) as {
      aud?: string | string[];
      exp?: number;
      iss?: string;
      sub?: string;
    };
    if (!validJWTPayload(payload, env)) {
      return null;
    }

    return payload.sub?.trim() || null;
  } catch {
    return null;
  }
}

function validJWTPayload(
  payload: { aud?: string | string[]; exp?: number; iss?: string; sub?: string },
  env: Env
): boolean {
  const expectedIssuer = stringValue(env.JWT_ISSUER, DEFAULT_JWT_ISSUER);
  const expectedAudience = stringValue(env.JWT_AUDIENCE, DEFAULT_JWT_AUDIENCE);
  const nowSeconds = Math.floor(Date.now() / 1000);

  if (!payload.sub?.trim()) {
    return false;
  }

  if (payload.iss !== expectedIssuer) {
    return false;
  }

  if (typeof payload.exp !== "number" || payload.exp <= nowSeconds) {
    return false;
  }

  if (Array.isArray(payload.aud)) {
    return payload.aud.includes(expectedAudience);
  }

  return payload.aud === expectedAudience;
}

function utf8FromBase64URL(value: string): string {
  const bytes = bytesFromBase64URL(value);

  return new TextDecoder().decode(bytes);
}

function bytesFromBase64URL(value: string): Uint8Array {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);

  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }

  return bytes;
}

function utcDateKey(date: Date): string {
  return date.toISOString().slice(0, 10);
}

function secondsUntilNextUTCMidnight(date: Date): number {
  const nextMidnight = Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate() + 1);

  return Math.max(1, Math.ceil((nextMidnight - date.getTime()) / 1000));
}

function envFlag(value: string | undefined, fallback: boolean): boolean {
  const normalized = value?.trim().toLowerCase();
  if (!normalized) {
    return fallback;
  }

  return ["1", "true", "yes", "on"].includes(normalized);
}

function stripTrailingSlash(path: string): string {
  if (path.length > 1 && path.endsWith("/")) {
    return path.slice(0, -1);
  }

  return path;
}

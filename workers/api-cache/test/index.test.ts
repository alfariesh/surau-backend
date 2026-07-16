import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cacheDecision,
  cacheKey,
  edgeRateLimitDecision,
  edgeRateLimitKey,
  type Env,
  handleRequest,
  normalizedCacheURL,
  verifiedJWTSubject
} from "../src/index";

class MemoryKV {
  readonly values = new Map<string, string>();

  async get<T = unknown>(key: string, options?: { type?: string }): Promise<T | string | null> {
    const value = this.values.get(key);
    if (value === undefined) {
      return null;
    }

    if (options?.type === "json") {
      return JSON.parse(value) as T;
    }

    return value;
  }

  async put(key: string, value: string): Promise<void> {
    this.values.set(key, value);
  }
}

class MemoryCache {
  readonly values = new Map<string, Response>();

  async match(request: Request): Promise<Response | undefined> {
    return this.values.get(request.url)?.clone();
  }

  async put(request: Request, response: Response): Promise<void> {
    this.values.set(request.url, response.clone());
  }
}

class MemoryRateLimit {
  readonly keys: string[] = [];

  constructor(private success = true) {}

  async limit(options: RateLimitOptions): Promise<RateLimitOutcome> {
    this.keys.push(options.key);

    return { success: this.success };
  }

  block(): void {
    this.success = false;
  }
}

class MemoryDurableRateLimitObject {
  private count = 0;
  private resetAt = 0;

  async fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const request = input instanceof Request ? input : new Request(input, init);
    const payload = (await request.json()) as {
      limit: number;
      resetAtMilliseconds?: number;
      windowSeconds: number;
    };
    const now = Date.now();
    const resetAt = payload.resetAtMilliseconds ?? now + payload.windowSeconds * 1000;

    if (!this.resetAt || now >= this.resetAt) {
      this.count = 1;
      this.resetAt = resetAt;
    } else {
      this.count += 1;
    }

    return new Response(
      JSON.stringify({
        success: this.count <= payload.limit,
        retryAfterSeconds: Math.max(1, Math.ceil((this.resetAt - now) / 1000))
      }),
      {
        headers: {
          "Content-Type": "application/json"
        }
      }
    );
  }
}

class MemoryDurableRateLimitNamespace {
  readonly objects = new Map<string, MemoryDurableRateLimitObject>();

  idFromName(name: string): DurableObjectId {
    return name as unknown as DurableObjectId;
  }

  get(id: DurableObjectId): DurableObjectStub {
    const key = id as unknown as string;
    let object = this.objects.get(key);
    if (!object) {
      object = new MemoryDurableRateLimitObject();
      this.objects.set(key, object);
    }

    return object as unknown as DurableObjectStub;
  }
}

class TestExecutionContext {
  readonly promises: Promise<unknown>[] = [];

  waitUntil(promise: Promise<unknown>): void {
    this.promises.push(promise);
  }

  passThroughOnException(): void {}

  async drain(): Promise<void> {
    await Promise.all(this.promises);
  }
}

const defaultEnv = (): Env => ({
  PUBLIC_API_CACHE: new MemoryKV() as unknown as KVNamespace,
  EDGE_RATE_LIMITER: new MemoryDurableRateLimitNamespace() as unknown as DurableObjectNamespace,
  RAG_RATE_LIMITER: new MemoryRateLimit() as unknown as RateLimit,
  AUTH_EDGE_RATE_LIMITER: new MemoryRateLimit() as unknown as RateLimit,
  FEEDBACK_RATE_LIMITER: new MemoryRateLimit() as unknown as RateLimit,
  SEARCH_RATE_LIMITER: new MemoryRateLimit() as unknown as RateLimit,
  ORIGIN_BASE_URL: "https://origin-api.surau.org",
  CACHE_VERSION: "1",
  CACHE_FRESH_TTL_SECONDS: "300",
  CACHE_STALE_TTL_SECONDS: "86400",
  MAX_CACHE_BYTES: "2000000",
  RAG_DAILY_QUOTA_ENABLED: "true",
  RAG_DAILY_GUEST_LIMIT: "100",
  RAG_DAILY_USER_LIMIT: "50",
  JWT_SECRET: "0123456789abcdef0123456789abcdef",
  JWT_ISSUER: "surau-backend",
  JWT_AUDIENCE: "surau-api"
});

const OLD_JWT_SECRET = "old-secret-0123456789abcdef0123456789abcdef";
const NEW_JWT_SECRET = "new-secret-0123456789abcdef0123456789abcdef";

function rotatingJWTEnv(): Env {
  return {
    ...defaultEnv(),
    JWT_KEYSET: JSON.stringify({
      version: 1,
      active_kid: "new-2026-07",
      legacy_kid: "old-2026-01",
      keys: {
        "old-2026-01": OLD_JWT_SECRET,
        "new-2026-07": NEW_JWT_SECRET
      }
    })
  };
}

function installMemoryCache(): MemoryCache {
  const cache = new MemoryCache();
  vi.stubGlobal("caches", { default: cache });

  return cache;
}

function request(path: string, init?: RequestInit): Request {
  return new Request(`https://api.surau.org${path}`, init);
}

function bearerRequest(token: string): Request {
  return request("/v1/books/0/rag", {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` }
  });
}

function jsonResponse(body: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    ...init,
    headers: {
      "Cache-Control": "public, max-age=300, stale-while-revalidate=86400",
      "Content-Type": "application/json",
      ETag: 'W/"test-etag"',
      ...(init?.headers ?? {})
    }
  });
}

function memoryRateLimit(binding: RateLimit): MemoryRateLimit {
  return binding as unknown as MemoryRateLimit;
}

function memoryDurableNamespace(namespace: DurableObjectNamespace): MemoryDurableRateLimitNamespace {
  return namespace as unknown as MemoryDurableRateLimitNamespace;
}

async function signTestJWT(
  subject: string,
  secret = "0123456789abcdef0123456789abcdef",
  overrides: Record<string, unknown> = {},
  headerOverrides: Record<string, unknown> = {}
): Promise<string> {
  const header = base64URLFromBytes(
    new TextEncoder().encode(JSON.stringify({ alg: "HS256", typ: "JWT", ...headerOverrides }))
  );
  const nowSeconds = Math.floor(Date.now() / 1000);
  const payload = base64URLFromBytes(
    new TextEncoder().encode(
      JSON.stringify({
        sub: subject,
        iss: "surau-backend",
        aud: "surau-api",
        exp: nowSeconds + 3600,
        iat: nowSeconds,
        ...overrides
      })
    )
  );
  const signed = `${header}.${payload}`;
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    {
      name: "HMAC",
      hash: "SHA-256"
    },
    false,
    ["sign"]
  );
  const signature = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(signed));

  return `${signed}.${base64URLFromBytes(new Uint8Array(signature))}`;
}

function base64URLFromBytes(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }

  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

describe("Surau API cache policy", () => {
  beforeEach(() => {
    installMemoryCache();
    vi.useRealTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("allowlists only safe public GET paths", () => {
    expect(cacheDecision(request("/v1/categories?lang=id")).cacheable).toBe(true);
    expect(cacheDecision(request("/v1/quran/surahs/1/ayahs?lang=id"))).toEqual({
      cacheable: false,
      reason: "protected_or_operational_path"
    });
    for (const path of ["/v1/quran/sitemap", "/v1/quran/feed?lang=id", "/v1/quran/slugs/al-fatihah"]) {
      expect(cacheDecision(request(path))).toEqual({
        cacheable: false,
        reason: "protected_or_operational_path"
      });
    }

    expect(cacheDecision(request("/v1/books/797/toc/10/read?lang=id"))).toEqual({
      cacheable: false,
      reason: "protected_or_operational_path"
    });
    expect(cacheDecision(request("/v1/books?lang=id"))).toEqual({
      cacheable: false,
      reason: "protected_or_operational_path"
    });
    expect(cacheDecision(request("/v1/books/797/quran-references?status=approved")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/books?q=hadith")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/cross-references?anchor=quran%2F1%3A1&direction=incoming"))).toEqual({
      cacheable: false,
      reason: "protected_or_operational_path"
    });
    expect(cacheDecision(request("/v1/books/797/quran-references?status=pending")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/quran/search?q=rahman")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/anchors/resolve?anchor=quran%2F1%3A1"))).toEqual({
      cacheable: false,
      reason: "protected_or_operational_path"
    });
    expect(cacheDecision(request("/v1/me/saved-items")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/categories", { headers: { Authorization: "Bearer token" } })).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/books/797/rag", { method: "POST" })).cacheable).toBe(false);
  });

  it("never reads stale edge entries for license-sensitive public content", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const l1 = installMemoryCache();
    const bookURL = "https://api.surau.org/v1/books/797?lang=id";
    await l1.put(
      new Request(bookURL),
      jsonResponse({ id: 797, title: "stale L1 title" })
    );
    (env.PUBLIC_API_CACHE as unknown as MemoryKV).values.set(
      cacheKey(new URL(bookURL), "1"),
      JSON.stringify({
        status: 200,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id: 797, title: "stale KV title" }),
        cachedAt: Date.now(),
        freshTtlSeconds: 300,
        staleTtlSeconds: 86400
      })
    );

    let revision = 0;
    const fetchMock = vi.fn(async () => {
      revision += 1;

      return jsonResponse(
        { id: 797, title: `origin revision ${revision}`, updated_at: "2026-07-11T08:09:10Z" },
        {
          headers: {
            "Cache-Control": "public, max-age=0, must-revalidate",
            ETag: `W/\"revision-${revision}\"`,
            "Last-Modified": "Sat, 11 Jul 2026 08:09:10 GMT"
          }
        }
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    const first = await handleRequest(request("/v1/books/797?lang=id"), env, ctx as unknown as ExecutionContext);
    const second = await handleRequest(request("/v1/books/797?lang=id"), env, ctx as unknown as ExecutionContext);

    expect(first.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(second.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(first.headers.get("Cache-Control")).toBe("public, max-age=0, must-revalidate");
    expect(first.headers.get("ETag")).toBe('W/"revision-1"');
    expect(first.headers.get("Last-Modified")).toBe("Sat, 11 Jul 2026 08:09:10 GMT");
    expect(await first.json()).toMatchObject({ title: "origin revision 1" });
    expect(await second.json()).toMatchObject({ title: "origin revision 2" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("rate-limits kitab heading searches while keeping them out of edge caches", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ items: [], total: 0 }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleRequest(
      request("/v1/books/797/headings?q=iman", {
        headers: { "CF-Connecting-IP": "203.0.113.79" }
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(response.status).toBe(200);
    expect(response.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(response.headers.get("X-Surau-RateLimit")).toBe("PASS");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(memoryRateLimit(env.SEARCH_RATE_LIMITER).keys).toEqual(["search:ip:203.0.113.79"]);
  });

  it("selects edge rate limit policies for expensive endpoints only", () => {
    expect(edgeRateLimitDecision(request("/v1/books/797/rag", { method: "POST" }))).toMatchObject({
      bindingName: "RAG_RATE_LIMITER",
      group: "rag"
    });
    expect(
      edgeRateLimitDecision(request("/v1/books/797/toc/10/translation-feedback", { method: "POST" }))
    ).toMatchObject({
      bindingName: "FEEDBACK_RATE_LIMITER",
      group: "feedback"
    });
    expect(edgeRateLimitDecision(request("/v1/auth/forgot-password", { method: "POST" }))).toMatchObject({
      bindingName: "AUTH_EDGE_RATE_LIMITER",
      group: "auth:forgot-password"
    });
    expect(edgeRateLimitDecision(request("/v1/books?q=hadith"))).toMatchObject({
      bindingName: "SEARCH_RATE_LIMITER",
      group: "search"
    });
    expect(edgeRateLimitDecision(request("/v1/books/797/headings?q=iman"))).toMatchObject({
      bindingName: "SEARCH_RATE_LIMITER",
      group: "search"
    });
    expect(edgeRateLimitDecision(request("/v1/quran/search?q=rahman"))).toMatchObject({
      bindingName: "SEARCH_RATE_LIMITER",
      group: "search"
    });

    expect(edgeRateLimitDecision(request("/v1/quran/surahs?lang=id"))).toBeNull();
    expect(edgeRateLimitDecision(request("/v1/editorial/production-candidates?q=test"))).toBeNull();
    expect(edgeRateLimitDecision(request("/v1/books/797/rag"))).toBeNull();
  });

  it("builds stable rate limit keys from route group and identity", async () => {
    const bearerKey = await edgeRateLimitKey(
      request("/v1/books/797/rag", {
        method: "POST",
        headers: { Authorization: "Bearer secret-token" }
      }),
      "rag"
    );
    const ipKey = await edgeRateLimitKey(
      request("/v1/books/999/rag", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.10" }
      }),
      "rag"
    );

    expect(bearerKey).toMatch(/^rag:bearer:[a-f0-9]{64}$/);
    expect(bearerKey).not.toContain("secret-token");
    expect(ipKey).toBe("rag:ip:203.0.113.10");
  });

  it("uses sorted query parameters and strips tracking parameters in cache keys", () => {
    const normalized = normalizedCacheURL(
      "https://api.surau.org/v1/quran/surahs?utm_source=x&lang=id&include_info=false&fbclid=abc"
    );

    expect(normalized.toString()).toBe("https://api.surau.org/v1/quran/surahs?include_info=false&lang=id");
    expect(cacheKey(normalized, "7")).toBe("v1:7:GET:/v1/quran/surahs?include_info=false&lang=id");
  });

  it("returns MISS then an L1 cache hit for repeated public GETs", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse([{ id: 1, name: "Al-Fatihah" }]));
    vi.stubGlobal("fetch", fetchMock);

    const first = await handleRequest(request("/v1/categories?lang=id"), env, ctx as unknown as ExecutionContext);
    const second = await handleRequest(request("/v1/categories?lang=id"), env, ctx as unknown as ExecutionContext);

    expect(first.headers.get("X-Surau-Cache")).toBe("MISS");
    expect(second.headers.get("X-Surau-Cache")).toBe("L1-HIT");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("bypasses authenticated requests, POST RAG, and search endpoints", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const authed = await handleRequest(
      request("/v1/quran/surahs?lang=id", { headers: { Authorization: "Bearer token" } }),
      env,
      ctx as unknown as ExecutionContext
    );
    const rag = await handleRequest(
      request("/v1/books/797/rag", { method: "POST", body: JSON.stringify({ question: "Apa?" }) }),
      env,
      ctx as unknown as ExecutionContext
    );
    const search = await handleRequest(
      request("/v1/quran/search?q=rahman"),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(authed.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(rag.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(rag.headers.get("X-Surau-RateLimit")).toBe("PASS");
    expect(search.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(search.headers.get("X-Surau-RateLimit")).toBe("PASS");
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect((fetchMock.mock.calls[0]?.[0] as Request).headers.get("Authorization")).toBe("Bearer token");
  });

  it("returns 429 at the edge when an expensive endpoint exceeds its limiter", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    memoryRateLimit(env.RAG_RATE_LIMITER).block();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleRequest(
      request("/v1/books/797/rag", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.10" },
        body: JSON.stringify({ question: "Apa?" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(response.status).toBe(429);
    expect(response.headers.get("Retry-After")).toBe("60");
    expect(response.headers.get("Cache-Control")).toBe("no-store");
    expect(response.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(response.headers.get("X-Surau-RateLimit")).toBe("BLOCKED");
    expect(await response.json()).toEqual({
      error: "edge rate limit exceeded",
      code: "EDGE_RATE_LIMITED"
    });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(memoryRateLimit(env.RAG_RATE_LIMITER).keys).toEqual(["rag:ip:203.0.113.10"]);
  });

  it("uses Durable Object coordination before Cloudflare's permissive limiter catches up", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    let response = new Response(null);
    for (let index = 0; index < 11; index += 1) {
      response = await handleRequest(
        request("/v1/books/0/rag", {
          method: "POST",
          headers: { "CF-Connecting-IP": "203.0.113.11" },
          body: JSON.stringify({ question: "smoke" })
        }),
        env,
        ctx as unknown as ExecutionContext
      );
    }

    expect(response.status).toBe(429);
    expect(response.headers.get("X-Surau-RateLimit")).toBe("BLOCKED");
    expect(response.headers.get("Retry-After")).toBe("60");
    expect(fetchMock).toHaveBeenCalledTimes(10);
    expect(memoryRateLimit(env.RAG_RATE_LIMITER).keys).toHaveLength(10);
  });

  it("continues to origin when an expensive endpoint is allowed by the limiter", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleRequest(
      request("/v1/auth/forgot-password", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.20" },
        body: JSON.stringify({ email: "user@example.com" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(response.status).toBe(200);
    expect(response.headers.get("X-Surau-Cache")).toBe("BYPASS");
    expect(response.headers.get("X-Surau-RateLimit")).toBe("PASS");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(memoryRateLimit(env.AUTH_EDGE_RATE_LIMITER).keys).toEqual([
      "auth:forgot-password:ip:203.0.113.20"
    ]);
  });

  it("does not call rate limit bindings for cacheable public reads without q", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse([{ id: 1, name: "Al-Fatihah" }]));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleRequest(
      request("/v1/quran/surahs?lang=id", {
        headers: { "CF-Connecting-IP": "203.0.113.30" }
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(response.headers.get("X-Surau-RateLimit")).toBeNull();
    expect(memoryRateLimit(env.SEARCH_RATE_LIMITER).keys).toEqual([]);
  });

  it("returns daily quota 429 for the 101st guest RAG request in the same UTC day", async () => {
    vi.setSystemTime(new Date("2026-06-06T00:00:00Z"));
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    let response = new Response(null);
    for (let index = 0; index < 101; index += 1) {
      response = await handleRequest(
        request("/v1/books/0/rag", {
          method: "POST",
          headers: { "CF-Connecting-IP": "203.0.113.101" },
          body: JSON.stringify({ question: "smoke" })
        }),
        env,
        ctx as unknown as ExecutionContext
      );
      if (index < 100) {
        vi.setSystemTime(new Date(Date.now() + 61_000));
      }
    }

    expect(response.status).toBe(429);
    expect(response.headers.get("X-Surau-RateLimit")).toBe("BLOCKED");
    expect(response.headers.get("X-Surau-RateLimit-Policy")).toBe("rag-daily");
    expect(response.headers.get("X-Surau-JWT-Identity")).toBe("guest");
    expect(response.headers.get("Retry-After")).toBe("80300");
    expect(await response.json()).toEqual({
      error: "rag daily quota exceeded",
      code: "RAG_DAILY_QUOTA_EXCEEDED"
    });
    expect(fetchMock).toHaveBeenCalledTimes(100);
  });

  it("returns daily quota 429 for the 51st valid JWT user RAG request", async () => {
    vi.setSystemTime(new Date("2026-06-06T00:00:00Z"));
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const token = await signTestJWT("user-123");
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    let response = new Response(null);
    for (let index = 0; index < 51; index += 1) {
      response = await handleRequest(
        request("/v1/books/0/rag", {
          method: "POST",
          headers: {
            Authorization: `Bearer ${token}`,
            "CF-Connecting-IP": "203.0.113.102"
          },
          body: JSON.stringify({ question: "smoke" })
        }),
        env,
        ctx as unknown as ExecutionContext
      );
      if (index < 50) {
        vi.setSystemTime(new Date(Date.now() + 61_000));
      }
    }

    expect(response.status).toBe(429);
    expect(response.headers.get("X-Surau-RateLimit-Policy")).toBe("rag-daily");
    expect(response.headers.get("X-Surau-JWT-Identity")).toBe("user");
    expect(response.headers.get("Retry-After")).toBe("83350");
    expect(fetchMock).toHaveBeenCalledTimes(50);
    expect(Array.from(memoryDurableNamespace(env.EDGE_RATE_LIMITER).objects.keys())).toContain(
      "rag-daily:user:user-123:2026-06-06"
    );
  });

  it("accepts old, new, and living no-kid tokens during the rotation overlap", async () => {
    const env = rotatingJWTEnv();
    const oldToken = await signTestJWT("old-user", OLD_JWT_SECRET, {}, { kid: "old-2026-01" });
    const newToken = await signTestJWT("new-user", NEW_JWT_SECRET, {}, { kid: "new-2026-07" });
    const noKidToken = await signTestJWT("legacy-user", OLD_JWT_SECRET);

    await expect(verifiedJWTSubject(bearerRequest(oldToken), env)).resolves.toBe("old-user");
    await expect(verifiedJWTSubject(bearerRequest(newToken), env)).resolves.toBe("new-user");
    await expect(verifiedJWTSubject(bearerRequest(noKidToken), env)).resolves.toBe("legacy-user");
  });

  it("exposes a non-sensitive live verifier result for rotation smoke checks", async () => {
    const env = rotatingJWTEnv();
    const ctx = new TestExecutionContext();
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({ ok: true })));
    const tokens = [
      await signTestJWT("old-user", OLD_JWT_SECRET, {}, { kid: "old-2026-01" }),
      await signTestJWT("new-user", NEW_JWT_SECRET, {}, { kid: "new-2026-07" }),
      await signTestJWT("legacy-user", OLD_JWT_SECRET)
    ];

    for (const token of tokens) {
      const response = await handleRequest(
        request("/v1/books/0/rag", {
          method: "POST",
          headers: { Authorization: `Bearer ${token}` },
          body: JSON.stringify({ question: "rotation smoke" })
        }),
        env,
        ctx as unknown as ExecutionContext
      );
      expect(response.headers.get("X-Surau-JWT-Identity")).toBe("user");
    }
  });

  it("rejects old and no-kid tokens after retirement while keeping the new key valid", async () => {
    const env = {
      ...defaultEnv(),
      JWT_KEYSET: JSON.stringify({
        version: 1,
        active_kid: "new-2026-07",
        keys: { "new-2026-07": NEW_JWT_SECRET }
      })
    };
    const oldToken = await signTestJWT("old-user", OLD_JWT_SECRET, {}, { kid: "old-2026-01" });
    const noKidToken = await signTestJWT("legacy-user", OLD_JWT_SECRET);
    const newToken = await signTestJWT("new-user", NEW_JWT_SECRET, {}, { kid: "new-2026-07" });

    await expect(verifiedJWTSubject(bearerRequest(oldToken), env)).resolves.toBeNull();
    await expect(verifiedJWTSubject(bearerRequest(noKidToken), env)).resolves.toBeNull();
    await expect(verifiedJWTSubject(bearerRequest(newToken), env)).resolves.toBe("new-user");
  });

  it("requires an exact, string kid and verifies only with the selected key", async () => {
    const env = rotatingJWTEnv();
    const unknownKid = await signTestJWT("unknown-user", OLD_JWT_SECRET, {}, { kid: "unknown" });
    const nonStringKid = await signTestJWT("number-user", OLD_JWT_SECRET, {}, { kid: 17 });
    const wrongSignature = await signTestJWT(
      "wrong-user",
      NEW_JWT_SECRET,
      {},
      { kid: "old-2026-01" }
    );

    await expect(verifiedJWTSubject(bearerRequest(unknownKid), env)).resolves.toBeNull();
    await expect(verifiedJWTSubject(bearerRequest(nonStringKid), env)).resolves.toBeNull();
    await expect(verifiedJWTSubject(bearerRequest(wrongSignature), env)).resolves.toBeNull();
  });

  it("fails closed on every invalid configured keyset without falling back to JWT_SECRET", async () => {
    const fallbackToken = await signTestJWT("must-be-guest");
    const invalidKeysets = [
      "",
      "not-json",
      JSON.stringify({ version: 2, active_kid: "old", keys: { old: OLD_JWT_SECRET } }),
      JSON.stringify({ version: 1, active_kid: "old", keys: { old: OLD_JWT_SECRET }, extra: true }),
      JSON.stringify({ version: 1, active_kid: "missing", keys: { old: OLD_JWT_SECRET } }),
      JSON.stringify({
        version: 1,
        active_kid: "old",
        legacy_kid: "missing",
        keys: { old: OLD_JWT_SECRET }
      }),
      JSON.stringify({ version: 1, active_kid: "old", keys: [] }),
      JSON.stringify({ version: 1, active_kid: "old", keys: { old: "short" } }),
      JSON.stringify({
        version: 1,
        active_kid: "old",
        keys: { old: OLD_JWT_SECRET, duplicate: OLD_JWT_SECRET }
      }),
      JSON.stringify({
        version: 1,
        active_kid: "old",
        keys: { old: OLD_JWT_SECRET, next: NEW_JWT_SECRET, stale: `${NEW_JWT_SECRET}-third` }
      }),
      JSON.stringify({
        version: 1,
        active_kid: "contains space",
        keys: { "contains space": OLD_JWT_SECRET }
      })
    ];

    for (const JWT_KEYSET of invalidKeysets) {
      await expect(
        verifiedJWTSubject(bearerRequest(fallbackToken), { ...defaultEnv(), JWT_KEYSET })
      ).resolves.toBeNull();
    }
  });

  it("accounts a signed bearer as guest when the configured keyset is invalid", async () => {
    vi.setSystemTime(new Date("2026-06-06T00:00:00Z"));
    const env = { ...defaultEnv(), JWT_KEYSET: "not-json" };
    const token = await signTestJWT("must-be-guest");
    const ctx = new TestExecutionContext();
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({ ok: true })));

    const response = await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "CF-Connecting-IP": "203.0.113.199"
        },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(response.status).toBe(200);
    expect(response.headers.get("X-Surau-JWT-Identity")).toBe("guest");
    expect(Array.from(memoryDurableNamespace(env.EDGE_RATE_LIMITER).objects.keys())).toContain(
      "rag-daily:ip:203.0.113.199:2026-06-06"
    );
    expect(Array.from(memoryDurableNamespace(env.EDGE_RATE_LIMITER).objects.keys())).not.toContain(
      "rag-daily:user:must-be-guest:2026-06-06"
    );
  });

  it("keeps JWT_SECRET compatibility only while JWT_KEYSET is absent", async () => {
    const token = await signTestJWT("migration-user");

    await expect(verifiedJWTSubject(bearerRequest(token), defaultEnv())).resolves.toBe("migration-user");
  });

  it("preserves legacy JWT_SECRET bytes instead of normalizing them", async () => {
    const literalSecret = ` ${OLD_JWT_SECRET} `;
    const token = await signTestJWT("literal-secret-user", literalSecret);

    await expect(
      verifiedJWTSubject(bearerRequest(token), { ...defaultEnv(), JWT_SECRET: literalSecret })
    ).resolves.toBe("literal-secret-user");
  });

  it("falls back to IP daily quota for invalid Bearer tokens", async () => {
    vi.setSystemTime(new Date("2026-06-06T00:00:00Z"));
    const env = { ...defaultEnv(), RAG_DAILY_GUEST_LIMIT: "1" };
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const first = await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: {
          Authorization: "Bearer fake-a",
          "CF-Connecting-IP": "203.0.113.103"
        },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );
    const second = await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: {
          Authorization: "Bearer fake-b",
          "CF-Connecting-IP": "203.0.113.103"
        },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(first.headers.get("X-Surau-RateLimit")).toBe("PASS");
    expect(second.status).toBe(429);
    expect(second.headers.get("X-Surau-RateLimit-Policy")).toBe("rag-daily");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(Array.from(memoryDurableNamespace(env.EDGE_RATE_LIMITER).objects.keys())).toContain(
      "rag-daily:ip:203.0.113.103:2026-06-06"
    );
  });

  it("does not consume daily quota when the per-minute limiter blocks first", async () => {
    vi.setSystemTime(new Date("2026-06-06T00:00:00Z"));
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    memoryRateLimit(env.RAG_RATE_LIMITER).block();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.104" },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(response.status).toBe(429);
    expect(response.headers.get("X-Surau-RateLimit-Policy")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(Array.from(memoryDurableNamespace(env.EDGE_RATE_LIMITER).objects.keys())).not.toContain(
      "rag-daily:ip:203.0.113.104:2026-06-06"
    );
  });

  it("resets daily quota on UTC day rollover and updates Retry-After", async () => {
    vi.setSystemTime(new Date("2026-06-06T23:59:30Z"));
    const env = { ...defaultEnv(), RAG_DAILY_GUEST_LIMIT: "1" };
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.105" },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );
    const blocked = await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.105" },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );
    vi.setSystemTime(new Date("2026-06-07T00:00:01Z"));
    const nextDay = await handleRequest(
      request("/v1/books/0/rag", {
        method: "POST",
        headers: { "CF-Connecting-IP": "203.0.113.105" },
        body: JSON.stringify({ question: "smoke" })
      }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(blocked.status).toBe(429);
    expect(blocked.headers.get("Retry-After")).toBe("30");
    expect(nextDay.status).toBe(200);
    expect(nextDay.headers.get("X-Surau-RateLimit")).toBe("PASS");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not cache responses above MAX_CACHE_BYTES", async () => {
    const env = { ...defaultEnv(), MAX_CACHE_BYTES: "10" };
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ payload: "this response is too large" }));
    vi.stubGlobal("fetch", fetchMock);

    const first = await handleRequest(request("/v1/categories"), env, ctx as unknown as ExecutionContext);
    const second = await handleRequest(request("/v1/categories"), env, ctx as unknown as ExecutionContext);

    expect(first.headers.get("X-Surau-Cache")).toBe("MISS");
    expect(second.headers.get("X-Surau-Cache")).toBe("MISS");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("returns 304 from cached validators when If-None-Match matches", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ id: 1 }));
    vi.stubGlobal("fetch", fetchMock);

    await handleRequest(request("/v1/categories?lang=id"), env, ctx as unknown as ExecutionContext);
    const cached = await handleRequest(
      request("/v1/categories?lang=id", { headers: { "If-None-Match": 'W/"test-etag"' } }),
      env,
      ctx as unknown as ExecutionContext
    );

    expect(cached.status).toBe(304);
    expect(cached.headers.get("X-Surau-Cache")).toBe("L1-HIT");
  });

  it("serves stale KV and revalidates in the background", async () => {
    vi.setSystemTime(new Date("2026-06-05T00:00:00Z"));
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const normalized = normalizedCacheURL("https://api.surau.org/v1/categories?lang=id");
    const key = cacheKey(normalized, "1");
    await (env.PUBLIC_API_CACHE as unknown as MemoryKV).put(
      key,
      JSON.stringify({
        status: 200,
        headers: {
          "cache-control": "public, max-age=300, stale-while-revalidate=86400",
          "content-type": "application/json"
        },
        body: JSON.stringify([{ id: 1, stale: true }]),
        cachedAt: Date.now() - 301_000,
        freshTtlSeconds: 300,
        staleTtlSeconds: 86400
      })
    );
    const fetchMock = vi.fn(async () => jsonResponse([{ id: 1, stale: false }]));
    vi.stubGlobal("fetch", fetchMock);

    const stale = await handleRequest(request("/v1/categories?lang=id"), env, ctx as unknown as ExecutionContext);
    expect(stale.headers.get("X-Surau-Cache")).toBe("STALE");
    expect(await stale.json()).toEqual([{ id: 1, stale: true }]);

    await ctx.drain();

    const refreshed = await handleRequest(request("/v1/categories?lang=id"), env, ctx as unknown as ExecutionContext);
    expect(refreshed.headers.get("X-Surau-Cache")).toMatch(/^(L1-HIT|KV-HIT)$/);
    expect(await refreshed.json()).toEqual([{ id: 1, stale: false }]);
  });

  it("serves stale during origin failure, then stops serving after stale window expires", async () => {
    vi.setSystemTime(new Date("2026-06-05T00:00:00Z"));
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const normalized = normalizedCacheURL("https://api.surau.org/v1/categories?lang=id");
    const key = cacheKey(normalized, "1");
    const kv = env.PUBLIC_API_CACHE as unknown as MemoryKV;

    await kv.put(
      key,
      JSON.stringify({
        status: 200,
        headers: {
          "cache-control": "public, max-age=300, stale-while-revalidate=86400",
          "content-type": "application/json"
        },
        body: JSON.stringify([{ id: "old" }]),
        cachedAt: Date.now() - 301_000,
        freshTtlSeconds: 300,
        staleTtlSeconds: 86400
      })
    );

    const fetchMock = vi.fn(async () => new Response("origin unavailable", { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);

    const stale = await handleRequest(
      request("/v1/categories?lang=id"),
      env,
      ctx as unknown as ExecutionContext
    );
    expect(stale.headers.get("X-Surau-Cache")).toBe("STALE");
    expect(await stale.json()).toEqual([{ id: "old" }]);

    await kv.put(
      key,
      JSON.stringify({
        status: 200,
        headers: {
          "cache-control": "public, max-age=300, stale-while-revalidate=86400",
          "content-type": "application/json"
        },
        body: JSON.stringify([{ id: "expired" }]),
        cachedAt: Date.now() - (300 + 86400) * 1000 - 1,
        freshTtlSeconds: 300,
        staleTtlSeconds: 86400
      })
    );

    const expired = await handleRequest(
      request("/v1/categories?lang=id"),
      env,
      new TestExecutionContext() as unknown as ExecutionContext
    );
    expect(expired.status).toBe(503);
    expect(expired.headers.get("X-Surau-Cache")).toBe("MISS");
    expect(await expired.text()).toBe("origin unavailable");
  });

  it("prevents routing loops when origin host equals request host", async () => {
    const env = { ...defaultEnv(), ORIGIN_BASE_URL: "https://api.surau.org" };
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await handleRequest(request("/v1/quran/surahs?lang=id"), env, ctx as unknown as ExecutionContext);

    expect(response.status).toBe(508);
    expect(await response.text()).toBe("origin loop detected");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

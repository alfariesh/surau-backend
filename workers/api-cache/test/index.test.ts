import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cacheDecision,
  cacheKey,
  edgeRateLimitDecision,
  edgeRateLimitKey,
  type Env,
  handleRequest,
  normalizedCacheURL
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
  private windowStartedAt = 0;

  async fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const request = input instanceof Request ? input : new Request(input, init);
    const payload = (await request.json()) as { limit: number; windowSeconds: number };
    const now = Date.now();
    const windowMilliseconds = payload.windowSeconds * 1000;

    if (!this.windowStartedAt || now - this.windowStartedAt >= windowMilliseconds) {
      this.count = 1;
      this.windowStartedAt = now;
    } else {
      this.count += 1;
    }

    return new Response(
      JSON.stringify({
        success: this.count <= payload.limit,
        retryAfterSeconds: Math.max(1, Math.ceil((windowMilliseconds - (now - this.windowStartedAt)) / 1000))
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
  MAX_CACHE_BYTES: "2000000"
});

function installMemoryCache(): MemoryCache {
  const cache = new MemoryCache();
  vi.stubGlobal("caches", { default: cache });

  return cache;
}

function request(path: string, init?: RequestInit): Request {
  return new Request(`https://api.surau.org${path}`, init);
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

describe("Surau API cache policy", () => {
  beforeEach(() => {
    installMemoryCache();
    vi.useRealTimers();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("allowlists only safe public GET paths", () => {
    expect(cacheDecision(request("/v1/categories?lang=id")).cacheable).toBe(true);
    expect(cacheDecision(request("/v1/books/797/toc/10/read?lang=id")).cacheable).toBe(true);
    expect(cacheDecision(request("/v1/books/797/quran-references?status=approved")).cacheable).toBe(true);
    expect(cacheDecision(request("/v1/quran/surahs/1/ayahs?lang=id")).cacheable).toBe(true);

    expect(cacheDecision(request("/v1/books?q=hadith")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/books/797/quran-references?status=pending")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/quran/search?q=rahman")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/me/saved-items")).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/categories", { headers: { Authorization: "Bearer token" } })).cacheable).toBe(false);
    expect(cacheDecision(request("/v1/books/797/rag", { method: "POST" })).cacheable).toBe(false);
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

    const first = await handleRequest(request("/v1/quran/surahs?lang=id"), env, ctx as unknown as ExecutionContext);
    const second = await handleRequest(request("/v1/quran/surahs?lang=id"), env, ctx as unknown as ExecutionContext);

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

  it("does not cache responses above MAX_CACHE_BYTES", async () => {
    const env = { ...defaultEnv(), MAX_CACHE_BYTES: "10" };
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ payload: "this response is too large" }));
    vi.stubGlobal("fetch", fetchMock);

    const first = await handleRequest(request("/v1/quran/recitations"), env, ctx as unknown as ExecutionContext);
    const second = await handleRequest(request("/v1/quran/recitations"), env, ctx as unknown as ExecutionContext);

    expect(first.headers.get("X-Surau-Cache")).toBe("MISS");
    expect(second.headers.get("X-Surau-Cache")).toBe("MISS");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("returns 304 from cached validators when If-None-Match matches", async () => {
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const fetchMock = vi.fn(async () => jsonResponse({ id: 1 }));
    vi.stubGlobal("fetch", fetchMock);

    await handleRequest(request("/v1/quran/surahs/1?lang=id"), env, ctx as unknown as ExecutionContext);
    const cached = await handleRequest(
      request("/v1/quran/surahs/1?lang=id", { headers: { "If-None-Match": 'W/"test-etag"' } }),
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
    const normalized = normalizedCacheURL("https://api.surau.org/v1/quran/surahs?lang=id");
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

    const stale = await handleRequest(request("/v1/quran/surahs?lang=id"), env, ctx as unknown as ExecutionContext);
    expect(stale.headers.get("X-Surau-Cache")).toBe("STALE");
    expect(await stale.json()).toEqual([{ id: 1, stale: true }]);

    await ctx.drain();

    const refreshed = await handleRequest(request("/v1/quran/surahs?lang=id"), env, ctx as unknown as ExecutionContext);
    expect(refreshed.headers.get("X-Surau-Cache")).toMatch(/^(L1-HIT|KV-HIT)$/);
    expect(await refreshed.json()).toEqual([{ id: 1, stale: false }]);
  });

  it("serves stale during origin failure, then stops serving after stale window expires", async () => {
    vi.setSystemTime(new Date("2026-06-05T00:00:00Z"));
    const env = defaultEnv();
    const ctx = new TestExecutionContext();
    const normalized = normalizedCacheURL("https://api.surau.org/v1/quran/translation-sources?lang=id");
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
      request("/v1/quran/translation-sources?lang=id"),
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
      request("/v1/quran/translation-sources?lang=id"),
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

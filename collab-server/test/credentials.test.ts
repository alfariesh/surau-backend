import { describe, expect, it, vi } from "vitest";
import type pg from "pg";

import { ReloadingServiceToken } from "../src/credentials.js";
import { createLogger } from "../src/logger.js";
import { ReloadingPool, type CollabPool } from "../src/persistence.js";

describe("overlap credential reload", () => {
  it("activates a verified T2 and retains T2 when a later candidate fails", async () => {
    let fileToken = "token-t1-32-bytes-minimum-aaaaaaaa";
    const fetchImpl = vi.fn(async (_url: string | URL | Request, init?: RequestInit) => {
      const candidate = (init?.headers as Record<string, string>)["X-Internal-Token"];
      if (candidate === "token-t2-32-bytes-minimum-bbbbbbbb") {
        return new Response(JSON.stringify({
          principal_name: "collab-server",
          scopes: ["collab:draft:write"],
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response("unauthorized", { status: 401 });
    });
    const provider = new ReloadingServiceToken({
      initialToken: fileToken,
      filePath: "/run/secrets/collab/service-token",
      baseUrl: "http://app:8080",
      logger: createLogger("silent"),
      fetchImpl: fetchImpl as typeof fetch,
      readImpl: async () => fileToken,
    });

    expect(await provider.getToken()).toBe(fileToken);
    fileToken = "token-t2-32-bytes-minimum-bbbbbbbb";
    expect(await provider.getToken()).toBe(fileToken);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    fileToken = "token-bad-32-bytes-minimum-cccccccc";
    expect(await provider.getToken()).toBe("token-t2-32-bytes-minimum-bbbbbbbb");
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it("atomically swaps a validated DB pool and keeps the old pool on failure", async () => {
    let fileUrl = "postgres://role-a/db";
    const pools = new Map<string, FakePool>();
    const poolFactory = (url: string): CollabPool => {
      const pool = new FakePool(url, url.includes("broken"));
      pools.set(url, pool);
      return pool;
    };
    const pool = new ReloadingPool({
      initialUrl: fileUrl,
      filePath: "/run/secrets/collab/pg-url",
      logger: createLogger("silent"),
      poolFactory,
      readImpl: async () => fileUrl,
    });

    await pool.query("SELECT state FROM collab_documents");
    expect(pools.get("postgres://role-a/db")?.queries).toEqual(["SELECT state FROM collab_documents"]);

    fileUrl = "postgres://role-b/db";
    await pool.query("SELECT state FROM collab_documents");
    expect(pools.get("postgres://role-b/db")?.queries.at(-1)).toBe("SELECT state FROM collab_documents");
    expect(pools.get("postgres://role-b/db")?.queries[0]).toContain("INSERT INTO collab_documents");
    await vi.waitFor(() => expect(pools.get("postgres://role-a/db")?.ended).toBe(true));

    fileUrl = "postgres://broken/db";
    await pool.query("SELECT state FROM collab_documents");
    expect(pools.get("postgres://broken/db")?.ended).toBe(true);
    expect(pools.get("postgres://role-b/db")?.queries.at(-1)).toBe("SELECT state FROM collab_documents");
  });
});

class FakePool implements CollabPool {
  queries: string[] = [];
  ended = false;

  constructor(readonly url: string, private readonly fail: boolean) {}

  async query<T extends pg.QueryResultRow = any>(text: string): Promise<pg.QueryResult<T>> {
    this.queries.push(text);
    if (this.fail) {
      throw new Error("candidate connection rejected");
    }
    return { rows: [], rowCount: 0, command: "SELECT", oid: 0, fields: [] } as pg.QueryResult<T>;
  }

  async end(): Promise<void> {
    this.ended = true;
  }
}

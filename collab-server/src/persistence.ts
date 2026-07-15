// Binary Yjs persistence into the shared Postgres (collab_documents table,
// owned exclusively by this process). The Database extension calls fetch on
// first document load and store on every debounced change.
import { Database } from "@hocuspocus/extension-database";
import pg from "pg";

import { initialSecret, readSecret } from "./credentials.js";
import type { Logger } from "./logger.js";

export function createPool(pgUrl: string): pg.Pool {
  return new pg.Pool({
    connectionString: pgUrl,
    max: 5,
    idleTimeoutMillis: 30000,
  });
}

const COLLAB_POOL_PREFLIGHT_SQL = `
WITH permission_probe AS (
  INSERT INTO collab_documents (name, state, updated_at)
  SELECT '__a2_permission_probe__', ''::bytea, now()
  WHERE false
  ON CONFLICT (name) DO UPDATE
    SET state = EXCLUDED.state, updated_at = EXCLUDED.updated_at
  RETURNING state
)
SELECT state FROM permission_probe`;

export interface CollabPool {
  query<T extends pg.QueryResultRow = any>(text: string, values?: unknown[]): Promise<pg.QueryResult<T>>;
  end(): Promise<void>;
}

export interface ReloadingPoolOptions {
  initialUrl: string;
  filePath?: string;
  logger: Logger;
  poolFactory?: (url: string) => CollabPool;
  readImpl?: typeof readSecret;
}

// ReloadingPool swaps a validated PostgreSQL pool atomically. Existing queries
// drain on the old pool while all new work immediately uses the new login role.
export class ReloadingPool implements CollabPool {
  private active: CollabPool;
  private activeUrl: string;
  private reloadPromise: Promise<void> | null = null;
  private readonly poolFactory: (url: string) => CollabPool;
  private readonly readImpl: typeof readSecret;

  constructor(private readonly opts: ReloadingPoolOptions) {
    this.activeUrl = opts.initialUrl;
    this.poolFactory = opts.poolFactory ?? createPool;
    this.readImpl = opts.readImpl ?? readSecret;
    this.active = this.poolFactory(this.activeUrl);
  }

  async query<T extends pg.QueryResultRow = any>(text: string, values?: unknown[]): Promise<pg.QueryResult<T>> {
    await this.maybeReload();
    return this.active.query<T>(text, values);
  }

  async end(): Promise<void> {
    await this.active.end();
  }

  private async maybeReload(): Promise<void> {
    if (!this.opts.filePath) {
      return;
    }
    this.reloadPromise ??= this.reloadCandidate().finally(() => {
      this.reloadPromise = null;
    });
    await this.reloadPromise;
  }

  private async reloadCandidate(): Promise<void> {
    let candidateUrl: string;
    try {
      candidateUrl = await this.readImpl(this.opts.filePath!, "COLLAB_PG_URL_FILE");
    } catch (err) {
      this.opts.logger.warn({ err: String(err) }, "database candidate unreadable; retaining active pool");
      return;
    }
    if (candidateUrl === this.activeUrl) {
      return;
    }

    const candidate = this.poolFactory(candidateUrl);
    try {
      await candidate.query(COLLAB_POOL_PREFLIGHT_SQL);
    } catch (err) {
      await candidate.end().catch(() => undefined);
      this.opts.logger.warn({ err: String(err) }, "database candidate rejected; retaining active pool");
      return;
    }

    const previous = this.active;
    this.active = candidate;
    this.activeUrl = candidateUrl;
    void previous.end().catch((err: unknown) => {
      this.opts.logger.warn({ err: String(err) }, "old database pool drain failed");
    });
    this.opts.logger.info("database role rotated without websocket restart");
  }
}

export async function createReloadingPool(
  directUrl: string | undefined,
  filePath: string | undefined,
  logger: Logger,
): Promise<ReloadingPool> {
  const initialUrl = await initialSecret(directUrl, filePath, "COLLAB_PG_URL");
  const pool = new ReloadingPool({ initialUrl, filePath, logger });
  await pool.query(COLLAB_POOL_PREFLIGHT_SQL);
  return pool;
}

export function createDatabaseExtension(pool: CollabPool, logger: Logger): Database {
  return new Database({
    fetch: async ({ documentName }) => {
      const result = await pool.query<{ state: Buffer }>(
        "SELECT state FROM collab_documents WHERE name = $1",
        [documentName],
      );
      if (result.rows.length === 0) {
        return null;
      }

      return new Uint8Array(result.rows[0].state);
    },
    store: async ({ documentName, state }) => {
      await pool.query(
        `INSERT INTO collab_documents (name, state, updated_at)
         VALUES ($1, $2, now())
         ON CONFLICT (name) DO UPDATE SET state = EXCLUDED.state, updated_at = now()`,
        [documentName, state],
      );
      logger.debug({ documentName, bytes: state.length }, "stored yjs state");
    },
  });
}

export async function pingDatabase(pool: CollabPool): Promise<void> {
  await pool.query("SELECT 1");
}

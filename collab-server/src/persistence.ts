// Binary Yjs persistence into the shared Postgres (collab_documents table,
// owned exclusively by this process). The Database extension calls fetch on
// first document load and store on every debounced change.
import { Database } from "@hocuspocus/extension-database";
import pg from "pg";

import type { Logger } from "./logger.js";

export function createPool(pgUrl: string): pg.Pool {
  return new pg.Pool({
    connectionString: pgUrl,
    max: 5,
    idleTimeoutMillis: 30000,
  });
}

export function createDatabaseExtension(pool: pg.Pool, logger: Logger): Database {
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

export async function pingDatabase(pool: pg.Pool): Promise<void> {
  await pool.query("SELECT 1");
}

// Surau collab-server: Hocuspocus (Yjs) websocket sync for the kitab page
// editor. Responsibilities per hook:
//
//   onRequest        GET /healthz (200 + pg ping)
//   onAuthenticate   validate doc name; introspect the user's access token
//                    against the Go API (role + revocable session checked
//                    there); editors/admins only; schedule connection close at
//                    token expiry + grace (client reconnects with fresh token)
//   onLoadDocument   if Postgres has no Yjs state yet, seed from the current
//                    Go draft (or raw page) via the internal API
//   onStoreDocument  debounced: Database extension persists binary state; this
//                    hook additionally converts Y.Doc -> HTML and PUTs it into
//                    the Go draft pipeline (sanitize/audit/revision, origin
//                    "collab")
//   onDestroy        close the pg pool
//
// The Go app stays the single write path for editorial drafts; this process
// only owns collab_documents (binary CRDT state).
import { Server, type onAuthenticatePayload } from "@hocuspocus/server";
import type { IncomingMessage, ServerResponse } from "node:http";

import { AuthError, canWrite, decodeTokenExp, introspect, type Identity } from "./auth.js";
import { loadConfig } from "./config.js";
import { initialSecret, ReloadingServiceToken } from "./credentials.js";
import { htmlToYDoc, yDocToHtml, verifyRoundTrip, Y_FRAGMENT_FIELD } from "./convert.js";
import { parseDocName } from "./docname.js";
import { GoApi } from "./goapi.js";
import { createLogger } from "./logger.js";
import { createDatabaseExtension, createReloadingPool, pingDatabase } from "./persistence.js";

const config = loadConfig();
const logger = createLogger(config.COLLAB_LOG_LEVEL);
const pool = await createReloadingPool(
  config.ALLOW_LEGACY_DB_CREDENTIALS ? config.COLLAB_PG_URL : undefined,
  config.COLLAB_PG_URL_FILE,
  logger,
);
const initialServiceToken = await initialSecret(
  config.COLLAB_SERVICE_TOKEN,
  config.COLLAB_SERVICE_TOKEN_FILE,
  "COLLAB_SERVICE_TOKEN",
);
const tokenProvider = new ReloadingServiceToken({
  initialToken: initialServiceToken,
  filePath: config.COLLAB_SERVICE_TOKEN_FILE,
  baseUrl: config.COLLAB_GO_API_URL,
  logger,
});
const goApi = new GoApi({
  baseUrl: config.COLLAB_GO_API_URL,
  tokenProvider,
  logger,
});
assertCollabIdentity(await goApi.whoami());

interface ConnectionContext {
  user: Identity;
  tokenExp: number | null;
}

// Last mutating editor per document, for draft attribution. Updated on every
// change; consumed by onStoreDocument. Connected editor ids ride along as
// contributors.
const lastEditor = new Map<string, string>();
const connectedEditors = new Map<string, Map<string, number>>();
const expiryTimers = new Map<string, NodeJS.Timeout>();

function trackEditor(documentName: string, userId: string): void {
  let editors = connectedEditors.get(documentName);
  if (!editors) {
    editors = new Map();
    connectedEditors.set(documentName, editors);
  }
  editors.set(userId, (editors.get(userId) ?? 0) + 1);
}

function untrackEditor(documentName: string, userId: string): void {
  const editors = connectedEditors.get(documentName);
  if (!editors) {
    return;
  }
  const count = (editors.get(userId) ?? 1) - 1;
  if (count <= 0) {
    editors.delete(userId);
  } else {
    editors.set(userId, count);
  }
  if (editors.size === 0) {
    connectedEditors.delete(documentName);
  }
}

const server = new Server({
  port: config.COLLAB_PORT,
  debounce: config.COLLAB_DEBOUNCE_MS,
  maxDebounce: config.COLLAB_DEBOUNCE_MAX_MS,
  // We own SIGTERM/SIGINT below so the pg pool closes after the final flush.
  stopOnSignals: false,
  // Only documents matching the naming scheme ever reach the hooks.
  extensions: [createDatabaseExtension(pool, logger)],

  async onRequest({ request, response }) {
    if (request.url === "/healthz") {
      await respondHealth(request, response);

      // Stop the chain: the request is fully handled.
      return Promise.reject();
    }

    return Promise.resolve();
  },

  async onAuthenticate({ documentName, token, socketId }: onAuthenticatePayload) {
    const doc = parseDocName(documentName);
    if (!doc) {
      throw new AuthError(`invalid document name: ${documentName}`);
    }
    if (!token) {
      throw new AuthError("missing token");
    }

    const identity = await introspect(config.COLLAB_GO_API_URL, token);
    if (!canWrite(identity.role)) {
      throw new AuthError("editor or admin role required");
    }

    // Access tokens are short-lived (15m). Close the connection shortly after
    // expiry; the HocuspocusProvider reconnects with a fresh token from its
    // token callback, and local Yjs state survives the reconnect.
    const exp = decodeTokenExp(token);
    if (exp !== null) {
      const closeInMs = exp * 1000 - Date.now() + config.COLLAB_TOKEN_GRACE_MS;
      const timer = setTimeout(() => {
        logger.info({ documentName, userId: identity.userId }, "closing connection: token expired");
        try {
          server.hocuspocus.documents
            .get(documentName)
            ?.getConnections()
            .forEach((conn) => {
              if (conn.socketId === socketId) {
                conn.close({ code: 4401, reason: "token expired" });
              }
            });
        } catch (err) {
          logger.warn({ err: String(err) }, "token-expiry close failed");
        }
      }, Math.max(closeInMs, 0));
      expiryTimers.set(socketId, timer);
    }

    trackEditor(documentName, identity.userId);
    logger.info(
      { documentName, userId: identity.userId, role: identity.role },
      "connection authenticated",
    );

    const context: ConnectionContext = { user: identity, tokenExp: exp };

    return context;
  },

  async onLoadDocument({ documentName, document }) {
    // The Database extension already applied stored binary state when present.
    if (!document.isEmpty(Y_FRAGMENT_FIELD)) {
      return document;
    }

    const doc = parseDocName(documentName);
    if (!doc) {
      return document;
    }

    const draft = await goApi.fetchPageDraft(doc.bookId, doc.pageId);
    const report = verifyRoundTrip(draft.content_html);
    if (!report.identical) {
      // Expected for legacy markup (b->strong, paragraph wrapping). The
      // pre-collab content is already captured as a source-edit revision in Go.
      logger.warn(
        { documentName, source: draft.source },
        "seed HTML normalizes through the schema (round-trip differs)",
      );
    }

    const seeded = htmlToYDoc(draft.content_html);
    logger.info({ documentName, source: draft.source }, "seeded document from Go draft");

    return seeded;
  },

  async onChange({ documentName, context }) {
    const ctx = context as ConnectionContext | undefined;
    if (ctx?.user) {
      lastEditor.set(documentName, ctx.user.userId);
    }
  },

  async onStoreDocument({ documentName, document }) {
    const doc = parseDocName(documentName);
    if (!doc) {
      return;
    }

    const html = yDocToHtml(document);
    const actorId = lastEditor.get(documentName);
    if (!actorId) {
      // No tracked mutation (e.g. store triggered right after seeding).
      return;
    }

    const contributors = [...(connectedEditors.get(documentName)?.keys() ?? [])];
    await goApi.putPageDraft(doc.bookId, doc.pageId, html, actorId, contributors);
    logger.info(
      { documentName, actorId, contributors: contributors.length, bytes: html.length },
      "synced collab document into Go draft pipeline",
    );
  },

  async onDisconnect({ documentName, context, socketId }) {
    const timer = expiryTimers.get(socketId);
    if (timer) {
      clearTimeout(timer);
      expiryTimers.delete(socketId);
    }

    const ctx = context as ConnectionContext | undefined;
    if (ctx?.user) {
      untrackEditor(documentName, ctx.user.userId);
    }
  },

  async onDestroy() {
    await pool.end();
    logger.info("collab-server stopped");
  },
});

async function respondHealth(_request: IncomingMessage, response: ServerResponse): Promise<void> {
  try {
    await pingDatabase(pool);
    const identity = assertCollabIdentity(await goApi.whoami());
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ status: "ok", principal: identity.principal_name }));
  } catch (err) {
    logger.error({ err: String(err) }, "healthcheck failed");
    response.writeHead(503, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ status: "degraded" }));
  }
}

function assertCollabIdentity(identity: Awaited<ReturnType<GoApi["whoami"]>>) {
  if (
    identity.principal_name !== "collab-server" ||
    !identity.scopes.includes("collab:draft:write")
  ) {
    throw new Error("service principal must be collab-server with collab:draft:write");
  }

  return identity;
}

async function shutdown(signal: string): Promise<void> {
  logger.info({ signal }, "shutting down: flushing documents");
  try {
    // destroy() awaits onStoreDocument for every loaded document (final flush).
    await server.destroy();
  } catch (err) {
    logger.error({ err: String(err) }, "shutdown error");
    process.exit(1);
  }
  process.exit(0);
}

process.on("SIGTERM", () => void shutdown("SIGTERM"));
process.on("SIGINT", () => void shutdown("SIGINT"));

void server.listen().then(() => {
  logger.info(
    { port: config.COLLAB_PORT, goApi: config.COLLAB_GO_API_URL },
    "collab-server listening",
  );
});

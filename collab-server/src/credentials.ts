import { readFile } from "node:fs/promises";

import type { Logger } from "./logger.js";

export async function readSecret(path: string, label: string): Promise<string> {
  const value = (await readFile(path, "utf8")).trim();
  if (!value) {
    throw new Error(`${label} file is empty`);
  }
  return value;
}

export async function initialSecret(
  direct: string | undefined,
  filePath: string | undefined,
  label: string,
): Promise<string> {
  if (filePath) {
    try {
      return await readSecret(filePath, label);
    } catch (err) {
      // During the single-release A-2 overlap, the old direct credential is
      // the rollback path if the root-owned file has not been materialized
      // yet. Once the file exists it still wins, and later reload failures
      // retain the last validated credential.
      if (!direct) {
        throw err;
      }
    }
  }
  if (direct) {
    return direct.trim();
  }
  throw new Error(`${label} is not configured`);
}

export interface TokenIdentity {
  principal_name: string;
  scopes: string[];
}

export interface ServiceTokenProvider {
  getToken(): Promise<string>;
}

export interface ReloadingServiceTokenOptions {
  initialToken: string;
  filePath?: string;
  baseUrl: string;
  logger: Logger;
  fetchImpl?: typeof fetch;
  readImpl?: typeof readSecret;
}

// ReloadingServiceToken implements the T1/T2 overlap cutover. A changed file
// becomes active only after /whoami proves it is the collab-server principal;
// an invalid candidate never replaces the last working credential.
export class ReloadingServiceToken implements ServiceTokenProvider {
  private activeToken: string;
  private reloadPromise: Promise<void> | null = null;
  private readonly fetchImpl: typeof fetch;
  private readonly readImpl: typeof readSecret;

  constructor(private readonly opts: ReloadingServiceTokenOptions) {
    this.activeToken = opts.initialToken;
    this.fetchImpl = opts.fetchImpl ?? fetch;
    this.readImpl = opts.readImpl ?? readSecret;
  }

  async getToken(): Promise<string> {
    if (this.opts.filePath) {
      this.reloadPromise ??= this.reloadCandidate().finally(() => {
        this.reloadPromise = null;
      });
      await this.reloadPromise;
    }
    return this.activeToken;
  }

  private async reloadCandidate(): Promise<void> {
    let candidate: string;
    try {
      candidate = await this.readImpl(this.opts.filePath!, "COLLAB_SERVICE_TOKEN_FILE");
    } catch (err) {
      this.opts.logger.warn({ err: String(err) }, "service-token candidate unreadable; retaining active token");
      return;
    }
    if (candidate === this.activeToken) {
      return;
    }

    try {
      const response = await this.fetchImpl(`${this.opts.baseUrl}/internal/collab/whoami`, {
        headers: { "X-Internal-Token": candidate },
        signal: AbortSignal.timeout(10000),
      });
      if (!response.ok) {
        throw new Error(`whoami returned ${response.status}`);
      }
      const identity = (await response.json()) as TokenIdentity;
      if (identity.principal_name !== "collab-server" || !identity.scopes.includes("collab:draft:write")) {
        throw new Error("candidate is not collab-server with collab:draft:write");
      }
      this.activeToken = candidate;
      this.opts.logger.info({ principal: identity.principal_name }, "service token rotated without restart");
    } catch (err) {
      this.opts.logger.warn({ err: String(err) }, "service-token candidate rejected; retaining active token");
    }
  }
}

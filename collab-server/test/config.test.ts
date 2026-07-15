import { describe, expect, it } from "vitest";

import { loadConfig } from "../src/config.js";

const baseEnv = {
  COLLAB_PG_URL: "postgres://owner-a/db",
  COLLAB_PG_URL_FILE: "/run/secrets/surau-collab/pg-url",
  COLLAB_GO_API_URL: "http://app:8080",
  COLLAB_SERVICE_TOKEN: "legacy-token-at-least-32-bytes-aaaa",
  COLLAB_SERVICE_TOKEN_FILE: "/run/secrets/surau-collab/service-token",
};

describe("collab credential overlap config", () => {
  it("disables the direct owner fallback by default", () => {
    expect(loadConfig(baseEnv).ALLOW_LEGACY_DB_CREDENTIALS).toBe(false);
  });

  it("enables the direct owner fallback only for literal true", () => {
    expect(loadConfig({
      ...baseEnv,
      ALLOW_LEGACY_DB_CREDENTIALS: "true",
    }).ALLOW_LEGACY_DB_CREDENTIALS).toBe(true);

    expect(() => loadConfig({
      ...baseEnv,
      ALLOW_LEGACY_DB_CREDENTIALS: "1",
    })).toThrow(/ALLOW_LEGACY_DB_CREDENTIALS/);
  });
});

import { z } from "zod";

const schema = z.object({
  COLLAB_PORT: z.coerce.number().int().positive().default(8090),
  COLLAB_PG_URL: z.string().min(1),
  COLLAB_GO_API_URL: z.string().url(),
  COLLAB_SERVICE_TOKEN: z.string().min(32),
  // Debounce window for persisting (binary state + HTML sync to Go).
  COLLAB_DEBOUNCE_MS: z.coerce.number().int().positive().default(3000),
  COLLAB_DEBOUNCE_MAX_MS: z.coerce.number().int().positive().default(10000),
  // Grace period after access-token expiry before the connection is closed.
  COLLAB_TOKEN_GRACE_MS: z.coerce.number().int().nonnegative().default(30000),
  COLLAB_LOG_LEVEL: z.string().default("info"),
});

export type Config = z.infer<typeof schema>;

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const parsed = schema.safeParse(env);
  if (!parsed.success) {
    const issues = parsed.error.issues
      .map((issue) => `${issue.path.join(".")}: ${issue.message}`)
      .join("; ");
    throw new Error(`invalid collab-server config: ${issues}`);
  }

  return parsed.data;
}

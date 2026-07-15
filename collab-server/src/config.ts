import { z } from "zod";

const optionalSecret = z.preprocess(
  (value) => (typeof value === "string" && value.trim() === "" ? undefined : value),
  z.string().min(1).optional(),
);

const schema = z.object({
  COLLAB_PORT: z.coerce.number().int().positive().default(8090),
  COLLAB_PG_URL: optionalSecret,
  COLLAB_PG_URL_FILE: optionalSecret,
  COLLAB_GO_API_URL: z.string().url(),
  COLLAB_SERVICE_TOKEN: z.preprocess(
    (value) => (typeof value === "string" && value.trim() === "" ? undefined : value),
    z.string().min(32).optional(),
  ),
  COLLAB_SERVICE_TOKEN_FILE: optionalSecret,
  // Debounce window for persisting (binary state + HTML sync to Go).
  COLLAB_DEBOUNCE_MS: z.coerce.number().int().positive().default(3000),
  COLLAB_DEBOUNCE_MAX_MS: z.coerce.number().int().positive().default(10000),
  // Grace period after access-token expiry before the connection is closed.
  COLLAB_TOKEN_GRACE_MS: z.coerce.number().int().nonnegative().default(30000),
  COLLAB_LOG_LEVEL: z.string().default("info"),
}).superRefine((value, ctx) => {
  if (!value.COLLAB_PG_URL && !value.COLLAB_PG_URL_FILE) {
    ctx.addIssue({ code: "custom", path: ["COLLAB_PG_URL_FILE"], message: "COLLAB_PG_URL or COLLAB_PG_URL_FILE is required" });
  }
  if (!value.COLLAB_SERVICE_TOKEN && !value.COLLAB_SERVICE_TOKEN_FILE) {
    ctx.addIssue({ code: "custom", path: ["COLLAB_SERVICE_TOKEN_FILE"], message: "COLLAB_SERVICE_TOKEN or COLLAB_SERVICE_TOKEN_FILE is required" });
  }
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

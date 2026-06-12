import { pino } from "pino";

export function createLogger(level: string) {
  return pino({
    level,
    base: { service: "collab-server" },
    timestamp: pino.stdTimeFunctions.isoTime,
  });
}

export type Logger = ReturnType<typeof createLogger>;

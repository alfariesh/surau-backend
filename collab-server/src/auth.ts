// Authentication bridge to the Go backend.
//
// The user's short-lived access token (HS256 JWT) is introspected against
// GET /v1/auth/introspect instead of being verified locally: role is not a JWT
// claim and sessions are revocable server-side (token_version, session table),
// so only the Go app can answer "is this token still good and who is it".
export interface Identity {
  userId: string;
  username: string;
  role: string;
  sessionId: string;
}

export class AuthError extends Error {}

export async function introspect(goApiUrl: string, token: string): Promise<Identity> {
  let response: Response;
  try {
    response = await fetch(`${goApiUrl}/v1/auth/introspect`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(5000),
    });
  } catch (err) {
    throw new AuthError(`introspection request failed: ${String(err)}`);
  }

  if (response.status === 401) {
    throw new AuthError("token rejected");
  }
  if (!response.ok) {
    throw new AuthError(`introspection returned ${response.status}`);
  }

  const body = (await response.json()) as {
    user_id?: string;
    username?: string;
    role?: string;
    session_id?: string;
  };
  if (!body.user_id || !body.role) {
    throw new AuthError("introspection response incomplete");
  }

  return {
    userId: body.user_id,
    username: body.username ?? "",
    role: body.role,
    sessionId: body.session_id ?? "",
  };
}

// decodeTokenExp reads the exp claim without verifying the signature — the Go
// backend already verified the token during introspection. Used only to
// schedule the connection close at expiry.
export function decodeTokenExp(token: string): number | null {
  const parts = token.split(".");
  if (parts.length !== 3) {
    return null;
  }

  try {
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8")) as {
      exp?: number;
    };

    return typeof payload.exp === "number" ? payload.exp : null;
  } catch {
    return null;
  }
}

export function canWrite(role: string): boolean {
  return role === "editor" || role === "admin";
}

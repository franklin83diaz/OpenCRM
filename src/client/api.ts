/**
 * Every call to the CRM's API. The session is an HttpOnly cookie the browser
 * sends by itself, so nothing here handles it. A 401 from any call means there
 * is no session any more (it expired, it was ended elsewhere, the account was
 * disabled): the one listener the app registers swaps to the login page.
 */

/** A failed call: the API's own `error` string (shown as is), with the HTTP status. */
export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
    this.name = "ApiError";
  }
}

let unauthorized: (() => void) | null = null;

/** Who hears a 401 (the app's sign-in state). One listener; returns its removal. */
export function onUnauthorized(listener: () => void): () => void {
  unauthorized = listener;
  return () => {
    if (unauthorized === listener) unauthorized = null;
  };
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const opts: RequestInit = { method, headers: {} };
  if (body) {
    (opts.headers as Record<string, string>)["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(path, opts);
  if (!r.ok) {
    // An error without a JSON body (a proxy's error page) still says it failed.
    const data = (await r.json().catch(() => ({}))) as { error?: string; message?: string };
    if (r.status === 401) unauthorized?.();
    // The unauthorized body is {"error": "unauthorized", "message": "Sign in to use the CRM."};
    // every other error, a wrong password included, is {"error": "<what to tell the person>"}.
    throw new ApiError((r.status === 401 && data.message) || data.error || "Request failed", r.status);
  }
  return (await r.json()) as T;
}

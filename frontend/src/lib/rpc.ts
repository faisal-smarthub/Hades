'use client';

import { Code, ConnectError } from '@connectrpc/connect';
import { clearToken, clearUsername, isSignedIn } from './auth';

// codeFromString maps a Connect JSON error code ("not_found") to its Code.
function codeFromString(s: string): Code | undefined {
  const name = s.replace(/(^|_)([a-z])/g, (_, __, c: string) => c.toUpperCase());
  const code = (Code as unknown as Record<string, Code>)[name];
  return typeof code === 'number' ? code : undefined;
}

// rpcFetch posts to the Next proxy route, which attaches the session token from
// the HttpOnly cookie. Nothing here reads the token: it is not readable, which
// is the point.
export async function rpcFetch<T>(path: string, body: unknown): Promise<T> {
  const url = path.startsWith('/api/rpc/') ? path : `/api/rpc${path.startsWith('/') ? '' : '/'}${path}`;
  let resp: Response;
  try {
    resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      credentials: 'same-origin',
    });
  } catch {
    throw new Error('Backend unreachable. Is the server running?');
  }

  if (resp.status === 401 && isSignedIn()) {
    await clearToken();
    clearUsername();
    window.location.href = '/login';
    throw new Error('Session expired');
  }

  if (!resp.ok) {
    const text = await resp.text();
    try {
      const json = JSON.parse(text);
      // A ConnectError, not a plain Error, so isNotFound and friends can see the
      // code. They checked for one that rpcFetch never threw, which left every
      // not-found branch dead and showed the server's message as an error.
      // message is reset to the server's text because ConnectError prefixes it
      // ("[not_found] ...") and callers render e.message directly.
      const code = typeof json?.code === 'string' ? codeFromString(json.code) : undefined;
      if (json?.message && code !== undefined) {
        const err = new ConnectError(json.message, code);
        err.message = json.message;
        throw err;
      }
      if (json?.message) throw new Error(json.message);
    } catch (e) {
      if (!(e instanceof SyntaxError)) throw e;
    }
    throw new Error(text || `Request failed: ${resp.status}`);
  }

  const contentType = resp.headers.get('content-type') ?? '';
  if (!contentType.includes('application/json') && !contentType.includes('application/proto')) {
    const text = await resp.text();
    if (text.includes('<!DOCTYPE') || text.includes('<html')) {
      throw new Error(`Backend unreachable - got HTML instead of JSON. Is the server running?`);
    }
    throw new Error(`Unexpected content-type: ${contentType}`);
  }

  return resp.json() as Promise<T>;
}

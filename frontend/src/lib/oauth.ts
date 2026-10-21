'use client';

import { rpcFetch } from './rpc';

// The providers the server's OAuthService accepts, keyed by the name used in
// the callback route (/oauth/<name>/callback) and in the server's redirectUrl
// config.
export const OAUTH_PROVIDERS = {
  github: 'OAUTH_PROVIDER_GITHUB',
  google: 'OAUTH_PROVIDER_GOOGLE',
} as const;

export type OAuthProviderName = keyof typeof OAUTH_PROVIDERS;

export function isOAuthProviderName(v: string): v is OAuthProviderName {
  return Object.prototype.hasOwnProperty.call(OAUTH_PROVIDERS, v);
}

// startOAuth sends the browser to the provider's authorization page.
//
// The server generates and records the state value itself (see GetOAuthURL),
// so there is nothing to keep client-side: the callback page hands back
// whatever state the provider echoes and the server checks it.
export async function startOAuth(provider: OAuthProviderName): Promise<void> {
  const { url } = await rpcFetch<{ url?: string }>('/hades.api.auth.v1.OAuthService/GetOAuthURL', {
    provider: OAUTH_PROVIDERS[provider],
  });
  if (!url) throw new Error('No authorization URL returned');
  window.location.assign(url);
}

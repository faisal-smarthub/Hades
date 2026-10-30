'use client';
import React, { Suspense, useEffect, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { useAuthStore } from '@/stores/authStore';
import { rpcFetch } from '@/lib/rpc';
import { formatError } from '@/lib/connectError';
import { OAUTH_PROVIDERS, isOAuthProviderName } from '@/lib/oauth';
import Card from '@/components/ui/Card';
import { IconAlert } from '@/components/icons';

interface OAuthCallbackResponse {
  login?: { token?: string; pendingTotp?: boolean };
  username?: string;
}

// completeOAuth exchanges the provider's code for a Hades session token and
// the signed-in username. The username comes from the server because a first
// login may have created the account under a name the user never typed.
async function completeOAuth(provider: string, code: string, state: string): Promise<{ token: string; username: string }> {
  const data = await rpcFetch<OAuthCallbackResponse>('/hades.api.auth.v1.OAuthService/OAuthCallback', {
    provider,
    code,
    state,
  });
  const token = data.login?.token;
  if (!token || !data.username) throw new Error('Incomplete sign-in response from the server');
  if (data.login?.pendingTotp) {
    // The token is only good for VerifyTOTP, which the web UI does not offer
    // yet; installing it would sign the user into a session every page rejects.
    throw new Error('This account has two-factor authentication enabled, which web sign-in does not support yet.');
  }
  return { token, username: data.username };
}

function OAuthCallbackContent() {
  const { setAuth } = useAuthStore();
  const params = useParams<{ provider: string }>();
  const searchParams = useSearchParams();
  const [error, setError] = useState('');
  // The state value is single-use on the server, so a second run of this
  // effect (React strict mode runs effects twice in development) would fail
  // the login it is racing.
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    const providerName = params.provider;
    if (!isOAuthProviderName(providerName)) {
      setError(`Unknown sign-in provider "${providerName}".`);
      return;
    }
    // The provider redirects here with error=... instead of a code when the
    // user declines or the app is misconfigured.
    const providerError = searchParams.get('error');
    if (providerError) {
      setError(searchParams.get('error_description') || `Sign-in was not completed (${providerError}).`);
      return;
    }
    const code = searchParams.get('code');
    const state = searchParams.get('state');
    if (!code || !state) {
      setError('The sign-in response is missing its code or state. Start again from the sign-in page.');
      return;
    }

    completeOAuth(OAUTH_PROVIDERS[providerName], code, state)
      .then(async ({ token, username }) => {
        await setAuth(token, username);
        // replace, not push: the code and state are spent, so going back to
        // this URL can only fail. A full page load, not router.replace, for the
        // same reason as login: a soft navigation can replay a redirect to
        // /login cached while signed out.
        window.location.replace('/');
      })
      .catch(e => setError(formatError(e)));
  }, [params.provider, searchParams, setAuth]);

  return (
    <Card style={{ padding: '32px 36px' }}>
      {error ? (
        <>
          <h2 style={{ margin: '0 0 16px', fontSize: 20, fontWeight: 700, color: 'var(--c-fg)', letterSpacing: -0.3 }}>Sign-in failed</h2>
          <div role="alert" style={{ padding: '10px 14px', borderRadius: 6, background: 'var(--c-danger-bg)', border: '1px solid var(--c-danger)', color: 'var(--c-danger)', fontSize: 13, marginBottom: 20, display: 'flex', gap: 8, alignItems: 'center' }}>
            <IconAlert size={14}/>{error}
          </div>
          <Link href="/login" style={{ fontSize: 13, color: 'var(--c-accent)', fontWeight: 500 }}>Back to sign in</Link>
        </>
      ) : (
        <p style={{ margin: 0, fontSize: 14, color: 'var(--c-fg-muted)' }}>Signing you in…</p>
      )}
    </Card>
  );
}

export default function PageOAuthCallback() {
  return (
    <div style={{ minHeight: '100vh', background: 'var(--c-bg-canvas)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '32px 16px' }}>
      <div style={{ width: '100%', maxWidth: 420 }}>
        <Suspense fallback={null}>
          <OAuthCallbackContent/>
        </Suspense>
      </div>
    </div>
  );
}

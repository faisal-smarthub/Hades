'use client';
import { useEffect, useState } from 'react';
import { rpcFetch } from './rpc';

// useSignupEnabled reports whether the server will currently accept a new
// account, per auth.firstUserOnlySignup (see GetSignupStatus). Starts true
// so the signup entry points don't flicker away then back on a fast check;
// a failed check is treated as enabled too, since GetSignupStatus is
// advisory UI guidance and Register is still the real gate either way.
export function useSignupEnabled(): boolean {
  const [enabled, setEnabled] = useState(true);

  useEffect(() => {
    rpcFetch<{ enabled: boolean }>('/hades.api.auth.v1.AuthenticationService/GetSignupStatus', {})
      .then(data => setEnabled(data.enabled !== false))
      .catch(() => {});
  }, []);

  return enabled;
}

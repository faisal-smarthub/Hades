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
    rpcFetch<{ enabled?: boolean }>('/hades.api.auth.v1.AuthenticationService/GetSignupStatus', {})
      // protobuf JSON omits a bool field entirely when it's false, so a
      // closed signup response is the literal body "{}", not
      // {"enabled":false}. `!== false` treated that missing field as "not
      // false" and always read as open; `=== true` is the only check that
      // matches protobuf JSON's own default-omission rule.
      .then(data => setEnabled(data.enabled === true))
      .catch(() => {});
  }, []);

  return enabled;
}

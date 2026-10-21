import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';

// Hades has no public-browsing mode: every page requires a session, not just
// settings. Visiting signed out lands on /login instead of a working app
// shell with nothing to show.
//
// /login and /signup are exempted because they're what establishes a
// session in the first place, and /verify-email/* because that link is
// opened from an email client in whatever browser the user has, not
// necessarily a logged-in one. /oauth/* is where GitHub and Google send the
// browser back after sign-in, which is necessarily before a session exists.
// The matcher below already excludes /api/*
// (the proxy routes /login and /signup themselves call to reach the
// backend) and /oauth2/* (the buf CLI's own token exchange, authenticated
// by its own Bearer header rather than a browser cookie).
const PUBLIC_PATHS = new Set(['/login', '/signup']);

export function proxy(req: NextRequest) {
  const { pathname } = req.nextUrl;
  if (PUBLIC_PATHS.has(pathname) || pathname.startsWith('/verify-email/') || pathname.startsWith('/oauth/')) {
    return NextResponse.next();
  }

  const token = req.cookies.get('hades_token')?.value;
  if (!token) {
    return NextResponse.redirect(new URL('/login', req.url));
  }
  return NextResponse.next();
}

export const config = {
  // Everything except Next's own static/image assets, /api/*, /oauth2/*, and
  // any request for a file with an extension (favicon.svg, icons.svg, …),
  // which are public by nature and would otherwise 404 the login page's own
  // asset requests.
  matcher: ['/((?!_next/static|_next/image|api/|oauth2/|.*\\.[^/]+$).*)'],
};

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStubProviderClient returns an http.Client whose requests are all served by
// the given handler, so the provider API can be faked without touching the
// network.
func newStubProviderClient(t *testing.T, handler http.HandlerFunc) (*http.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client := srv.Client()
	client.Transport = rewriteHost{base: client.Transport, target: srv.Listener.Addr().String()}
	return client, srv.Close
}

// rewriteHost redirects every request to the test server regardless of the URL
// the caller asked for.
type rewriteHost struct {
	base   http.RoundTripper
	target string
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = r.target
	return r.base.RoundTrip(clone)
}

func TestFetchGitHubProfile_UsesVerifiedPrimaryEmail(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id": 4242, "login": "alice", "email": "public-but-unverified@example.com"}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[
				{"email": "other@example.com", "primary": false, "verified": true},
				{"email": "real@example.com", "primary": true, "verified": true}
			]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer closeFn()

	uid, email, login, err := fetchGitHubProfile(client)

	require.NoError(t, err)
	assert.Equal(t, "4242", uid)
	assert.Equal(t, "alice", login)
	// The profile email must be ignored: any GitHub account can set it to an
	// address it does not own, which would allow claiming another Hades account.
	assert.Equal(t, "real@example.com", email)
}

func TestFetchGitHubProfile_RejectsUnverifiedPrimaryEmail(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id": 4242, "login": "alice"}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[{"email": "victim@example.com", "primary": true, "verified": false}]`))
		}
	})
	defer closeFn()

	_, _, _, err := fetchGitHubProfile(client)

	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestFetchGitHubProfile_RejectsNonOKStatus(t *testing.T) {
	// A 401 body unmarshals cleanly into the zero-value struct, producing
	// uid "0" and an empty email, which would then be looked up as a real
	// identity. The status must be checked before parsing.
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message": "Bad credentials"}`))
	})
	defer closeFn()

	uid, email, login, err := fetchGitHubProfile(client)

	require.Error(t, err)
	assert.Empty(t, uid)
	assert.Empty(t, email)
	assert.Empty(t, login)
}

func TestFetchGitHubProfile_RejectsZeroAccountID(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	defer closeFn()

	_, _, _, err := fetchGitHubProfile(client)
	require.Error(t, err)
}

func TestFetchGoogleProfile_RequiresEmailVerified(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sub": "1234", "email": "victim@example.com", "email_verified": false}`))
	})
	defer closeFn()

	_, _, err := fetchGoogleProfile(client)

	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestFetchGoogleProfile_AcceptsVerifiedEmail(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sub": "1234", "email": "real@example.com", "email_verified": true}`))
	})
	defer closeFn()

	uid, email, err := fetchGoogleProfile(client)

	require.NoError(t, err)
	assert.Equal(t, "1234", uid)
	assert.Equal(t, "real@example.com", email)
}

func TestFetchGoogleProfile_RejectsNonOKStatus(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error": "denied"}`))
	})
	defer closeFn()

	_, _, err := fetchGoogleProfile(client)
	require.Error(t, err)
}

func TestFetchGoogleProfile_RejectsEmptySubject(t *testing.T) {
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"email": "a@example.com", "email_verified": true}`))
	})
	defer closeFn()

	_, _, err := fetchGoogleProfile(client)
	require.Error(t, err)
}

// stubOrgMemberships serves /user/memberships/orgs/{org} from a map of org to
// status code and body. Orgs not in the map answer 404, as GitHub does for a
// non-member. It records which orgs were asked about.
func stubOrgMemberships(t *testing.T, responses map[string]struct {
	status int
	body   string
}) (*http.Client, *[]string, func()) {
	t.Helper()
	var asked []string
	client, closeFn := newStubProviderClient(t, func(w http.ResponseWriter, r *http.Request) {
		org, ok := strings.CutPrefix(r.URL.Path, "/user/memberships/orgs/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = append(asked, org)
		resp, ok := responses[org]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	})
	return client, &asked, closeFn
}

func TestRequireGitHubOrgMembership_EmptyAllowListAllowsAnyoneWithoutAsking(t *testing.T) {
	client, asked, closeFn := stubOrgMemberships(t, nil)
	defer closeFn()

	require.NoError(t, requireGitHubOrgMembership(client, nil))
	assert.Empty(t, *asked, "no membership request is needed without an allow-list")
}

func TestRequireGitHubOrgMembership_ActiveMemberIsAllowed(t *testing.T) {
	client, _, closeFn := stubOrgMemberships(t, map[string]struct {
		status int
		body   string
	}{
		"smarthub": {http.StatusOK, `{"state": "active", "role": "member"}`},
	})
	defer closeFn()

	require.NoError(t, requireGitHubOrgMembership(client, []string{"smarthub"}))
}

// Any one allowed org is enough; a 404 from an earlier org is not a failure.
func TestRequireGitHubOrgMembership_MemberOfLaterOrgIsAllowed(t *testing.T) {
	client, asked, closeFn := stubOrgMemberships(t, map[string]struct {
		status int
		body   string
	}{
		"second": {http.StatusOK, `{"state": "active"}`},
	})
	defer closeFn()

	require.NoError(t, requireGitHubOrgMembership(client, []string{"first", "second"}))
	assert.Equal(t, []string{"first", "second"}, *asked)
}

func TestRequireGitHubOrgMembership_NonMemberIsDenied(t *testing.T) {
	client, _, closeFn := stubOrgMemberships(t, nil)
	defer closeFn()

	err := requireGitHubOrgMembership(client, []string{"smarthub"})
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// A pending membership is an invitation the user has not accepted.
func TestRequireGitHubOrgMembership_PendingInvitationIsDenied(t *testing.T) {
	client, _, closeFn := stubOrgMemberships(t, map[string]struct {
		status int
		body   string
	}{
		"smarthub": {http.StatusOK, `{"state": "pending"}`},
	})
	defer closeFn()

	err := requireGitHubOrgMembership(client, []string{"smarthub"})
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// GitHub answers 403 when the org has not approved this OAuth app. That is
// still a refusal, but its message must point the operator at the cause.
func TestRequireGitHubOrgMembership_UnapprovedAppIsDeniedWithHint(t *testing.T) {
	client, _, closeFn := stubOrgMemberships(t, map[string]struct {
		status int
		body   string
	}{
		"smarthub": {http.StatusForbidden, `{"message": "Although you appear to have the correct authorization credentials, the organization has enabled OAuth App access restrictions"}`},
	})
	defer closeFn()

	err := requireGitHubOrgMembership(client, []string{"smarthub"})
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "approve this application")
}

// A GitHub outage must not be reported as the user being outside the org.
func TestRequireGitHubOrgMembership_ServerErrorIsUnavailable(t *testing.T) {
	client, _, closeFn := stubOrgMemberships(t, map[string]struct {
		status int
		body   string
	}{
		"smarthub": {http.StatusBadGateway, ``},
	})
	defer closeFn()

	err := requireGitHubOrgMembership(client, []string{"smarthub"})
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestGitHubScopes_RequestsReadOrgOnlyWithAnAllowList(t *testing.T) {
	assert.Equal(t, []string{"user:email"}, gitHubScopes(nil))
	assert.Equal(t, []string{"user:email", "read:org"}, gitHubScopes([]string{"smarthub"}))
}

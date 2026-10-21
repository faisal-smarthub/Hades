package config

// AuthConfig configures authentication subsystems: passwords, sessions,
// lockout behaviour, email verification, and password reset.
type AuthConfig struct {
	Password          PasswordConfig      `json:"password" yaml:"password"`
	Session           SessionConfig       `json:"session" yaml:"session"`
	Lockout           LockoutConfig       `json:"lockout" yaml:"lockout"`
	EmailVerification EmailVerifConfig    `json:"emailVerification" yaml:"emailVerification"`
	PasswordReset     PasswordResetConfig `json:"passwordReset" yaml:"passwordReset"`
	// FirstUserOnlySignup, once true, allows Register to succeed only while no
	// user account exists yet. The first account created closes signup
	// permanently (until this is set back to false) with no further action
	// needed, which suits a single-admin deployment where self-service signup
	// is never wanted again after bootstrap.
	//
	// False by default: open signup, the behaviour before this flag existed.
	FirstUserOnlySignup bool `json:"firstUserOnlySignup" yaml:"firstUserOnlySignup"`
}

type PasswordConfig struct {
	MinLength  int `json:"minLength" yaml:"minLength"`
	BcryptCost int `json:"bcryptCost" yaml:"bcryptCost"`
}

type SessionConfig struct {
	IdleTimeoutDays     int `json:"idleTimeoutDays" yaml:"idleTimeoutDays"`
	AbsoluteTimeoutDays int `json:"absoluteTimeoutDays" yaml:"absoluteTimeoutDays"`
	// Session tokens are deliberately non-rotating; see session.Storage. There
	// is no rotation grace window to configure.
}

type LockoutConfig struct {
	MaxAttempts     int `json:"maxAttempts" yaml:"maxAttempts"`
	CooldownMinutes int `json:"cooldownMinutes" yaml:"cooldownMinutes"`
}

type EmailVerifConfig struct {
	TokenExpiryHours int `json:"tokenExpiryHours" yaml:"tokenExpiryHours"`
	// AutoVerify marks every newly registered address as verified without the
	// user proving they control it.
	//
	// It exists for development, where email.stub is true so no mail is ever
	// delivered and there is nothing for anyone to click.
	//
	// False by default.
	AutoVerify bool `json:"autoVerify" yaml:"autoVerify"`
}

type PasswordResetConfig struct {
	TokenExpiryHours int `json:"tokenExpiryHours" yaml:"tokenExpiryHours"`
}

type EmailConfig struct {
	Stub bool       `json:"stub" yaml:"stub"`
	From string     `json:"from" yaml:"from"`
	SMTP SMTPConfig `json:"smtp" yaml:"smtp"`
}

type SMTPConfig struct {
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

type OAuthConfig struct {
	GitHub GitHubOAuthProvider `json:"github" yaml:"github"`
	Google OAuthProvider       `json:"google" yaml:"google"`

	// AllowAccountLinkingByEmail permits a first-time OAuth login to take over
	// an existing local account whose email address matches the one the
	// provider reports as verified.
	//
	// It defaults to false. With it on, a compromise of the user's provider
	// account, or a provider that mis-reports verification, is a full takeover
	// of the Hades account including every organisation role it holds, with no
	// confirmation step. Turn it on only where the identity provider is the
	// same trust domain as the registry.
	AllowAccountLinkingByEmail bool `json:"allowAccountLinkingByEmail" yaml:"allowAccountLinkingByEmail"`
}

type OAuthProvider struct {
	ClientID     string `json:"clientId" yaml:"clientId"`
	ClientSecret string `json:"clientSecret" yaml:"clientSecret"`
	RedirectURL  string `json:"redirectUrl" yaml:"redirectUrl"`
}

// GitHubOAuthProvider is an OAuthProvider plus GitHub-only access controls.
type GitHubOAuthProvider struct {
	OAuthProvider `yaml:",inline"`

	// AllowedOrgs, when non-empty, restricts GitHub login to accounts that are
	// active members of at least one of these GitHub organisations. Membership
	// is checked on every login, not only the first, so someone removed from
	// the org can no longer sign in with GitHub. Sessions and API tokens they
	// already hold are not revoked by this.
	//
	// Setting it adds the read:org scope to the GitHub authorization request.
	// An org that restricts third-party application access must approve the
	// OAuth app, or every membership check fails and nobody can sign in.
	//
	// It also lets members create accounts through GitHub while
	// auth.firstUserOnlySignup keeps password signup closed: a login that has
	// already passed an org check is not open signup.
	//
	// Empty by default: any GitHub account may sign in.
	AllowedOrgs []string `json:"allowedOrgs" yaml:"allowedOrgs"`
}

type RedisConfig struct {
	Addr     string `json:"addr" yaml:"addr"`
	Password string `json:"password" yaml:"password"`
	DB       int    `json:"db" yaml:"db"`
}

// TOTPConfig configures time-based one-time password (TOTP) two-factor authentication.
type TOTPConfig struct {
	EncryptionKey string `json:"encryptionKey" yaml:"encryptionKey"` // 32-byte hex AES-256 key; encrypts TOTP secrets at rest
	Issuer        string `json:"issuer" yaml:"issuer"`
}

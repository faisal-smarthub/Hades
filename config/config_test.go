package config_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/alipourhabibi/Hades/config"
)

// TestContainerYAMLStartsWithNoOperatorInput pins the claim CLAUDE.md makes
// about the image: container.yaml "starts with no operator input and nothing
// else running".
//
// It did not. registryHost read "${HADES_REGISTRY_HOST}" with no default, an
// unset variable expands to the empty string, and Validate rejects that, so
// `docker run ghcr.io/alipourhabibi/hades` exited immediately on a config
// error. Both spellings are covered here, unset and set-but-empty, because the
// expansion used to treat them differently and only the first was ever tried.
func TestContainerYAMLStartsWithNoOperatorInput(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		if _, err := config.LoadFile("container.yaml"); err != nil {
			t.Fatalf("container.yaml must load with no environment: %v", err)
		}
	})

	t.Run("set but empty", func(t *testing.T) {
		t.Setenv("HADES_REGISTRY_HOST", "")
		t.Setenv("HADES_TOTP_ENCRYPTION_KEY", "")
		if _, err := config.LoadFile("container.yaml"); err != nil {
			t.Fatalf("an empty variable must fall back to the default: %v", err)
		}
	})

	t.Run("set", func(t *testing.T) {
		t.Setenv("HADES_REGISTRY_HOST", "registry.example.com")
		cfg, err := config.LoadFile("container.yaml")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Server.RegistryHost != "registry.example.com" {
			t.Fatalf("registryHost = %q, want the environment value", cfg.Server.RegistryHost)
		}
	})
}

// The GitHub provider embeds OAuthProvider, so its client fields must still be
// read from the same flat YAML keys alongside allowedOrgs.
func TestOAuthGitHubAllowedOrgsParsesAlongsideClientFields(t *testing.T) {
	var cfg config.OAuthConfig
	err := yaml.Unmarshal([]byte(`
github:
  clientId: "id"
  clientSecret: "secret"
  redirectUrl: "https://example.com/cb"
  allowedOrgs: ["smarthub", "other-org"]
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.GitHub.ClientID != "id" || cfg.GitHub.ClientSecret != "secret" || cfg.GitHub.RedirectURL != "https://example.com/cb" {
		t.Fatalf("client fields not read: %+v", cfg.GitHub.OAuthProvider)
	}
	if len(cfg.GitHub.AllowedOrgs) != 2 || cfg.GitHub.AllowedOrgs[0] != "smarthub" || cfg.GitHub.AllowedOrgs[1] != "other-org" {
		t.Fatalf("allowedOrgs = %v", cfg.GitHub.AllowedOrgs)
	}
}

// Org names are interpolated into a GitHub API path, so Validate must reject
// anything that is not a GitHub login.
func TestValidateRejectsInvalidGitHubAllowedOrgs(t *testing.T) {
	for _, tc := range []struct {
		org   string
		valid bool
	}{
		{"smarthub", true},
		{"Smart-Hub-42", true},
		{"a", true},
		{"", false},
		{"-leading", false},
		{"trailing-", false},
		{"../admin", false},
		{"org/other", false},
		{"has space", false},
		{"this-org-name-is-much-too-long-for-github", false},
	} {
		t.Run(tc.org, func(t *testing.T) {
			cfg, err := config.LoadFile("container.yaml")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			cfg.OAuth.GitHub.AllowedOrgs = []string{tc.org}
			err = cfg.Validate()
			if tc.valid && err != nil {
				t.Fatalf("%q must be accepted: %v", tc.org, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("%q must be rejected", tc.org)
			}
		})
	}
}

// Admin is deliberately not an auto-join role, and an empty org name is a
// config mistake, not "no org".
func TestValidateAutoJoinOrgs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry config.AutoJoinOrg
		valid bool
	}{
		{"default role", config.AutoJoinOrg{Org: "smarthub"}, true},
		{"reader", config.AutoJoinOrg{Org: "smarthub", Role: "reader"}, true},
		{"member", config.AutoJoinOrg{Org: "smarthub", Role: "member"}, true},
		{"admin", config.AutoJoinOrg{Org: "smarthub", Role: "admin"}, false},
		{"owner", config.AutoJoinOrg{Org: "smarthub", Role: "owner"}, false},
		{"no org", config.AutoJoinOrg{Role: "reader"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.LoadFile("container.yaml")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			cfg.Auth.AutoJoinOrgs = []config.AutoJoinOrg{tc.entry}
			err = cfg.Validate()
			if tc.valid && err != nil {
				t.Fatalf("%+v must be accepted: %v", tc.entry, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("%+v must be rejected", tc.entry)
			}
		})
	}
}

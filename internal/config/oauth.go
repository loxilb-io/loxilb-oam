package config

import (
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

// OAuth is an experimental, opt-in feature. It is disabled by default (most
// deployments are on-premise and do not use it); the OAuth routes are only
// registered and configured when OAM_OAUTH_ENABLED=true.
//
// OAuthEnabled reports whether OAuth login is turned on.
func OAuthEnabled() bool { return os.Getenv("OAM_OAUTH_ENABLED") == "true" }

// OAuth client credentials are loaded from the environment in InitOAuthConfigs
// (OAM_OAUTH_<PROVIDER>_CLIENT_ID / _CLIENT_SECRET); redirect URLs are supplied
// at runtime. Nothing is committed here. Providers with empty credentials are
// effectively disabled even when OAuth is enabled.
var OAuthConfigs = map[string]*oauth2.Config{
	"google": {
		Scopes:   []string{"email", "profile"},
		Endpoint: google.Endpoint,
	},
	"github": {
		Scopes:   []string{"user:email"},
		Endpoint: github.Endpoint,
	},
	"facebook": {
		Scopes:   []string{"email"},
		Endpoint: facebook.Endpoint,
	},
}

func InitOAuthConfigs(googleRedirectURL, githubRedirectURL, facebookRedirectURL string) {
	applyOAuthEnv := func(provider, envPrefix, redirectURL string) {
		cfg := OAuthConfigs[provider]
		cfg.RedirectURL = redirectURL
		if id := os.Getenv(envPrefix + "_CLIENT_ID"); id != "" {
			cfg.ClientID = id
		}
		if secret := os.Getenv(envPrefix + "_CLIENT_SECRET"); secret != "" {
			cfg.ClientSecret = secret
		}
		OAuthConfigs[provider] = cfg
	}
	applyOAuthEnv("google", "OAM_OAUTH_GOOGLE", googleRedirectURL)
	applyOAuthEnv("github", "OAM_OAUTH_GITHUB", githubRedirectURL)
	applyOAuthEnv("facebook", "OAM_OAUTH_FACEBOOK", facebookRedirectURL)
}

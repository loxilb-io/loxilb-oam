# OAuth2 Configuration (`internal/config/oauth.go`)

`oauth.go` defines OAuth2 login configurations for Google, GitHub, and Facebook.

> **OAuth is experimental and disabled by default.** Most deployments are
> on-premise and do not use it. The OAuth routes (`/oam/oauth/...`) are only
> registered when `OAM_OAUTH_ENABLED=true`; otherwise they are absent. New
> deployments should not depend on OAuth.

## Enabling OAuth

Set `OAM_OAUTH_ENABLED=true`, then configure at least one provider's
credentials below. When disabled (the default), no OAuth configuration is
loaded and no OAuth endpoints are exposed.

## Credentials come from the environment

Client credentials are **never** committed to source. They are read from
environment variables at startup by `InitOAuthConfigs`:

| Provider | Client ID env var            | Client Secret env var            |
|----------|------------------------------|----------------------------------|
| Google   | `OAM_OAUTH_GOOGLE_CLIENT_ID` | `OAM_OAUTH_GOOGLE_CLIENT_SECRET` |
| GitHub   | `OAM_OAUTH_GITHUB_CLIENT_ID` | `OAM_OAUTH_GITHUB_CLIENT_SECRET` |
| Facebook | `OAM_OAUTH_FACEBOOK_CLIENT_ID` | `OAM_OAUTH_FACEBOOK_CLIENT_SECRET` |

If a provider's variables are empty, that provider is disabled.

## Scopes

| Provider | Scopes            |
|----------|-------------------|
| Google   | `email`, `profile` |
| GitHub   | `user:email`      |
| Facebook | `email`           |

## Redirect URLs

Redirect (callback) URLs are supplied at runtime, not through the environment
variables above. `InitOAuthConfigs` receives them as arguments (wired from the
`-google-redirect-url`, `-github-redirect-url`, and `-facebook-redirect-url`
CLI flags) so the same build can serve different deployment hostnames:

```go
func InitOAuthConfigs(googleRedirectURL, githubRedirectURL, facebookRedirectURL string) {
    // For each provider: set RedirectURL from the argument, then overlay
    // ClientID / ClientSecret from OAM_OAUTH_<PROVIDER>_CLIENT_{ID,SECRET}.
    // ...
}
```

## Notes

- **Never hardcode client secrets.** Supply them only through the environment.
  Any previously committed secrets must be treated as leaked and rotated at the
  provider.
- Redirect URLs are per-deployment and set at runtime via CLI flags.

## References

- [Google OAuth2 Documentation](https://developers.google.com/identity/protocols/oauth2)
- [GitHub OAuth2 Documentation](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)
- [Facebook Login Documentation](https://developers.facebook.com/docs/facebook-login/)

package config

import "testing"

func TestSecretIsPlaceholder(t *testing.T) {
	for _, value := range []string{
		"", "   ", "\t",
		"# e.g. `openssl rand -base64 48`",
		"# bootstrap admin; change after first login.",
		"  # note",
		"CHANGE_ME", "change-me", "changeme", "Change Me", "#changeme",
		"Ch4nge-me!", "replace_me", "placeholder", "secret", "<secret>",
		"<bootstrap-admin-password>", "<the value you put in .env>",
		"...", "…", "password", "your-password", "xxx",
	} {
		if !SecretIsPlaceholder(value) {
			t.Errorf("SecretIsPlaceholder(%q) = false, want true", value)
		}
	}
}

func TestSecretIsPlaceholderAcceptsRealValues(t *testing.T) {
	for _, value := range []string{
		"startup-test-only-secret-32-bytes-long",
		"ci-integration-jwt-secret",
		"ChangeMe-Admin1!",
		"oampass",
		"Valid!Oam7462",
		"#Generated9Pass",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"correct horse battery staple",
	} {
		if SecretIsPlaceholder(value) {
			t.Errorf("SecretIsPlaceholder(%q) = true, want false", value)
		}
	}
}

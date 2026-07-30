package config

import (
	"fmt"
	"os"
)

// Docker Engine API connection settings for driving LoxiLB instance hosts.
//
// By default the API is reached over plaintext on port 2375 (the historical
// behavior). For production, enable TLS with OAM_DOCKER_TLS=true and provide
// client certificates via OAM_DOCKER_CERT_PATH; the port can be overridden with
// OAM_DOCKER_PORT.

// DockerScheme returns "https" when OAM_DOCKER_TLS=true, otherwise "http".
func DockerScheme() string {
	if os.Getenv("OAM_DOCKER_TLS") == "true" {
		return "https"
	}
	return "http"
}

// DockerAPIPort returns the Docker Engine API port (OAM_DOCKER_PORT, default 2375).
func DockerAPIPort() string {
	if v := os.Getenv("OAM_DOCKER_PORT"); v != "" {
		return v
	}
	return "2375"
}

// DockerBaseURL builds the Docker Engine API base URL for the given host.
func DockerBaseURL(host string) string {
	return fmt.Sprintf("%s://%s:%s", DockerScheme(), host, DockerAPIPort())
}

// DockerTLSEnabled reports whether the Docker API is driven over TLS.
func DockerTLSEnabled() bool { return os.Getenv("OAM_DOCKER_TLS") == "true" }

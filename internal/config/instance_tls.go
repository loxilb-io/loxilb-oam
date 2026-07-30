package config

import (
	"crypto/tls"
	"crypto/x509"
	"os"
)

// Environment variables controlling TLS for outbound connections to managed
// LoxiLB instances (the gateway proxy and the snapshot client).
const (
	instanceCABundleEnv   = "OAM_INSTANCE_CA_BUNDLE"    // path to a PEM CA bundle to trust
	instanceTLSInsecureEnv = "OAM_INSTANCE_TLS_INSECURE" // "true" disables verification (dev only)
)

// InstanceTLSInsecure reports whether server-certificate verification is
// disabled for connections to managed instances.
func InstanceTLSInsecure() bool { return os.Getenv(instanceTLSInsecureEnv) == "true" }

// InstanceTLSConfig returns the TLS configuration used for outbound connections
// to managed LoxiLB instances.
//
// By default, server certificates are verified against the system roots. Set
// OAM_INSTANCE_CA_BUNDLE to a PEM file to trust a private CA (recommended for
// self-signed instance certificates). Set OAM_INSTANCE_TLS_INSECURE=true to skip
// verification entirely — development only; a startup warning is logged when it
// is in effect.
func InstanceTLSConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if InstanceTLSInsecure() {
		cfg.InsecureSkipVerify = true
		return cfg
	}

	if path := os.Getenv(instanceCABundleEnv); path != "" {
		if pem, err := os.ReadFile(path); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				cfg.RootCAs = pool
			}
		}
	}
	return cfg
}

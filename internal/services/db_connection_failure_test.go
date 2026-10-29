package services_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/stretchr/testify/require"
)

func connectionTestPKI(t *testing.T) (string, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &k.PublicKey, k)
	require.NoError(t, err)
	key, err := x509.MarshalECPrivateKey(k)
	require.NoError(t, err)
	certPath, keyPath := filepath.Join(t.TempDir(), "cert.pem"), filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key}), 0600))
	return certPath, keyPath
}
func TestDatabaseConnectionFailureRetainsPingCause(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	db, err := services.ConnectWithRetry("postgres://fixture:private-secret@"+address+"/fixture?sslmode=disable", 2, 0)
	require.Nil(t, db)
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection refused")
	require.NotContains(t, err.Error(), "private-secret")
	require.NotContains(t, err.Error(), "%!w")
	_, err = services.ConnectWithRetry("postgres://localhost/fixture", 0, 0)
	require.ErrorContains(t, err, "retry policy")
}
func TestDatabaseTLSInputsReturnErrors(t *testing.T) {
	db, err := services.ConnectWithSecureTLS("postgres://localhost/fixture", 1, 0, "missing-ca", "missing-cert", "missing-key")
	require.Nil(t, db)
	require.ErrorContains(t, err, "CA certificate")
	cert, key := connectionTestPKI(t)
	bad := filepath.Join(t.TempDir(), "bad.pem")
	require.NoError(t, os.WriteFile(bad, []byte("invalid certificate"), 0600))
	_, err = services.ConnectWithSecureTLS("postgres://localhost/fixture", 1, 0, bad, cert, key)
	require.ErrorContains(t, err, "no certificates")
	_, err = services.ConnectWithSecureTLS("postgres://localhost/fixture", 1, 0, cert, cert, "missing-key")
	require.ErrorContains(t, err, "client certificate")
}
func TestDatabaseTLSRefusalCannotFallBackToPlaintext(t *testing.T) {
	cert, key := connectionTestPKI(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		probe := make([]byte, 8)
		if _, err = io.ReadFull(c, probe); err != nil {
			done <- err
			return
		}
		if string(probe) != string([]byte{0, 0, 0, 8, 4, 210, 22, 47}) {
			done <- fmt.Errorf("unexpected PostgreSQL SSL probe")
			return
		}
		_, err = c.Write([]byte("N"))
		done <- err
	}()
	db, err := services.ConnectWithSecureTLS("postgres://fixture:private-secret@"+listener.Addr().String()+"/fixture?sslmode=prefer", 1, 0, cert, cert, key)
	require.Nil(t, db)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "TLS") || strings.Contains(err.Error(), "SSL"), err.Error())
	require.NotContains(t, err.Error(), "private-secret")
	require.NoError(t, <-done)
}

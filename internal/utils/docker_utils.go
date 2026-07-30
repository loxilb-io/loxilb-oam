package utils

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// dockerHTTPClient returns an HTTP client for the Docker Engine API. When
// OAM_DOCKER_TLS=true it uses TLS with the CA and client certificate found in
// OAM_DOCKER_CERT_PATH (ca.pem, cert.pem, key.pem); otherwise a plain client.
func dockerHTTPClient() *http.Client {
	if os.Getenv("OAM_DOCKER_TLS") != "true" {
		return &http.Client{}
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certPath := os.Getenv("OAM_DOCKER_CERT_PATH"); certPath != "" {
		if ca, err := os.ReadFile(filepath.Join(certPath, "ca.pem")); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(ca) {
				tlsCfg.RootCAs = pool
			}
		}
		if cert, err := tls.LoadX509KeyPair(filepath.Join(certPath, "cert.pem"), filepath.Join(certPath, "key.pem")); err == nil {
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}
}

// dockerPost issues a POST to the Docker API and returns the response body.
func dockerPost(reqURL string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := dockerHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// StopContainer stops a running Docker container by name on the given host.
func StopContainer(dockerHost, containerName string) error {
	reqURL := dockerHost + "/containers/" + url.PathEscape(containerName) + "/stop"
	if _, err := dockerPost(reqURL, nil); err != nil {
		return fmt.Errorf("error stopping container: %w", err)
	}
	return nil
}

// RemoveContainer removes a Docker container by name on the given host.
func RemoveContainer(dockerHost, containerName string) error {
	reqURL := dockerHost + "/containers/" + url.PathEscape(containerName)
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("error creating request to remove container: %w", err)
	}
	resp, err := dockerHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("error removing container: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// PullImage pulls the given Docker image and tag onto the host.
func PullImage(dockerHost, imgName, imgTag string) error {
	reqURL := fmt.Sprintf("%s/images/create?fromImage=%s&tag=%s",
		dockerHost, url.QueryEscape(imgName), url.QueryEscape(imgTag))
	if _, err := dockerPost(reqURL, nil); err != nil {
		return fmt.Errorf("error pulling image: %w", err)
	}
	return nil
}

// dockerCreateRequest models the subset of the container-create body we send.
// Building it as a struct (rather than string formatting) prevents any value
// from breaking out of the JSON and injecting arbitrary Docker API fields.
type dockerCreateRequest struct {
	Image      string           `json:"Image"`
	User       string           `json:"User"`
	HostConfig dockerHostConfig `json:"HostConfig"`
}

type dockerHostConfig struct {
	CapAdd        []string            `json:"CapAdd"`
	RestartPolicy dockerRestartPolicy `json:"RestartPolicy"`
	Privileged    bool                `json:"Privileged"`
	NetworkMode   string              `json:"NetworkMode"`
	Binds         []string            `json:"Binds"`
}

type dockerRestartPolicy struct {
	Name string `json:"Name"`
}

// RunContainer creates, renames, and starts a Docker container from the given
// image and tag. Privileged/SYS_ADMIN/host networking are required by the
// LoxiLB dataplane and are intentionally set.
func RunContainer(dockerHost, containerName, imageName, imageTag string) error {
	reqBody := dockerCreateRequest{
		Image: fmt.Sprintf("%s:%s", imageName, imageTag),
		User:  "root",
		HostConfig: dockerHostConfig{
			CapAdd:        []string{"SYS_ADMIN"},
			RestartPolicy: dockerRestartPolicy{Name: "unless-stopped"},
			Privileged:    true,
			NetworkMode:   "host",
			Binds:         []string{"/var/log:/var/log"},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("error encoding container-create request: %w", err)
	}

	body, err := dockerPost(dockerHost+"/containers/create", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("error running container: %w", err)
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("error parsing container-create response: %w", err)
	}
	if result.ID == "" {
		return fmt.Errorf("unexpected Docker response: %s", string(body))
	}

	if err := renameContainer(dockerHost, result.ID, containerName); err != nil {
		return fmt.Errorf("error renaming container: %w", err)
	}
	return StartContainer(dockerHost, result.ID)
}

// StartContainer starts a Docker container by name (or ID) on the given host.
func StartContainer(dockerHost, containerName string) error {
	reqURL := dockerHost + "/containers/" + url.PathEscape(containerName) + "/start"
	if _, err := dockerPost(reqURL, nil); err != nil {
		return fmt.Errorf("error starting container: %w", err)
	}
	return nil
}

// renameContainer renames the container with the given ID to newName.
func renameContainer(dockerHost, containerID, newName string) error {
	reqURL := fmt.Sprintf("%s/containers/%s/rename?name=%s",
		dockerHost, url.PathEscape(containerID), url.QueryEscape(newName))
	if _, err := dockerPost(reqURL, nil); err != nil {
		log.Printf("Error renaming container: %v", err)
		return err
	}
	return nil
}

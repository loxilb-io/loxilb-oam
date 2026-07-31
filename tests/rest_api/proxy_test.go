package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test the proxy functionality
func TestProxyToLoxiLB(t *testing.T) {
	// First, we need to create a user and get a token. Instance creation below
	// needs the instance-write capability, so the user is created as an admin.
	userID, err := createUser("proxyuser", "proxyuser@example.com", testPassword(), "admin")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	defer deleteUser(userID)

	// Login to get token
	loginData := map[string]string{
		"username": "proxyuser",
		"password": testPassword(),
	}
	jsonValue, _ := json.Marshal(loginData)
	req, _ := http.NewRequest("POST", baseURL+"/login", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Login failed with status: %d", resp.StatusCode)
	}

	var loginResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&loginResp)
	token = loginResp["token"].(string)

	// Create a LoxiLB instance for testing
	instanceID, err := createLoxiLBInstance("test-proxy-instance", "127.0.0.1", "11111", "Test proxy instance", "v1", "loxilb", "latest")
	if err != nil {
		t.Fatalf("Failed to create LoxiLB instance: %v", err)
	}
	defer deleteLoxiLBInstance(instanceID)

	// Test 1: Proxy request to get meta information (this should work even if LoxiLB is not running)
	// We expect this to fail with connection error since we don't have a real LoxiLB running
	proxyURL := fmt.Sprintf("%s/loxilbs/%d/netlox/v1/meta", baseURL, instanceID)
	req, _ = http.NewRequest("GET", proxyURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make proxy request: %v", err)
	}
	defer resp.Body.Close()

	// We expect either:
	// - 502 Bad Gateway (if LoxiLB instance is not reachable) - which is correct behavior
	// - 200 OK (if somehow LoxiLB is running on 127.0.0.1:11111)
	assert.True(t, resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusOK,
		"Expected 502 (Bad Gateway) or 200 (OK), got %d", resp.StatusCode)

	// Test 2: Test with invalid instance ID
	invalidProxyURL := fmt.Sprintf("%s/loxilbs/99999/netlox/v1/meta", baseURL)
	req, _ = http.NewRequest("GET", invalidProxyURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make proxy request: %v", err)
	}
	defer resp.Body.Close()

	// Should return 404 Not Found for invalid instance ID
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"Expected 404 for invalid instance ID, got %d", resp.StatusCode)

	// Test 3: Test without authentication
	req, _ = http.NewRequest("GET", proxyURL, nil)
	// No Authorization header

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make proxy request: %v", err)
	}
	defer resp.Body.Close()

	// Should return 401 Unauthorized
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"Expected 401 for unauthenticated request, got %d", resp.StatusCode)
}

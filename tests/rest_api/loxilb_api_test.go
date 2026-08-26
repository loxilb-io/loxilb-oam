package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/models"

	"github.com/stretchr/testify/assert"
)

const loxiLBBaseURL = "http://127.0.0.1:8080/oam/loxilbs"
const baseURL = "http://127.0.0.1:8080/oam"

var token string

// adminToken is the bootstrap-admin JWT used for privileged setup (creating and
// deleting users, which are admin-only). TestMain populates it by logging in as
// the admin account.
var adminToken string

// testPassword is the fixture-account password for this live-server suite.
// Override with OAM_TEST_PASSWORD; the default is an obvious placeholder that
// still satisfies the password policy.
func testPassword() string {
	if p := os.Getenv("OAM_TEST_PASSWORD"); p != "" {
		return p
	}
	return "ChangeMe-Test1!"
}

// testPasswordUpdated is the new password used by the update-password test.
// Override with OAM_TEST_PASSWORD_NEW.
func testPasswordUpdated() string {
	if p := os.Getenv("OAM_TEST_PASSWORD_NEW"); p != "" {
		return p
	}
	return "ChangeMe-Test2!"
}

// testAdminUser / testAdminPassword identify the bootstrap admin account the
// server creates on a fresh database. In CI, OAM_TEST_ADMIN_PASSWORD must match
// the OAM_DEFAULT_ADMIN_PASSWORD the server was booted with.
func testAdminUser() string {
	if u := os.Getenv("OAM_TEST_ADMIN_USER"); u != "" {
		return u
	}
	return "admin"
}

func testAdminPassword() string {
	if p := os.Getenv("OAM_TEST_ADMIN_PASSWORD"); p != "" {
		return p
	}
	return "ChangeMe-Admin1!"
}

// mustLogin authenticates against POST /oam/login and returns the JWT, aborting
// the whole suite on failure (used only for fixture setup in TestMain).
func mustLogin(username, password string) string {
	loginData := map[string]string{"username": username, "password": password}
	jsonValue, _ := json.Marshal(loginData)
	req, _ := http.NewRequest("POST", baseURL+"/login", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Failed to login as %s: %s", username, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Login as %s failed: status %d", username, resp.StatusCode)
	}
	var response map[string]string
	json.NewDecoder(resp.Body).Decode(&response)
	if response["token"] == "" {
		log.Fatalf("Login as %s returned an empty token", username)
	}
	return response["token"]
}

// requireLiveInstance skips tests that drive firmware operations against a real
// LoxiLB instance. The PostgreSQL-backed integration job has no reachable instance
// (the proxy call would just time out), so these run only when
// OAM_TEST_LIVE_INSTANCE is set — e.g. locally or in the e2e context.
func requireLiveInstance(t *testing.T) {
	if os.Getenv("OAM_TEST_LIVE_INSTANCE") == "" {
		t.Skip("firmware ops need a reachable LoxiLB instance; set OAM_TEST_LIVE_INSTANCE=1 to run")
	}
}

// createUser creates a user via the admin-only POST /oam/users endpoint. The
// current API requires an email and admin authentication; role is optional
// (defaults to "user" server-side). Returns the new user's ID.
func createUser(username, email, password, role string) (int, error) {
	userData := map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}
	if role != "" {
		userData["role"] = role
	}
	jsonValue, _ := json.Marshal(userData)
	req, _ := http.NewRequest("POST", baseURL+"/users", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("failed to create user %s: status %d", username, resp.StatusCode)
	}

	// POST /oam/users responds with {"id": <n>, "message": ...}; models.User
	// decodes the id field.
	var createdUser models.User
	if err := json.NewDecoder(resp.Body).Decode(&createdUser); err != nil {
		return 0, err
	}

	return createdUser.ID, nil
}

func deleteUser(userID int) error {
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/users/%d", baseURL, userID), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete user: %d", userID)
	}

	return nil
}

func createLoxiLBInstance(name, host, port, description, version, cimage, ctag string) (int, error) {
	instanceData := map[string]string{
		"name":        name,
		"host":        host,
		"port":        port,
		"protocol":    "http",
		"description": description,
		"version":     version,
		"cimage":      cimage,
		"ctag":        ctag,
	}
	jsonValue, _ := json.Marshal(instanceData)
	req, _ := http.NewRequest("POST", loxiLBBaseURL, bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("failed to create LoxiLB instance: %s", name)
	}

	var createdInstance models.LoxiLBInstance
	if err := json.NewDecoder(resp.Body).Decode(&createdInstance); err != nil {
		return 0, err
	}

	return createdInstance.ID, nil
}

func deleteLoxiLBInstance(instanceID int) error {
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/%d", loxiLBBaseURL, instanceID), nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete LoxiLB instance: %d", instanceID)
	}

	return nil
}

func TestApiCreateLoxiLBInstance(t *testing.T) {
	newInstance := map[string]string{
		"name":        "test-instance",
		"host":        "localhost",
		"port":        "8091",
		"protocol":    "http",
		"description": "Test instance",
		"version":     "v1",
		"cimage":      "ghcr.io/loxilab/loxilb",
		"ctag":        "v0.9.7",
	}
	jsonValue, _ := json.Marshal(newInstance)

	req, _ := http.NewRequest("POST", loxiLBBaseURL, bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var responseInstance models.LoxiLBInstance
	json.NewDecoder(resp.Body).Decode(&responseInstance)
	assert.Equal(t, "test-instance", responseInstance.Name)
}

func TestApiFetchLoxiLBInstanceByID(t *testing.T) {
	instanceID, err := createLoxiLBInstance("test-instance-fetch", "localhost", "8097",
		"Test instance fetch", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/%d", loxiLBBaseURL, instanceID), nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseInstance models.LoxiLBInstance
	json.NewDecoder(resp.Body).Decode(&responseInstance)
	assert.Equal(t, instanceID, responseInstance.ID)
	assert.Equal(t, "test-instance-fetch", responseInstance.Name)

	err = deleteLoxiLBInstance(instanceID)
	if err != nil {
		t.Fatalf("Failed to delete test LoxiLB instance: %s", err)
	}
}

func TestApiUpdateLoxiLBInstance(t *testing.T) {
	instanceID, err := createLoxiLBInstance("test-instance-update", "localhost",
		"8082", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "updated-instance",
		"host":        "localhost",
		"port":        "8092",
		"protocol":    "http",
		"description": "Updated instance",
		"version":     "v1",
		"cimage":      "ghcr.io/loxilb-io/loxilb",
		"ctag":        "v0.9.7",
	}
	jsonValue, _ := json.Marshal(updateInstance)

	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/%d", loxiLBBaseURL, instanceID), bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseInstance models.LoxiLBInstance
	json.NewDecoder(resp.Body).Decode(&responseInstance)
	assert.Equal(t, "updated-instance", responseInstance.Name)

	err = deleteLoxiLBInstance(instanceID)
	if err != nil {
		t.Fatalf("Failed to delete test LoxiLB instance: %s", err)
	}
}

func TestApiDeleteLoxiLBInstance(t *testing.T) {
	instanceID, err := createLoxiLBInstance("test-instance-delete", "localhost",
		"8093", "Test instance delete", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/%d", loxiLBBaseURL, instanceID), nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApiFetchLoxiLBInstances(t *testing.T) {
	req, _ := http.NewRequest("GET", loxiLBBaseURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseInstances []models.LoxiLBInstance
	json.NewDecoder(resp.Body).Decode(&responseInstances)
	assert.NotEmpty(t, responseInstances)
}

func TestApiUpdateLoxiLBFirmware(t *testing.T) {
	requireLiveInstance(t)
	instanceID, err := createLoxiLBInstance("test-instance-update", "192.0.2.10",
		"8080", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "my-loxilb",
		"host":        "192.0.2.10",
		"port":        "8080",
		"protocol":    "http",
		"description": "Test instance update",
		"version":     "v1",
		"cimage":      "ghcr.io/loxilb-io/loxilb",
		"ctag":        "latest",
	}
	jsonValue, _ := json.Marshal(updateInstance)
	apiURL := loxiLBBaseURL + "/" + fmt.Sprintf("%d", instanceID) + "/firmware"

	req, _ := http.NewRequest("PUT", apiURL, bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	err = deleteLoxiLBInstance(instanceID)
	if err != nil {
		t.Fatalf("Failed to delete test LoxiLB instance: %s", err)
	}
}

func TestApiStartLoxiLBFirmware(t *testing.T) {
	requireLiveInstance(t)
	instanceID, err := createLoxiLBInstance("test-instance-update", "192.0.2.10",
		"8080", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "my-loxilb",
		"host":        "192.0.2.10",
		"port":        "8080",
		"protocol":    "http",
		"description": "Test instance update",
		"version":     "v1",
		"cimage":      "ghcr.io/loxilb-io/loxilb",
		"ctag":        "V0.9.7",
	}
	jsonValue, _ := json.Marshal(updateInstance)
	apiURL := loxiLBBaseURL + "/" + fmt.Sprintf("%d", instanceID) + "/firmware/start"

	req, _ := http.NewRequest("PUT", apiURL, bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	err = deleteLoxiLBInstance(instanceID)
	if err != nil {
		t.Fatalf("Failed to delete test LoxiLB instance: %s", err)
	}
}

func TestApiStopLoxiLBFirmware(t *testing.T) {
	requireLiveInstance(t)
	instanceID, err := createLoxiLBInstance("test-instance-update", "192.0.2.10",
		"8080", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "my-loxilb",
		"host":        "192.0.2.10",
		"port":        "8080",
		"protocol":    "http",
		"description": "Test instance update",
		"version":     "v1",
		"cimage":      "ghcr.io/loxilb-io/loxilb",
		"ctag":        "V0.9.7",
	}
	jsonValue, _ := json.Marshal(updateInstance)
	apiURL := loxiLBBaseURL + "/" + fmt.Sprintf("%d", instanceID) + "/firmware/stop"

	req, _ := http.NewRequest("PUT", apiURL, bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	err = deleteLoxiLBInstance(instanceID)
	if err != nil {
		t.Fatalf("Failed to delete test LoxiLB instance: %s", err)
	}
}

func TestApiLogin(t *testing.T) {
	// Use a dedicated account rather than re-logging-in testllb: two logins of
	// the same user within one second produce an identical JWT (same subject +
	// second-granularity expiry) and collide on the api_tokens primary key. This
	// exercises the login contract without disturbing the package-level token.
	id, err := createUser("testlogin", "testlogin@example.com", testPassword(), "")
	if err != nil {
		t.Fatalf("Failed to create user: %s", err)
	}
	defer deleteUser(id)

	loginData := map[string]string{
		"username": "testlogin",
		"password": testPassword(),
	}
	jsonValue, _ := json.Marshal(loginData)

	req, _ := http.NewRequest("POST", baseURL+"/login", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]string
	json.NewDecoder(resp.Body).Decode(&response)
	assert.NotEmpty(t, response["token"])
}

func TestApiCreateUser(t *testing.T) {
	// User creation is admin-only and requires an email; createUser sends both.
	id, err := createUser("testusercreate", "testusercreate@example.com", testPassword(), "")
	if err != nil {
		t.Fatalf("Failed to create user: %s", err)
	}
	assert.Greater(t, id, 0, "expected a positive new user ID")

	// Keep the fixture DB clean for later tests.
	if err := deleteUser(id); err != nil {
		t.Fatalf("Failed to delete created user: %s", err)
	}
}

func TestApiUpdateUser(t *testing.T) {
	// Operate on a dedicated user rather than a hardcoded ID so the test never
	// clobbers the bootstrap admin.
	id, err := createUser("testuserupdate", "testuserupdate@example.com", testPassword(), "")
	if err != nil {
		t.Fatalf("Failed to create user: %s", err)
	}
	defer deleteUser(id)

	updateUser := map[string]string{
		"password": testPasswordUpdated(),
	}
	jsonValue, _ := json.Marshal(updateUser)

	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/users/%d", baseURL, id), bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApiDeleteUser(t *testing.T) {
	// Create a throwaway user to delete, rather than assuming a hardcoded ID.
	id, err := createUser("testuserdelete", "testuserdelete@example.com", testPassword(), "")
	if err != nil {
		t.Fatalf("Failed to create user: %s", err)
	}

	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/users/%d", baseURL, id), nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApiLogout(t *testing.T) {
	// Create a logout request
	req, _ := http.NewRequest("POST", baseURL+"/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	// Create a response recorder to record the response
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	// Check the response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]string
	json.NewDecoder(resp.Body).Decode(&response)
	assert.Equal(t, "Logged out successfully", response["message"])
}

func TestMain(m *testing.M) {
	// Authenticate as the bootstrap admin so we can create the test principal
	// (user creation is admin-only).
	adminToken = mustLogin(testAdminUser(), testAdminPassword())

	// The test principal is created as an admin so it holds every capability the
	// suite exercises (instance write + user administration), then we log in as
	// it for the bulk of the tests.
	userID, err := createUser("testllb", "testllb@example.com", testPassword(), "admin")
	if err != nil {
		log.Fatalf("Failed to create test user: %s", err)
	}
	token = mustLogin("testllb", testPassword())

	code := m.Run()

	// Clean up with the admin token (the principal may have logged itself out).
	if err := deleteUser(userID); err != nil {
		log.Fatalf("Failed to delete test user: %s", err)
	}

	os.Exit(code)
}

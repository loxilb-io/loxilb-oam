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

func createUser(username, password string) (int, error) {
	userData := map[string]string{
		"username": username,
		"password": password,
	}
	jsonValue, _ := json.Marshal(userData)
	req, _ := http.NewRequest("POST", baseURL+"/users", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("failed to create user: %s", username)
	}

	var createdUser models.User
	if err := json.NewDecoder(resp.Body).Decode(&createdUser); err != nil {
		return 0, err
	}

	return createdUser.ID, nil
}

func deleteUser(userID int) error {
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/users/%d", baseURL, userID), nil)
	req.Header.Set("Authorization", "Bearer "+token)

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
	instanceID, err := createLoxiLBInstance("test-instance-update", "192.0.2.10",
		"8080", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "my-loxilb",
		"host":        "192.0.2.10",
		"port":        "8080",
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
	instanceID, err := createLoxiLBInstance("test-instance-update", "192.0.2.10",
		"8080", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "my-loxilb",
		"host":        "192.0.2.10",
		"port":        "8080",
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
	instanceID, err := createLoxiLBInstance("test-instance-update", "192.0.2.10",
		"8080", "Test instance update", "v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7")
	if err != nil {
		t.Fatalf("Failed to create test LoxiLB instance: %s", err)
	}

	updateInstance := map[string]string{
		"name":        "my-loxilb",
		"host":        "192.0.2.10",
		"port":        "8080",
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
	// Create login data
	loginData := map[string]string{
		"username": "testllb",
		"password": testPassword(),
	}
	jsonValue, _ := json.Marshal(loginData)

	// Create a request to send to the endpoint
	req, _ := http.NewRequest("POST", baseURL+"/login", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

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
	assert.NotEmpty(t, response["token"])

	// Store the token for use in other tests
	token = response["token"]
}

func TestApiCreateUser(t *testing.T) {
	// Create a new user
	newUser := map[string]string{
		"username": "testusercreate",
		"password": testPassword(),
	}
	jsonValue, _ := json.Marshal(newUser)

	// Create a request to send to the endpoint
	req, _ := http.NewRequest("POST", baseURL+"/users", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	// Create a response recorder to record the response
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %s", err)
	}
	defer resp.Body.Close()

	// Check the response
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var responseUser map[string]string
	json.NewDecoder(resp.Body).Decode(&responseUser)
	assert.Equal(t, "testusercreate", responseUser["username"])
}

func TestApiUpdateUser(t *testing.T) {
	// Update user password
	updateUser := map[string]string{
		"password": testPasswordUpdated(),
	}
	jsonValue, _ := json.Marshal(updateUser)

	// Create a request to send to the endpoint
	req, _ := http.NewRequest("PUT", baseURL+"/users/1", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
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
}

func TestApiDeleteUser(t *testing.T) {
	// Create a DELETE request
	req, _ := http.NewRequest("DELETE", baseURL+"/users/1", nil)
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
	userID, err := createUser("testllb", testPassword())
	if err != nil {
		log.Fatalf("Failed to create test user: %s", err)
	}

	loginData := map[string]string{
		"username": "testllb",
		"password": testPassword(),
	}
	jsonValue, _ := json.Marshal(loginData)
	req, _ := http.NewRequest("POST", baseURL+"/login", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Failed to login: %s", err)
	}
	defer resp.Body.Close()

	var loginResponse map[string]string
	json.NewDecoder(resp.Body).Decode(&loginResponse)
	token = loginResponse["token"]

	code := m.Run()

	err = deleteUser(userID)
	if err != nil {
		log.Fatalf("Failed to delete test user: %s", err)
	}

	os.Exit(code)
}

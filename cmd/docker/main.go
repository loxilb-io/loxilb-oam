package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

// dockerHost is the Docker Engine API endpoint. Override with DOCKER_HOST_URL;
// defaults to the local Docker daemon over TCP.
var dockerHost = getenvDefault("DOCKER_HOST_URL", "http://127.0.0.1:2375")

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

const containerName = "my-loxilb"
const imageName = "ghcr.io/loxilb-io/loxilb"

const imageTag = "v0.9.7"

// const imageTag = "latest"

func main() {
	// Step 1: List all images
	fmt.Println("Listing all images...")
	listImages(dockerHost)

	// Step 2: Pull the latest image
	fmt.Printf("Pulling the image %s:%s...\n", imageName, imageTag)
	pullImage(dockerHost, imageName, imageTag)

	// Step 3: Stop the running container
	fmt.Println("Stopping the existing container...")
	stopContainer(dockerHost, containerName)

	// Step 4: Remove the old container
	fmt.Println("Removing the old container...")
	removeContainer(dockerHost, containerName)

	// Step 5: Run the new container
	fmt.Println("Running the new container...")
	runContainer(dockerHost, containerName, imageName, imageTag)
}

func listImages(dockerHost string) {
	url := dockerHost + "/images/json"
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Error listing images: %v", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
	}
	fmt.Println(string(body))
}

func pullImage(dockerHost, imgName string, imgTag string) {
	url := fmt.Sprintf("%s/images/create?fromImage=%s&tag=%s",
		dockerHost, imgName, imgTag)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		log.Fatalf("Error pulling image: %v", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}

func stopContainer(dockerHost, containerName string) {
	url := dockerHost + "/containers/" + containerName + "/stop"
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		log.Fatalf("Error stopping container: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}

func removeContainer(dockerHost, containerName string) {
	url := dockerHost + "/containers/" + containerName
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		log.Fatalf("Error creating request to remove container: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Error removing container: %v", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}

func runContainer(dockerHost, containerName, imageName, imageTag string) {
	url := dockerHost + "/containers/create"
	payload := fmt.Sprintf(`{
		"Image": "%s:%s",
        "User": "root",
        "HostConfig": {
            "CapAdd": ["SYS_ADMIN"],
            "RestartPolicy": {
                "Name": "unless-stopped"
            },
            "Privileged": true,
            "NetworkMode": "host",
            "Binds": [
                "/var/log:/var/log"
            ]
        }
    }`, imageName, imageTag)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte(payload)))
	if err != nil {
		log.Fatalf("Error creating request to run container: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error running container: %v", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))

	// Extract container ID from response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Fatalf("Error parsing response: %v", err)
	}
	containerID := result["Id"].(string)

	// Rename the container to the desired name
	renameContainer(dockerHost, containerID, containerName)

	// Start the container after creation
	startContainer(dockerHost, containerID)
}

func renameContainer(dockerHost, containerID, newName string) {
	url := fmt.Sprintf("%s/containers/%s/rename?name=%s", dockerHost, containerID, newName)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		log.Fatalf("Error creating request to rename container: %v", err)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error renaming container: %v", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}

func startContainer(dockerHost, containerID string) {
	url := dockerHost + "/containers/" + containerID + "/start"
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		log.Fatalf("Error starting container: %v", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
}

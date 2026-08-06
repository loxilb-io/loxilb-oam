package services

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

type LoxiLBService struct {
	DB *sql.DB
}

func NewLoxiLBService(db *sql.DB) *LoxiLBService {
	return &LoxiLBService{DB: db}
}

// FetchLoxiLBInstances returns all LoxiLB instances from the database.
func (s *LoxiLBService) FetchLoxiLBInstances() ([]models.LoxiLBInstance, error) {
	var instances []models.LoxiLBInstance
	err := utils.RetryOperation(func() error {
		query := config.SelectLoxiLBInstancesQuery
		rows, err := s.DB.Query(query)
		if err != nil {
			utils.LogError("Failed to fetch LoxiLB instances: " + err.Error())
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var instance models.LoxiLBInstance

			err := rows.Scan(&instance.ID, &instance.Name, &instance.Host, &instance.Port, &instance.Protocol,
				&instance.Description, &instance.Version, &instance.ApiEndpoint, &instance.Cimage, &instance.Ctag, &instance.IsActive, &instance.CreatedAt)

			if err != nil {
				utils.LogError("Failed to scan LoxiLB instance: " + err.Error())
				return err
			}

			instances = append(instances, instance)
		}

		if err = rows.Err(); err != nil {
			utils.LogError("Rows error: " + err.Error())
			return err
		}

		return nil
	}, config.MaxRetries, config.RetryDelay)
	return instances, err
}

// FetchLoxiLBInstanceByID returns the LoxiLB instance with the given ID.
func (s *LoxiLBService) FetchLoxiLBInstanceByID(id int) (*models.LoxiLBInstance, error) {
	var instance models.LoxiLBInstance

	err := utils.RetryOperation(func() error {
		query := config.SelectLoxiLBInstanceByIDQuery
		row := s.DB.QueryRow(query, id)
		err := row.Scan(&instance.ID, &instance.Name, &instance.Host, &instance.Port, &instance.Protocol, &instance.Description,
			&instance.Version, &instance.ApiEndpoint, &instance.Cimage, &instance.Ctag, &instance.IsActive, &instance.CreatedAt)
		if err != nil {
			if err == sql.ErrNoRows {
				utils.LogWarning(fmt.Sprintf("LoxiLB instance with ID %d not found", id))
				return err
			}
			utils.LogError("Failed to fetch LoxiLB instance by ID: " + err.Error())
			return err
		}

		return nil
	}, config.MaxRetries, config.RetryDelay)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// ErrInstanceNameTaken / ErrInstanceEndpointTaken are conflicts, not
// failures: the caller maps them to 409 with a message naming what clashed.
var (
	ErrInstanceNameTaken     = errors.New("an instance with this name already exists")
	ErrInstanceEndpointTaken = errors.New("an instance is already registered at this endpoint")
)

// InstanceNameTaken reports whether another row (excluding excludeID, which
// is 0 on create) carries this name. Case-insensitive: the UI resolves
// ?name=… by exact match against the list, so 'GW1' and 'gw1' would make one
// of the two instances unreachable.
func (s *LoxiLBService) InstanceNameTaken(name string, excludeID int) (bool, error) {
	return s.countInstances(config.CountLoxiLBInstanceByNameQuery, name, excludeID)
}

// InstanceEndpointTaken mirrors the UNIQUE(api_endpoint) constraint so the
// conflict can be reported before the insert fails.
func (s *LoxiLBService) InstanceEndpointTaken(endpoint string, excludeID int) (bool, error) {
	return s.countInstances(config.CountLoxiLBInstanceByEndpointQuery, endpoint, excludeID)
}

func (s *LoxiLBService) countInstances(query, value string, excludeID int) (bool, error) {
	var count int
	err := utils.RetryOperation(func() error {
		return s.DB.QueryRow(query, value, excludeID).Scan(&count)
	}, config.MaxRetries, config.RetryDelay)
	if err != nil {
		utils.LogError("Failed to check LoxiLB instance uniqueness: " + err.Error())
		return false, err
	}
	return count > 0, nil
}

// IsDuplicateKeyError recognizes the driver-level unique-violation. It is the
// backstop for the race between the pre-check above and the write: two
// concurrent creates both pass the check, and the loser must still get a 409
// rather than a 500 carrying raw SQL.
func IsDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}

// AddLoxiLBInstance inserts a new LoxiLB instance and returns its new ID.
func (s *LoxiLBService) AddLoxiLBInstance(instance models.LoxiLBInstance) (int, error) {
	// Generate the ApiEndpoint using the Protocol field
	instance.ApiEndpoint = fmt.Sprintf("%s://%s:%s/netlox/%s", instance.Protocol, instance.Host, instance.Port, instance.Version)

	var instanceID int
	err := utils.RetryOperation(func() error {
		query := config.InsertLoxiLBInstanceQuery
		result, err := s.DB.Exec(query, instance.Name, instance.Host, instance.Port, instance.Protocol,
			instance.Description, instance.Version, instance.ApiEndpoint, instance.Cimage, instance.Ctag, instance.IsActive)
		if err != nil {
			utils.LogError("Failed to add LoxiLB instance: " + err.Error())
			return err
		}

		lastInsertID, err := result.LastInsertId()
		if err != nil {
			utils.LogError("Failed to get last insert ID: " + err.Error())
			return err
		}
		instanceID = int(lastInsertID)

		utils.LogInfo("LoxiLB instance added: " + instance.Name)
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return instanceID, err
}

// AddLoxiLBInstanceWithArgs inserts a new LoxiLB instance from individual
// fields and returns its new ID and generated API endpoint.
func (s *LoxiLBService) AddLoxiLBInstanceWithArgs(name, host, port, protocol, description, version, cimage, ctag string, isActive bool) (int, string, error) {
	// Generate the ApiEndpoint with the specified protocol
	ApiEndpoint := fmt.Sprintf("%s://%s:%s/netlox/%s", protocol, host, port, version)

	var instanceID int
	var conflict error
	err := utils.RetryOperation(func() error {
		query := config.InsertLoxiLBInstanceQuery
		result, err := s.DB.Exec(query, name, host, port, protocol,
			description, version, ApiEndpoint, cimage, ctag, isActive)
		if err != nil {
			utils.LogError("Failed to add LoxiLB instance: " + err.Error())
			// A unique violation is a verdict, not a transient fault —
			// retrying it just burns MaxRetries × RetryDelay before failing
			// with the same error.
			if IsDuplicateKeyError(err) {
				conflict = ErrInstanceEndpointTaken
				return nil
			}
			return err
		}

		lastInsertID, err := result.LastInsertId()
		if err != nil {
			utils.LogError("Failed to get last insert ID: " + err.Error())
			return err
		}
		instanceID = int(lastInsertID)

		utils.LogInfo("LoxiLB instance added: " + name)
		return nil
	}, config.MaxRetries, config.RetryDelay)

	if conflict != nil {
		return 0, ApiEndpoint, conflict
	}
	return instanceID, ApiEndpoint, err
}

// UpdateLoxiLBInstance updates an existing LoxiLB instance in the database.
// Returns ErrInstanceNotFound (404) or ErrInstanceEndpointTaken (409) so the
// handler can answer precisely instead of collapsing everything into a 500.
func (s *LoxiLBService) UpdateLoxiLBInstance(instance models.LoxiLBInstance) error {
	var conflict error
	err := utils.RetryOperation(func() error {
		// Check if the instance exists
		var existingInstance models.LoxiLBInstance

		query := "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances WHERE id = ?"
		err := s.DB.QueryRow(query, instance.ID).Scan(&existingInstance.ID, &existingInstance.Name,
			&existingInstance.Host, &existingInstance.Port, &existingInstance.Protocol, &existingInstance.Description,
			&existingInstance.Version, &existingInstance.ApiEndpoint, &existingInstance.Cimage, &existingInstance.Ctag, &existingInstance.IsActive, &existingInstance.CreatedAt)

		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				utils.LogWarning(fmt.Sprintf("LoxiLB instance with ID %d not found", instance.ID))
				conflict = ErrInstanceNotFound
				return nil // a missing row will not appear on a retry
			}
			utils.LogError("Failed to query LoxiLB instance: " + err.Error())
			return err
		}

		// Update the instance information
		updateQuery := config.UpdateLoxiLBInstanceQuery

		// Generate the ApiEndpoint using the Protocol field
		instance.ApiEndpoint = fmt.Sprintf("%s://%s:%s/netlox/%s", instance.Protocol, instance.Host, instance.Port, instance.Version)
		_, err = s.DB.Exec(updateQuery, instance.Name, instance.Host, instance.Port, instance.Protocol, instance.Description,
			instance.Version, instance.ApiEndpoint, instance.Cimage, instance.Ctag, instance.IsActive, instance.ID)
		if err != nil {
			utils.LogError("Failed to update LoxiLB instance: " + err.Error())
			if IsDuplicateKeyError(err) {
				conflict = ErrInstanceEndpointTaken
				return nil
			}
			return err
		}

		utils.LogInfo("LoxiLB instance updated: " + instance.Name)
		return nil
	}, config.MaxRetries, config.RetryDelay)

	if conflict != nil {
		return conflict
	}
	return err
}

// DeleteLoxiLBInstance deletes the LoxiLB instance with the given ID.
func (s *LoxiLBService) DeleteLoxiLBInstance(id int) error {
	return utils.RetryOperation(func() error {
		query := config.DeleteLoxiLBInstanceQuery
		_, err := s.DB.Exec(query, id)
		if err != nil {
			utils.LogError("Failed to delete LoxiLB instance: " + err.Error())
			return err
		}
		utils.LogInfo(fmt.Sprintf("LoxiLB instance with ID %d deleted", id))
		return nil
	}, config.MaxRetries, config.RetryDelay)
}

// StartFirmware pulls the instance image and starts its container.
func (s *LoxiLBService) StartFirmware(instance models.LoxiLBInstance) error {
	dockerHost := config.DockerBaseURL(instance.Host)
	containerName := instance.Name
	imageName := instance.Cimage
	imageTag := instance.Ctag

	// Step 1: Pull the latest image
	utils.LogInfo(fmt.Sprintf("Pulling the image %s:%s...\n", imageName, imageTag))
	if err := utils.PullImage(dockerHost, imageName, imageTag); err != nil {
		utils.LogError(fmt.Sprintf("Error pulling image: %v", err))
		return fmt.Errorf("failed to pull image: %w", err)
	}

	// Step 2: Run the container
	utils.LogInfo("Running the new container...")
	if err := utils.StartContainer(dockerHost, containerName); err != nil {
		utils.LogError(fmt.Sprintf("Error running container: %v", err))
		return fmt.Errorf("failed to run container: %w", err)
	}

	return nil
}

// StopFirmware stops the running container for the given LoxiLB instance.
func (s *LoxiLBService) StopFirmware(instance models.LoxiLBInstance) error {
	dockerHost := config.DockerBaseURL(instance.Host)
	containerName := instance.Name

	// Step 1: Stop the running container
	utils.LogInfo("Stopping the existing container...")
	if err := utils.StopContainer(dockerHost, containerName); err != nil {
		utils.LogError(fmt.Sprintf("Error stopping container: %v", err))
		return fmt.Errorf("failed to stop container: %w", err)
	}

	return nil
}

// UpdateFirmware stops and removes the existing container, then pulls the
// latest image and starts a new container for the given LoxiLB instance.
func (s *LoxiLBService) UpdateFirmware(instance models.LoxiLBInstance) error {
	dockerHost := config.DockerBaseURL(instance.Host)
	containerName := instance.Name
	imageName := instance.Cimage
	imageTag := instance.Ctag

	// Step 1: Stop the running container
	utils.LogInfo("Stopping the existing container...")
	if err := utils.StopContainer(dockerHost, containerName); err != nil {
		utils.LogError(fmt.Sprintf("Error stopping container: %v", err))
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// Step 2: Remove the old container
	utils.LogInfo("Removing the old container...")
	if err := utils.RemoveContainer(dockerHost, containerName); err != nil {
		utils.LogError(fmt.Sprintf("Error removing container: %v", err))
		return fmt.Errorf("failed to remove container: %w", err)
	}

	// Step 3: Pull the latest image
	utils.LogInfo(fmt.Sprintf("Pulling the image %s:%s...\n", imageName, imageTag))
	if err := utils.PullImage(dockerHost, imageName, imageTag); err != nil {
		utils.LogError(fmt.Sprintf("Error pulling image: %v", err))
		return fmt.Errorf("failed to pull image: %w", err)
	}

	// Step 4: Run the new container
	utils.LogInfo("Running the new container...")
	if err := utils.RunContainer(dockerHost, containerName, imageName, imageTag); err != nil {
		utils.LogError(fmt.Sprintf("Error running container: %v", err))
		return fmt.Errorf("failed to run container: %w", err)
	}

	return nil
}

package polling

import (
	"fmt"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"net/http"
	"time"
)

// PollingService handles periodic health checks
type PollingService struct {
	AlertService  *services.AlertService
	LoxiLBService *services.LoxiLBService
}

// NewPollingService initializes the polling service
func NewPollingService(alertService *services.AlertService, loxiLBService *services.LoxiLBService) *PollingService {
	return &PollingService{
		AlertService:  alertService,
		LoxiLBService: loxiLBService,
	}
}

// StartPolling starts the polling mechanism with dynamic instance refresh
// StartPolling runs indefinitely, health-checking each instance every
// pollInterval and refreshing the instance list every refreshInterval. It seeds
// the list once at startup, logging (but not failing on) any fetch error.
func (ps *PollingService) StartPolling(pollInterval, refreshInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	refreshTicker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	defer refreshTicker.Stop()

	instances, err := ps.LoxiLBService.FetchLoxiLBInstances()
	if err != nil {
		utils.LogError("[ERROR] Failed to fetch LoxiLB instances: " + err.Error())
	}

	for {
		select {
		case <-ticker.C:
			for _, instance := range instances {
				ps.checkInstanceHealth(instance)
			}

		case <-refreshTicker.C:
			instances, err = ps.LoxiLBService.FetchLoxiLBInstances()
			if err != nil {
				utils.LogError("[ERROR] Failed to refresh instances: " + err.Error())
			}
		}
	}
}

// checkInstanceHealth checks the health of a LoxiLB instance
func (ps *PollingService) checkInstanceHealth(instance models.LoxiLBInstance) {
	url := fmt.Sprintf("%s/version", instance.ApiEndpoint)
	resp, err := http.Get(url)

	if err != nil || resp.StatusCode != http.StatusOK {
		utils.LogError(fmt.Sprintf("Instance %s is unreachable: %s", instance.Name, err))
		ps.AlertService.CreateAlert(models.CreateAlertRequest{
			InstanceID: instance.ID,
			Type:       config.AlertTypeAPIUnreachable,
			Severity:   config.SeverityCritical,
			Message:    fmt.Sprintf("Instance %s is unreachable.", instance.Name),
		})
	}
}

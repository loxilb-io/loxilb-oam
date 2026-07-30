package services

import (
	"database/sql"
	"errors"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"time"
)

type AlertService struct {
	DB *sql.DB
}

func NewAlertService(db *sql.DB) *AlertService {
	return &AlertService{DB: db}
}

// CreateAlert validates the alert type and severity, inserts a new alert, and
// returns its ID.
func (s *AlertService) CreateAlert(alertReq models.CreateAlertRequest) (int, error) {
	var alertID int

	// Validate alert type and severity using constants
	validTypes := map[string]bool{
		config.AlertTypeDBDisconnect:   true,
		config.AlertTypeAPIUnreachable: true,
		config.AlertTypeHighCPU:        true,
		config.AlertTypeMemoryLeak:     true,
	}

	validSeverities := map[string]bool{
		config.SeverityInfo:     true,
		config.SeverityWarning:  true,
		config.SeverityCritical: true,
	}

	if !validTypes[alertReq.Type] {
		return 0, errors.New("invalid alert type")
	}

	if !validSeverities[alertReq.Severity] {
		return 0, errors.New("invalid severity level")
	}

	err := utils.RetryOperation(func() error {
		query := config.InsertAlertQuery
		result, err := s.DB.Exec(query, alertReq.InstanceID, alertReq.Type, alertReq.Severity, alertReq.Message)
		if err != nil {
			return err
		}

		lastInsertID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		alertID = int(lastInsertID)
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return alertID, err
}

// GetActiveAlerts returns all active alerts from the database.
func (s *AlertService) GetActiveAlerts() ([]models.Alert, error) {
	var alerts []models.Alert
	err := utils.RetryOperation(func() error {
		query := config.SelectAlertsQuery
		rows, err := s.DB.Query(query)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var alert models.Alert
			err := rows.Scan(&alert.ID, &alert.InstanceID, &alert.Type, &alert.Severity, &alert.Message, &alert.CreatedAt)
			if err != nil {
				return err
			}

			alerts = append(alerts, alert)
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return alerts, err
}

// AcknowledgeAlert records that userID acknowledged alertID, updates the alert
// with the acknowledgment time, and returns that time.
func (s *AlertService) AcknowledgeAlert(alertID, userID int) (time.Time, error) {
	ackTime := time.Now()

	err := utils.RetryOperation(func() error {
		query := config.InsertAckQuery
		_, err := s.DB.Exec(query, alertID, userID, ackTime)
		if err != nil {
			return err
		}

		updateQuery := config.UpdateAckQuery
		_, err = s.DB.Exec(updateQuery, ackTime, alertID)
		return err
	}, config.MaxRetries, config.RetryDelay)

	return ackTime, err
}

// GetAlertHistory returns alerts within the given time range, applying limit
// and offset. Zero start/end times default to the full range.
func (s *AlertService) GetAlertHistory(startTime, endTime time.Time, limit, offset int) ([]models.Alert, error) {
	var alerts []models.Alert

	if startTime.IsZero() {
		startTime = time.Unix(0, 0)
	}
	if endTime.IsZero() {
		endTime = time.Now()
	}

	if limit <= 0 {
		limit = config.DefaultLogLimit
	}
	if offset < 0 {
		offset = config.DefaultLogOffset
	}

	err := utils.RetryOperation(func() error {
		query := config.SelectAlertHistoryQuery + " LIMIT ? OFFSET ?"
		rows, err := s.DB.Query(query, startTime, endTime, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var alert models.Alert
			if err := rows.Scan(&alert.ID, &alert.InstanceID, &alert.Type, &alert.Severity, &alert.Message, &alert.CreatedAt, &alert.ResolvedAt); err != nil {
				return err
			}
			alerts = append(alerts, alert)
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return alerts, err
}

// GetActiveAlertsPaginated returns a page of active alerts (1-based page) and
// the total count of active alerts.
func (s *AlertService) GetActiveAlertsPaginated(page, limit int) ([]models.Alert, int, error) {
	var alerts []models.Alert
	var totalCount int

	// Calculate offset
	offset := (page - 1) * limit

	// Get total count
	err := utils.RetryOperation(func() error {
		query := config.SelectAlertsCountQuery
		row := s.DB.QueryRow(query)
		return row.Scan(&totalCount)
	}, config.MaxRetries, config.RetryDelay)

	if err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err = utils.RetryOperation(func() error {
		query := config.SelectAlertsQueryPaginated
		rows, err := s.DB.Query(query, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		alerts = []models.Alert{} // Reset slice
		for rows.Next() {
			var alert models.Alert
			err := rows.Scan(&alert.ID, &alert.InstanceID, &alert.Type, &alert.Severity, &alert.Message, &alert.CreatedAt)
			if err != nil {
				return err
			}
			alerts = append(alerts, alert)
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return alerts, totalCount, err
}

// GetAlertHistoryPaginated returns a page of alerts within the given time range
// (1-based page) and the total count within that range.
func (s *AlertService) GetAlertHistoryPaginated(startTime, endTime time.Time, page, limit int) ([]models.Alert, int, error) {
	var alerts []models.Alert
	var totalCount int

	// Set defaults for time range if not provided
	if startTime.IsZero() {
		startTime = time.Unix(0, 0)
	}
	if endTime.IsZero() {
		endTime = time.Now()
	}

	// Calculate offset
	offset := (page - 1) * limit

	// Get total count
	err := utils.RetryOperation(func() error {
		query := config.SelectAlertHistoryCountQuery
		row := s.DB.QueryRow(query, startTime, endTime)
		return row.Scan(&totalCount)
	}, config.MaxRetries, config.RetryDelay)

	if err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err = utils.RetryOperation(func() error {
		query := config.SelectAlertHistoryQueryPaginated
		rows, err := s.DB.Query(query, startTime, endTime, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		alerts = []models.Alert{} // Reset slice
		for rows.Next() {
			var alert models.Alert
			err := rows.Scan(&alert.ID, &alert.InstanceID, &alert.Type, &alert.Severity, &alert.Message, &alert.CreatedAt, &alert.ResolvedAt)
			if err != nil {
				return err
			}
			alerts = append(alerts, alert)
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return alerts, totalCount, err
}

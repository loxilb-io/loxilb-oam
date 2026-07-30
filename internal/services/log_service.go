package services

import (
	"database/sql"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"time"
)

type LogService struct {
	DB *sql.DB
}

func NewLogService(db *sql.DB) *LogService {
	return &LogService{DB: db}
}

// FetchLogs returns logs from the Logs table filtered by the given time range,
// with pagination via limit and offset.
func (s *LogService) FetchLogs(limit, offset int, startTime, endTime time.Time) ([]models.LogEntry, error) {
	var logs []models.LogEntry
	err := utils.RetryOperation(func() error {
		query := config.SelectLogsQuery

		rows, err := s.DB.Query(query, startTime, endTime, limit, offset)
		if err != nil {
			utils.LogError("Failed to fetch logs: " + err.Error())
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var log models.LogEntry

			err := rows.Scan(
				&log.ID, &log.Level, &log.Timestamp, &log.Severity, &log.Facility,
				&log.Programname, &log.Host, &log.Message, &log.CreatedAt,
			)

			if err != nil {
				utils.LogError("Failed to scan log: " + err.Error())
				return err
			}

			logs = append(logs, log)
		}

		if err = rows.Err(); err != nil {
			utils.LogError("Rows error: " + err.Error())
			return err
		}

		return nil
	}, config.MaxRetries, config.RetryDelay)
	return logs, err
}

// FetchLogsFromFile is a placeholder for reading logs from a file source.
// Log retrieval currently uses the database via FetchLogs.
func (s *LogService) FetchLogsFromFile() {
}

package services

// Snapshot scheduler, retention and integrity sweep.
// One background goroutine started from main.go; every tick
// it (1) takes due scheduled snapshots, (2) trims each instance's
// snapshot count to its retain_count (pinned and pre_upgrade exempt), and
// (3) once a day verifies every stored blob against its checksum.
// A failing step is logged and recorded — the loop itself never dies.

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

const (
	// SchedulerTick is how often the scheduler wakes up.
	SchedulerTick = 5 * time.Minute
	// IntegritySweepEvery is how often the full checksum sweep runs.
	IntegritySweepEvery = 24 * time.Hour
	// schedulerActor is recorded as created_by on scheduled snapshots.
	schedulerActor = "snapshot-scheduler"
)

// SnapshotScheduler drives scheduled snapshots, retention and integrity
// sweeps over the SnapshotService.
type SnapshotScheduler struct {
	svc       *SnapshotService
	lastSweep time.Time
	stop      chan struct{}
}

func NewSnapshotScheduler(svc *SnapshotService) *SnapshotScheduler {
	return &SnapshotScheduler{svc: svc, stop: make(chan struct{})}
}

// Start runs the scheduler loop until Stop is called. Call as a goroutine.
func (s *SnapshotScheduler) Start() {
	utils.LogInfo(fmt.Sprintf("snapshot scheduler started (tick %s, integrity sweep every %s)", SchedulerTick, IntegritySweepEvery))
	ticker := time.NewTicker(SchedulerTick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.RunOnce(time.Now())
		case <-s.stop:
			return
		}
	}
}

// Stop terminates the loop.
func (s *SnapshotScheduler) Stop() {
	close(s.stop)
}

// RunOnce executes one scheduler tick. Exported so tests (and testbed
// probes) can drive it without waiting for wall-clock ticks.
func (s *SnapshotScheduler) RunOnce(now time.Time) {
	s.runDueSchedules(now)
	s.runRetention()
	if now.Sub(s.lastSweep) >= IntegritySweepEvery {
		s.lastSweep = now
		s.runIntegritySweep()
	}
}

// ScheduleDue reports whether a schedule should fire at `now`.
func ScheduleDue(sched *models.InstanceSnapshotSchedule, now time.Time) bool {
	if !sched.Enabled {
		return false
	}
	if sched.LastRunAt == nil {
		return true
	}
	interval := time.Duration(sched.IntervalHours) * time.Hour
	return now.Sub(*sched.LastRunAt) >= interval
}

func (s *SnapshotScheduler) runDueSchedules(now time.Time) {
	rows, err := s.svc.DB.Query(config.SelectEnabledSnapshotSchedulesQuery)
	if err != nil {
		utils.LogError("snapshot scheduler: listing schedules: " + err.Error())
		return
	}
	var due []models.InstanceSnapshotSchedule
	for rows.Next() {
		var sched models.InstanceSnapshotSchedule
		var lastRunAt sql.NullTime
		var lastResult sql.NullString
		if err := rows.Scan(&sched.InstanceID, &sched.Enabled, &sched.IntervalHours, &sched.RetainCount, &lastRunAt, &lastResult); err != nil {
			utils.LogError("snapshot scheduler: scanning schedule: " + err.Error())
			continue
		}
		if lastRunAt.Valid {
			t := lastRunAt.Time
			sched.LastRunAt = &t
		}
		if ScheduleDue(&sched, now) {
			due = append(due, sched)
		}
	}
	rows.Close()

	for _, sched := range due {
		result := "ok"
		_, err := s.svc.TakeSnapshot(sched.InstanceID, models.TakeSnapshotRequest{
			Name:        fmt.Sprintf("scheduled-%s", now.UTC().Format("20060102-1504")),
			TriggerType: models.SnapshotTriggerScheduled,
		}, schedulerActor)
		if err != nil {
			result = err.Error()
			if len(result) > 255 {
				result = result[:255]
			}
			utils.LogError(fmt.Sprintf("snapshot scheduler: instance %d snapshot failed: %s", sched.InstanceID, err.Error()))
		} else {
			utils.LogInfo(fmt.Sprintf("snapshot scheduler: took scheduled snapshot of instance %d", sched.InstanceID))
		}
		if _, err := s.svc.DB.Exec(config.RecordSnapshotScheduleRunQuery, result, sched.InstanceID); err != nil {
			utils.LogError("snapshot scheduler: recording run: " + err.Error())
		}
	}
}

// RetentionVictims returns the ids to delete: everything past the first
// retain entries of a newest-first candidate list.
func RetentionVictims(newestFirst []string, retain int) []string {
	if retain < 1 {
		retain = 1
	}
	if len(newestFirst) <= retain {
		return nil
	}
	return newestFirst[retain:]
}

func (s *SnapshotScheduler) runRetention() {
	// retain_count per instance; instances without a schedule row keep the
	// default 10.
	retainByInstance := map[int]int{}
	rows, err := s.svc.DB.Query(config.SelectEnabledSnapshotSchedulesQuery)
	if err == nil {
		for rows.Next() {
			var sched models.InstanceSnapshotSchedule
			var lastRunAt sql.NullTime
			var lastResult sql.NullString
			if err := rows.Scan(&sched.InstanceID, &sched.Enabled, &sched.IntervalHours, &sched.RetainCount, &lastRunAt, &lastResult); err == nil {
				retainByInstance[sched.InstanceID] = sched.RetainCount
			}
		}
		rows.Close()
	}

	instRows, err := s.svc.DB.Query(config.SelectSnapshotInstanceIDsQuery)
	if err != nil {
		utils.LogError("snapshot scheduler: listing snapshot instances: " + err.Error())
		return
	}
	var instanceIDs []int
	for instRows.Next() {
		var id int
		if err := instRows.Scan(&id); err == nil {
			instanceIDs = append(instanceIDs, id)
		}
	}
	instRows.Close()

	for _, instanceID := range instanceIDs {
		retain, ok := retainByInstance[instanceID]
		if !ok {
			retain = 10
		}
		candRows, err := s.svc.DB.Query(config.SelectSnapshotRetentionCandidatesQuery, instanceID)
		if err != nil {
			utils.LogError("snapshot scheduler: listing retention candidates: " + err.Error())
			continue
		}
		var ids []string
		for candRows.Next() {
			var id string
			if err := candRows.Scan(&id); err == nil {
				ids = append(ids, id)
			}
		}
		candRows.Close()

		for _, id := range RetentionVictims(ids, retain) {
			if _, err := s.svc.DB.Exec(config.DeleteSnapshotQuery, id); err != nil {
				utils.LogError("snapshot scheduler: retention delete failed: " + err.Error())
				continue
			}
			utils.LogInfo(fmt.Sprintf("snapshot scheduler: retention deleted snapshot %s of instance %d (keep %d)", id, instanceID, retain))
		}
	}
}

// runIntegritySweep re-verifies every stored blob. GetSnapshotDocument
// already marks checksum_ok both ways (flags corruption, heals recovered
// rows), so the sweep just walks the ids.
func (s *SnapshotScheduler) runIntegritySweep() {
	rows, err := s.svc.DB.Query(config.SelectAllSnapshotIDsQuery)
	if err != nil {
		utils.LogError("snapshot scheduler: integrity sweep listing: " + err.Error())
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	bad := 0
	for _, id := range ids {
		if _, _, err := s.svc.GetSnapshotDocument(id); err != nil {
			bad++
			utils.LogError(fmt.Sprintf("snapshot scheduler: integrity sweep: snapshot %s failed verification: %v", id, err))
		}
	}
	utils.LogInfo(fmt.Sprintf("snapshot scheduler: integrity sweep complete: %d snapshots checked, %d failed", len(ids), bad))
}

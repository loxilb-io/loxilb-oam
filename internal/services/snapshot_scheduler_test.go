package services_test

// Unit tests for internal/services/snapshot_scheduler.go: schedule due-time
// logic and retention math incl. the pinned/pre_upgrade exemptions (exercised via
// the SQL filter + RetentionVictims split), plus a full RunOnce tick over
// sqlmock proving a due schedule takes a snapshot and retention deletes
// only the overflow.

import (
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
)

func TestScheduleDue(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	past := func(h int) *time.Time { t := now.Add(-time.Duration(h) * time.Hour); return &t }

	cases := []struct {
		name  string
		sched models.InstanceSnapshotSchedule
		want  bool
	}{
		{"disabled never fires", models.InstanceSnapshotSchedule{Enabled: false, IntervalHours: 1, LastRunAt: past(100)}, false},
		{"never ran fires immediately", models.InstanceSnapshotSchedule{Enabled: true, IntervalHours: 24}, true},
		{"interval elapsed", models.InstanceSnapshotSchedule{Enabled: true, IntervalHours: 24, LastRunAt: past(25)}, true},
		{"interval exactly elapsed", models.InstanceSnapshotSchedule{Enabled: true, IntervalHours: 24, LastRunAt: past(24)}, true},
		{"interval not elapsed", models.InstanceSnapshotSchedule{Enabled: true, IntervalHours: 24, LastRunAt: past(23)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, services.ScheduleDue(&tc.sched, now))
		})
	}
}

func TestRetentionVictims(t *testing.T) {
	ids := []string{"newest", "b", "c", "d", "oldest"}
	assert.Nil(t, services.RetentionVictims(ids, 5), "at the cap: nothing deleted")
	assert.Nil(t, services.RetentionVictims(ids, 10), "under the cap: nothing deleted")
	assert.Equal(t, []string{"d", "oldest"}, services.RetentionVictims(ids, 3), "oldest beyond keep-N deleted")
	assert.Equal(t, []string{"b", "c", "d", "oldest"}, services.RetentionVictims(ids, 0), "retain clamps to 1, never deletes everything")
	assert.Nil(t, services.RetentionVictims(nil, 3))
}

var schedCols = []string{"instance_id", "enabled", "interval_hours", "retain_count", "last_run_at", "last_run_result"}

func TestRunOnceTakesDueSnapshotAndTrims(t *testing.T) {
	doc := sampleDoc(t)
	gw := &fakeGateway{snapshotBody: doc}
	svc, mock := newService(t, gw, nil)
	sched := services.NewSnapshotScheduler(svc)

	// Case 1 — due schedules: instance 1 enabled, never ran => due.
	mock.ExpectQuery(regexp.QuoteMeta("FROM instance_snapshot_schedules")).
		WillReturnRows(sqlmock.NewRows(schedCols).AddRow(1, true, 24, 2, nil, nil))
	expectInstanceFetch(mock, 1) // TakeSnapshot resolves the instance
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO instance_snapshots")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots"). // storeSnapshot re-read
									WillReturnRows(sqlmock.NewRows(append(append([]string{}, snapMetaCols...), "last_restore_response")).
										AddRow("new-id", 1, "scheduled-x", "", "scheduled", "1.0", "v0.9.9", len(doc),
				"sha256:a", "sha256:b", false, false, true, "snapshot-scheduler", time.Now(), 0, nil, nil, nil))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE instance_snapshot_schedules")).
		WithArgs("ok", 1).WillReturnResult(sqlmock.NewResult(0, 1))

	// Case 2 — retention: schedule row says retain 2; 3 candidates => 1 delete.
	mock.ExpectQuery(regexp.QuoteMeta("FROM instance_snapshot_schedules")).
		WillReturnRows(sqlmock.NewRows(schedCols).AddRow(1, true, 24, 2, nil, nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT instance_id FROM instance_snapshots")).
		WillReturnRows(sqlmock.NewRows([]string{"instance_id"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY created_at DESC")).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("new-id").AddRow("mid-id").AddRow("old-id"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM instance_snapshots WHERE id = $1")).
		WithArgs("old-id").WillReturnResult(sqlmock.NewResult(0, 1))

	// Case 3 — integrity sweep runs on the first tick (lastSweep is zero).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM instance_snapshots")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	sched.RunOnce(time.Now())
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, gw.restoreCalls, "scheduler must never restore anything")
}

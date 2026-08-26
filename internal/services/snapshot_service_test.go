package services_test

// Unit tests for internal/services/snapshot_service.go:
// checksum-mismatch rejection, encrypt/decrypt round trip, oversize (413)
// rejection, gateway-error passthrough, pinned-delete protection, restore
// orchestration incl. the mandatory pre_restore safety snapshot.
// The gateway client is mocked; the DB is go-sqlmock.

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
)

// fakeGateway is a scriptable SnapshotGatewayClient.
type fakeGateway struct {
	snapshotBody   []byte
	snapshotHeader http.Header
	snapshotErr    error

	restoreStatus int
	restoreBody   []byte
	restoreErr    error
	restoreCalls  []string // modes, in order
	restoreDocs   [][]byte
}

func (f *fakeGateway) FetchSnapshot(_ *models.LoxiLBInstance) ([]byte, http.Header, error) {
	if f.snapshotErr != nil {
		return nil, nil, f.snapshotErr
	}
	h := f.snapshotHeader
	if h == nil {
		h = http.Header{}
	}
	return f.snapshotBody, h, nil
}

func (f *fakeGateway) Restore(_ *models.LoxiLBInstance, doc []byte, mode string) (int, []byte, error) {
	f.restoreCalls = append(f.restoreCalls, mode)
	f.restoreDocs = append(f.restoreDocs, doc)
	if f.restoreErr != nil {
		return 0, nil, f.restoreErr
	}
	return f.restoreStatus, f.restoreBody, nil
}

// sampleDoc builds a minimal but envelope-valid snapshot document.
func sampleDoc(t *testing.T) []byte {
	t.Helper()
	doc := map[string]interface{}{
		"schema_version":  "1.0",
		"kind":            "loxilb-snapshot",
		"gateway_version": "v0.9.9",
		"checksum":        "sha256:" + strings.Repeat("ab", 32),
		"domains":         map[string]interface{}{"loadbalancer": []interface{}{}},
	}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	return b
}

func newService(t *testing.T, gw services.SnapshotGatewayClient, encKey []byte) (*services.SnapshotService, sqlmock.Sqlmock) {
	t.Helper()
	if encKey != nil {
		os.Setenv("SNAPSHOT_ENC_KEY", base64.StdEncoding.EncodeToString(encKey))
	} else {
		os.Unsetenv("SNAPSHOT_ENC_KEY")
	}
	t.Cleanup(func() { os.Unsetenv("SNAPSHOT_ENC_KEY") })

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	svc, err := services.NewSnapshotService(db, services.NewLoxiLBService(db))
	require.NoError(t, err)
	if gw != nil {
		svc.SetGatewayClient(gw)
	}
	return svc, mock
}

var instanceCols = []string{"id", "name", "host", "port", "protocol", "description",
	"version", "api_endpoint", "cimage", "ctag", "is_active", "created_at"}

func instanceRow(_ sqlmock.Sqlmock, id int) *sqlmock.Rows {
	return sqlmock.NewRows(instanceCols).AddRow(id, fmt.Sprintf("inst-%d", id), "10.0.0.12", "11111",
		"http", "", "v1", "http://10.0.0.12:11111/netlox/v1", "img", "tag", true, time.Now())
}

func expectInstanceFetch(mock sqlmock.Sqlmock, id int) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances WHERE id = $1")).
		WithArgs(id).WillReturnRows(instanceRow(mock, id))
}

var snapMetaCols = []string{"id", "instance_id", "name", "description", "trigger_type",
	"schema_version", "gateway_version", "size_bytes", "checksum", "stored_checksum",
	"encrypted", "pinned", "checksum_ok", "created_by", "created_at", "restore_count",
	"last_restored_at", "last_restore_result"}

func TestNewSnapshotServiceRejectsBadKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	os.Setenv("SNAPSHOT_ENC_KEY", "not-base64!!!")
	defer os.Unsetenv("SNAPSHOT_ENC_KEY")
	_, err = services.NewSnapshotService(db, services.NewLoxiLBService(db))
	assert.Error(t, err, "invalid base64 key must be a hard startup error")

	os.Setenv("SNAPSHOT_ENC_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = services.NewSnapshotService(db, services.NewLoxiLBService(db))
	assert.Error(t, err, "wrong-length key must be a hard startup error")
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	for name, enc := range map[string][]byte{"encrypted": key, "plaintext": nil} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService(t, nil, enc)
			raw := sampleDoc(t)
			blob, encrypted, err := svc.SealBlob(raw)
			require.NoError(t, err)
			assert.Equal(t, enc != nil, encrypted)
			assert.NotEqual(t, raw, blob, "blob must never be the raw document")

			got, err := svc.OpenBlob(blob, encrypted)
			require.NoError(t, err)
			assert.Equal(t, raw, got)
		})
	}
}

func TestOpenBlobDetectsTamper(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc, _ := newService(t, nil, key)
	blob, encrypted, err := svc.SealBlob(sampleDoc(t))
	require.NoError(t, err)
	require.True(t, encrypted)

	blob[len(blob)/2] ^= 0xff // bit-flip in the middle
	_, err = svc.OpenBlob(blob, true)
	assert.ErrorIs(t, err, services.ErrSnapshotCorrupted, "GCM must reject a bit-flipped blob")
}

func TestTakeSnapshotHappyPath(t *testing.T) {
	doc := sampleDoc(t)
	var env struct {
		Checksum string `json:"checksum"`
	}
	require.NoError(t, json.Unmarshal(doc, &env))
	header := http.Header{}
	header.Set("X-Snapshot-Checksum", env.Checksum)

	gw := &fakeGateway{snapshotBody: doc, snapshotHeader: header}
	svc, mock := newService(t, gw, nil)

	expectInstanceFetch(mock, 1)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO instance_snapshots")).
		WithArgs(sqlmock.AnyArg(), 1, "nightly", "desc", "manual", "1.0", "v0.9.9",
			len(doc), env.Checksum, sqlmock.AnyArg(), sqlmock.AnyArg(), false, false, "admin").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots").
		WillReturnRows(sqlmock.NewRows(append(append([]string{}, snapMetaCols...), "last_restore_response")).
			AddRow("some-id", 1, "nightly", "desc", "manual", "1.0", "v0.9.9", len(doc),
				env.Checksum, "sha256:x", false, false, true, "admin", time.Now(), 0, nil, nil, nil))

	snap, err := svc.TakeSnapshot(1, models.TakeSnapshotRequest{Name: "nightly", Description: "desc"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, "nightly", snap.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTakeSnapshotRejectsChecksumHeaderMismatch(t *testing.T) {
	header := http.Header{}
	header.Set("X-Snapshot-Checksum", "sha256:"+strings.Repeat("00", 32))
	gw := &fakeGateway{snapshotBody: sampleDoc(t), snapshotHeader: header}
	svc, mock := newService(t, gw, nil)

	expectInstanceFetch(mock, 1)
	_, err := svc.TakeSnapshot(1, models.TakeSnapshotRequest{}, "admin")
	assert.ErrorIs(t, err, services.ErrInvalidSnapshotDoc)
	assert.Contains(t, err.Error(), "X-Snapshot-Checksum")
}

func TestTakeSnapshotRejectsOversize(t *testing.T) {
	big := make([]byte, services.MaxSnapshotBytes+1)
	gw := &fakeGateway{snapshotBody: big}
	svc, mock := newService(t, gw, nil)

	expectInstanceFetch(mock, 1)
	_, err := svc.TakeSnapshot(1, models.TakeSnapshotRequest{}, "admin")
	assert.ErrorIs(t, err, services.ErrSnapshotTooLarge)
}

func TestTakeSnapshotRejectsNonSnapshotBody(t *testing.T) {
	gw := &fakeGateway{snapshotBody: []byte(`{"kind":"something-else","schema_version":"1.0","checksum":"sha256:aa"}`)}
	svc, mock := newService(t, gw, nil)

	expectInstanceFetch(mock, 1)
	_, err := svc.TakeSnapshot(1, models.TakeSnapshotRequest{}, "admin")
	assert.ErrorIs(t, err, services.ErrInvalidSnapshotDoc)
}

func TestTakeSnapshotPassesGatewayErrorThrough(t *testing.T) {
	gwErr := &services.GatewayError{StatusCode: 503, Body: `{"error":"config gate is busy"}`}
	gw := &fakeGateway{snapshotErr: gwErr}
	svc, mock := newService(t, gw, nil)

	expectInstanceFetch(mock, 1)
	_, err := svc.TakeSnapshot(1, models.TakeSnapshotRequest{}, "admin")
	var g *services.GatewayError
	require.ErrorAs(t, err, &g)
	assert.Equal(t, 503, g.StatusCode)
	assert.Contains(t, g.Body, "config gate is busy", "gateway error text must pass through verbatim")
}

func TestDeleteSnapshotPinnedRequiresForce(t *testing.T) {
	svc, mock := newService(t, nil, nil)

	pinnedRow := func() *sqlmock.Rows {
		return sqlmock.NewRows(append(append([]string{}, snapMetaCols...), "last_restore_response")).
			AddRow("sid-1", 1, "keeper", "", "manual", "1.0", "v0.9.9", 10,
				"sha256:a", "sha256:b", false, true, true, "admin", time.Now(), 0, nil, nil, nil)
	}
	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots").WillReturnRows(pinnedRow())
	err := svc.DeleteSnapshot("sid-1", false)
	assert.ErrorIs(t, err, services.ErrSnapshotPinned)

	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots").WillReturnRows(pinnedRow())
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM instance_snapshots WHERE id = $1")).
		WithArgs("sid-1").WillReturnResult(sqlmock.NewResult(0, 1))
	assert.NoError(t, svc.DeleteSnapshot("sid-1", true))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSnapshotNotFound(t *testing.T) {
	svc, mock := newService(t, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots").WillReturnError(sql.ErrNoRows)
	_, err := svc.GetSnapshot("nope")
	assert.ErrorIs(t, err, services.ErrSnapshotNotFound)
}

// expectSnapshotBlobFetch primes the blob query for snapshot sid-1 of
// instance 1 with the given sealed blob and stored checksum.
func expectSnapshotBlobFetch(mock sqlmock.Sqlmock, blob []byte, storedChecksum string, encrypted bool) {
	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots").
		WillReturnRows(sqlmock.NewRows(append(append([]string{}, snapMetaCols...), "snapshot_blob")).
			AddRow("sid-1", 1, "snap", "", "manual", "1.0", "v0.9.9", 10,
				"sha256:a", storedChecksum, encrypted, false, true, "admin", time.Now(), 0, nil, nil, blob))
}

func TestGetSnapshotDocumentRejectsTamperedPlaintext(t *testing.T) {
	svc, mock := newService(t, nil, nil)
	raw := sampleDoc(t)
	blob, _, err := svc.SealBlob(raw)
	require.NoError(t, err)

	// Stored checksum belongs to different content: simulates a blob swapped
	// or bit-flipped inside the DB (plaintext-gzip mode, so gzip may still
	// inflate fine — the checksum has to catch it).
	expectSnapshotBlobFetch(mock, blob, "sha256:"+strings.Repeat("00", 32), false)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE instance_snapshots SET checksum_ok = $1")).
		WithArgs(false, "sid-1").WillReturnResult(sqlmock.NewResult(0, 1))

	_, _, err = svc.GetSnapshotDocument("sid-1")
	assert.ErrorIs(t, err, services.ErrSnapshotCorrupted)
	assert.NoError(t, mock.ExpectationsWereMet(), "corrupted row must be marked checksum_ok=false")
}

func TestRestoreDryRunDoesNotTakePreSnapshotOrRecord(t *testing.T) {
	raw := sampleDoc(t)
	gw := &fakeGateway{restoreStatus: 200, restoreBody: []byte(`{"mode":"dry-run","compatible":true,"plan":[]}`)}
	svc, mock := newService(t, gw, nil)
	blob, _, err := svc.SealBlob(raw)
	require.NoError(t, err)

	expectSnapshotBlobFetch(mock, blob, services.RawChecksum(raw), false)
	expectInstanceFetch(mock, 1)

	out, err := svc.RestoreSnapshot("sid-1", models.RestoreSnapshotRequest{}, "admin")
	require.NoError(t, err)
	assert.Equal(t, []string{"dry-run"}, gw.restoreCalls)
	assert.Equal(t, raw, gw.restoreDocs[0], "gateway must receive the exact stored document")
	assert.Equal(t, 200, out.GatewayStatus)
	assert.Empty(t, out.PreRestoreSnapshotID)
	assert.False(t, out.CrossInstance)
	// No INSERT (pre-snapshot) and no UPDATE (audit) were expected on mock:
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRestoreCommitTakesPreSnapshotAndRecords(t *testing.T) {
	raw := sampleDoc(t)
	gw := &fakeGateway{
		snapshotBody:  raw, // served for the pre_restore safety snapshot
		restoreStatus: 200,
		restoreBody:   []byte(`{"mode":"commit","result":"ok"}`),
	}
	svc, mock := newService(t, gw, nil)
	blob, _, err := svc.SealBlob(raw)
	require.NoError(t, err)

	expectSnapshotBlobFetch(mock, blob, services.RawChecksum(raw), false)
	expectInstanceFetch(mock, 1) // resolve restore target
	expectInstanceFetch(mock, 1) // TakeSnapshot(pre_restore) resolves it again
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO instance_snapshots")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT (.+) FROM instance_snapshots"). // storeSnapshot re-reads the row
									WillReturnRows(sqlmock.NewRows(append(append([]string{}, snapMetaCols...), "last_restore_response")).
										AddRow("pre-id", 1, "pre-restore-x", "", "pre_restore", "1.0", "v0.9.9", len(raw),
				"sha256:a", "sha256:b", false, false, true, "admin", time.Now(), 0, nil, nil, nil))
	mock.ExpectExec("UPDATE instance_snapshots").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "sid-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	out, err := svc.RestoreSnapshot("sid-1", models.RestoreSnapshotRequest{Mode: "commit"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, "pre-id", out.PreRestoreSnapshotID)
	assert.Equal(t, []string{"commit"}, gw.restoreCalls)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRestoreCommitAbortsWhenPreSnapshotFails(t *testing.T) {
	raw := sampleDoc(t)
	gw := &fakeGateway{
		snapshotErr:   &services.GatewayError{Body: "connection refused"},
		restoreStatus: 200, restoreBody: []byte(`{}`),
	}
	svc, mock := newService(t, gw, nil)
	blob, _, err := svc.SealBlob(raw)
	require.NoError(t, err)

	expectSnapshotBlobFetch(mock, blob, services.RawChecksum(raw), false)
	expectInstanceFetch(mock, 1)
	expectInstanceFetch(mock, 1)

	_, err = svc.RestoreSnapshot("sid-1", models.RestoreSnapshotRequest{Mode: "commit"}, "admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pre-restore safety snapshot")
	assert.Empty(t, gw.restoreCalls, "gateway restore must NOT run without the safety net")
}

func TestRestoreRejectsBadMode(t *testing.T) {
	svc, _ := newService(t, nil, nil)
	_, err := svc.RestoreSnapshot("sid-1", models.RestoreSnapshotRequest{Mode: "yolo"}, "admin")
	assert.ErrorIs(t, err, services.ErrInvalidSnapshotDoc)
}

func TestMapGatewayRestoreResult(t *testing.T) {
	cases := map[string]*string{
		"ok":              strPtr("ok"),
		"rolled-back":     strPtr("rolled_back"),
		"ROLLBACK-FAILED": strPtr("rollback_failed"),
		"":                nil,
		"garbage":         nil,
	}
	for in, want := range cases {
		got := services.MapGatewayRestoreResult(in)
		if want == nil {
			assert.Nil(t, got, "input %q", in)
		} else {
			require.NotNil(t, got, "input %q", in)
			assert.Equal(t, *want, *got, "input %q", in)
		}
	}
}

func strPtr(s string) *string { return &s }

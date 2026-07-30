package services

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

// Instance snapshot orchestration service.
//
// OAM is a thin orchestrator over the gateway snapshot primitive: it stores
// the snapshot document as an opaque blob (gzip, AES-256-GCM when
// SNAPSHOT_ENC_KEY is set) plus envelope metadata IN MySQL — never on
// container-local disk, which is what broke the legacy config-export feature.

// MaxSnapshotBytes is the uncompressed size cap (matches the MEDIUMBLOB
// budget with generous headroom; also closes the legacy unbounded
// io.ReadAll defect). Oversize is rejected with ErrSnapshotTooLarge (413).
const MaxSnapshotBytes = 16 << 20

// snapshotEncKeyEnv holds the base64-encoded 32-byte AES-256 key for
// at-rest encryption. Unset => plaintext-gzip plus a startup warning.
const snapshotEncKeyEnv = "SNAPSHOT_ENC_KEY"

// Sentinel errors the handler layer maps to HTTP statuses.
var (
	ErrSnapshotNotFound   = errors.New("snapshot not found")
	ErrInstanceNotFound   = errors.New("LoxiLB instance not found")
	ErrSnapshotPinned     = errors.New("snapshot is pinned; delete requires force=true")
	ErrSnapshotTooLarge   = fmt.Errorf("snapshot exceeds the %d MB size limit", MaxSnapshotBytes>>20)
	ErrSnapshotCorrupted  = errors.New("stored snapshot blob failed integrity verification")
	ErrInvalidSnapshotDoc = errors.New("invalid snapshot document")
)

// GatewayError carries a gateway (or connection) failure through to the
// handler verbatim — the design forbids OAM-side rewording of gateway
// errors (lesson from the legacy download-404 UX). StatusCode 0 means the
// gateway was unreachable (mapped to 502 by the handler).
type GatewayError struct {
	StatusCode int
	Body       string
}

func (e *GatewayError) Error() string {
	if e.StatusCode == 0 {
		return e.Body
	}
	return fmt.Sprintf("gateway returned %d: %s", e.StatusCode, e.Body)
}

// SnapshotGatewayClient is the gateway-facing surface, injected so unit
// tests can mock it.
type SnapshotGatewayClient interface {
	// FetchSnapshot GETs {api_endpoint}/config/snapshot and returns the raw
	// document bytes plus response headers.
	FetchSnapshot(instance *models.LoxiLBInstance) ([]byte, http.Header, error)
	// Restore POSTs the raw document to {api_endpoint}/config/restore?mode=…
	// and returns the gateway's status code and raw response body.
	Restore(instance *models.LoxiLBInstance, doc []byte, mode string) (int, []byte, error)
}

// httpGatewayClient is the production SnapshotGatewayClient.
type httpGatewayClient struct {
	take    *http.Client
	restore *http.Client
}

func newHTTPGatewayClient() *httpGatewayClient {
	// Same TLS posture as ProxyService, centralized in config.InstanceTLSConfig
	// (verify by default; CA-bundle or explicit-insecure opt-in via env).
	tr := func() *http.Transport {
		return &http.Transport{
			TLSClientConfig:   config.InstanceTLSConfig(),
			DisableKeepAlives: true,
		}
	}
	return &httpGatewayClient{
		take: &http.Client{Transport: tr(), Timeout: 60 * time.Second},
		// A commit restore runs the gateway's full preserve→wipe→apply→verify
		// pipeline; give it far more headroom than a normal proxy call.
		restore: &http.Client{Transport: tr(), Timeout: 5 * time.Minute},
	}
}

func gatewayBaseURL(instance *models.LoxiLBInstance) string {
	return strings.TrimSuffix(instance.ApiEndpoint, "/")
}

func (g *httpGatewayClient) FetchSnapshot(instance *models.LoxiLBInstance) ([]byte, http.Header, error) {
	url := gatewayBaseURL(instance) + "/config/snapshot"
	resp, err := g.take.Get(url)
	if err != nil {
		return nil, nil, &GatewayError{Body: err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSnapshotBytes+1))
	if err != nil {
		return nil, nil, &GatewayError{Body: fmt.Sprintf("reading gateway response: %v", err)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, &GatewayError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, resp.Header, nil
}

func (g *httpGatewayClient) Restore(instance *models.LoxiLBInstance, doc []byte, mode string) (int, []byte, error) {
	url := gatewayBaseURL(instance) + "/config/restore?mode=" + mode
	resp, err := g.restore.Post(url, "application/json", bytes.NewReader(doc))
	if err != nil {
		return 0, nil, &GatewayError{Body: err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSnapshotBytes))
	if err != nil {
		return 0, nil, &GatewayError{Body: fmt.Sprintf("reading gateway response: %v", err)}
	}
	return resp.StatusCode, body, nil
}

// snapshotEnvelope is the only part of the snapshot document OAM ever
// parses: the document is treated as an opaque blob plus envelope metadata.
// Loose unmarshal — every other field is deliberately ignored.
type snapshotEnvelope struct {
	SchemaVersion  string `json:"schema_version"`
	Kind           string `json:"kind"`
	GatewayVersion string `json:"gateway_version"`
	Checksum       string `json:"checksum"`
}

// parseSnapshotEnvelope validates that raw looks like a gateway snapshot
// document and extracts its envelope metadata.
func parseSnapshotEnvelope(raw []byte) (*snapshotEnvelope, error) {
	var env snapshotEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: not valid JSON: %v", ErrInvalidSnapshotDoc, err)
	}
	if env.Kind != "loxilb-snapshot" {
		return nil, fmt.Errorf("%w: kind %q is not \"loxilb-snapshot\"", ErrInvalidSnapshotDoc, env.Kind)
	}
	if env.SchemaVersion == "" || env.Checksum == "" {
		return nil, fmt.Errorf("%w: envelope missing schema_version or checksum", ErrInvalidSnapshotDoc)
	}
	if !strings.HasPrefix(env.Checksum, "sha256:") {
		return nil, fmt.Errorf("%w: malformed checksum %q", ErrInvalidSnapshotDoc, env.Checksum)
	}
	return &env, nil
}

// RawChecksum is the OAM-computed "sha256:<hex>" over the raw document
// bytes exactly as received. The gateway's own checksum is over canonical
// JSON with the checksum field blanked, which OAM cannot recompute without
// deep-parsing — so integrity sweeps and tamper rejection verify THIS one.
func RawChecksum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// newUUID returns a random (v4) UUID string without adding a dependency.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating snapshot id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// SnapshotService orchestrates take/store/restore of gateway config
// snapshots for managed instances.
type SnapshotService struct {
	DB            *sql.DB
	loxilbService *LoxiLBService
	gateway       SnapshotGatewayClient
	encKey        []byte // nil => store plaintext-gzip
}

// NewSnapshotService wires the service. A set-but-invalid SNAPSHOT_ENC_KEY
// is a hard error — silently downgrading to plaintext when the operator
// asked for encryption would be worse than failing to boot.
func NewSnapshotService(db *sql.DB, loxilbService *LoxiLBService) (*SnapshotService, error) {
	s := &SnapshotService{
		DB:            db,
		loxilbService: loxilbService,
		gateway:       newHTTPGatewayClient(),
	}
	if v := os.Getenv(snapshotEncKeyEnv); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("%s is not valid base64: %w", snapshotEncKeyEnv, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to 32 bytes (AES-256), got %d", snapshotEncKeyEnv, len(key))
		}
		s.encKey = key
	} else {
		utils.LogError("SNAPSHOT_ENC_KEY is not set — instance snapshots (which contain IPsec PSKs and certificate private keys) will be stored UNENCRYPTED in MySQL. Set a 32-byte base64 key in production.")
	}
	return s, nil
}

// SetGatewayClient swaps the gateway client (tests inject a mock here).
func (s *SnapshotService) SetGatewayClient(gw SnapshotGatewayClient) {
	s.gateway = gw
}

// SealBlob gzips raw and encrypts it (nonce prepended) when a key is
// configured.
func (s *SnapshotService) SealBlob(raw []byte) (blob []byte, encrypted bool, err error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, false, fmt.Errorf("compressing snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, false, fmt.Errorf("compressing snapshot: %w", err)
	}
	if s.encKey == nil {
		return buf.Bytes(), false, nil
	}
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return nil, false, fmt.Errorf("initializing cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false, fmt.Errorf("initializing cipher: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, false, fmt.Errorf("generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, buf.Bytes(), nil), true, nil
}

// OpenBlob reverses SealBlob. Decryption failure on an encrypted blob is
// authenticated-tamper detection and surfaces as ErrSnapshotCorrupted.
func (s *SnapshotService) OpenBlob(blob []byte, encrypted bool) ([]byte, error) {
	if encrypted {
		if s.encKey == nil {
			return nil, fmt.Errorf("snapshot is encrypted but %s is not configured", snapshotEncKeyEnv)
		}
		block, err := aes.NewCipher(s.encKey)
		if err != nil {
			return nil, fmt.Errorf("initializing cipher: %w", err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("initializing cipher: %w", err)
		}
		if len(blob) < gcm.NonceSize() {
			return nil, ErrSnapshotCorrupted
		}
		blob, err = gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
		if err != nil {
			return nil, ErrSnapshotCorrupted
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, ErrSnapshotCorrupted
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, MaxSnapshotBytes+1))
	if err != nil {
		return nil, ErrSnapshotCorrupted
	}
	if len(raw) > MaxSnapshotBytes {
		return nil, ErrSnapshotCorrupted
	}
	return raw, nil
}

func validTriggerType(t string) bool {
	switch t {
	case models.SnapshotTriggerManual, models.SnapshotTriggerScheduled,
		models.SnapshotTriggerPreRestore, models.SnapshotTriggerPreUpgrade:
		return true
	}
	return false
}

// TakeSnapshot pulls a fresh snapshot from the instance's gateway and
// stores it. Gateway failures pass through verbatim as *GatewayError.
func (s *SnapshotService) TakeSnapshot(instanceID int, req models.TakeSnapshotRequest, createdBy string) (*models.InstanceSnapshot, error) {
	trigger := req.TriggerType
	if trigger == "" {
		trigger = models.SnapshotTriggerManual
	}
	if !validTriggerType(trigger) {
		return nil, fmt.Errorf("%w: unknown trigger_type %q", ErrInvalidSnapshotDoc, req.TriggerType)
	}
	instance, err := s.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstanceNotFound, err)
	}
	raw, header, err := s.gateway.FetchSnapshot(instance)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxSnapshotBytes {
		return nil, ErrSnapshotTooLarge
	}
	env, err := parseSnapshotEnvelope(raw)
	if err != nil {
		return nil, err
	}
	// The gateway sends its checksum both in the envelope and as a response
	// header; a mismatch means the body was mangled in transit.
	if hc := header.Get("X-Snapshot-Checksum"); hc != "" && hc != env.Checksum {
		return nil, fmt.Errorf("%w: X-Snapshot-Checksum header %q does not match document checksum %q", ErrInvalidSnapshotDoc, hc, env.Checksum)
	}
	name := req.Name
	if name == "" {
		name = fmt.Sprintf("%s-%s", instance.Name, time.Now().UTC().Format("20060102-150405"))
	}
	return s.storeSnapshot(instance.ID, name, req.Description, trigger, env, raw, createdBy)
}

// ImportSnapshot stores an off-box snapshot archive uploaded by the
// operator. Envelope-only validation — deep validation stays the gateway's
// job at restore time.
func (s *SnapshotService) ImportSnapshot(instanceID int, name, description string, raw []byte, createdBy string) (*models.InstanceSnapshot, error) {
	if len(raw) > MaxSnapshotBytes {
		return nil, ErrSnapshotTooLarge
	}
	instance, err := s.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstanceNotFound, err)
	}
	env, err := parseSnapshotEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = fmt.Sprintf("upload-%s", time.Now().UTC().Format("20060102-150405"))
	}
	return s.storeSnapshot(instance.ID, name, description, models.SnapshotTriggerManual, env, raw, createdBy)
}

func (s *SnapshotService) storeSnapshot(instanceID int, name, description, trigger string, env *snapshotEnvelope, raw []byte, createdBy string) (*models.InstanceSnapshot, error) {
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	blob, encrypted, err := s.SealBlob(raw)
	if err != nil {
		return nil, err
	}
	stored := RawChecksum(raw)
	if len(name) > 128 {
		name = name[:128]
	}
	_, err = s.DB.Exec(config.InsertInstanceSnapshotQuery,
		id, instanceID, name, description, trigger, env.SchemaVersion, env.GatewayVersion,
		len(raw), env.Checksum, stored, blob, encrypted, false, createdBy)
	if err != nil {
		utils.LogError("Failed to store snapshot: " + err.Error())
		return nil, fmt.Errorf("storing snapshot: %w", err)
	}
	utils.LogInfo(fmt.Sprintf("snapshot stored: id=%s instance=%d name=%q trigger=%s size=%d encrypted=%v by=%s",
		id, instanceID, name, trigger, len(raw), encrypted, createdBy))
	return s.GetSnapshot(id)
}

// scanSnapshotMeta scans the snapshotMetaColumns column set.
func scanSnapshotMeta(scan func(dest ...interface{}) error, withRestoreResponse bool) (*models.InstanceSnapshot, error) {
	var snap models.InstanceSnapshot
	var desc, lastResult, lastResponse sql.NullString
	var lastRestoredAt sql.NullTime
	dest := []interface{}{
		&snap.ID, &snap.InstanceID, &snap.Name, &desc, &snap.TriggerType,
		&snap.SchemaVersion, &snap.GatewayVersion, &snap.SizeBytes, &snap.Checksum,
		&snap.StoredChecksum, &snap.Encrypted, &snap.Pinned, &snap.ChecksumOK,
		&snap.CreatedBy, &snap.CreatedAt, &snap.RestoreCount, &lastRestoredAt, &lastResult,
	}
	if withRestoreResponse {
		dest = append(dest, &lastResponse)
	}
	if err := scan(dest...); err != nil {
		return nil, err
	}
	snap.Description = desc.String
	if lastRestoredAt.Valid {
		t := lastRestoredAt.Time
		snap.LastRestoredAt = &t
	}
	if lastResult.Valid {
		v := lastResult.String
		snap.LastRestoreResult = &v
	}
	if lastResponse.Valid {
		v := lastResponse.String
		snap.LastRestoreResponse = &v
	}
	return &snap, nil
}

// ListSnapshots returns metadata (never blobs) for one instance, newest
// first.
func (s *SnapshotService) ListSnapshots(instanceID, page, limit int) (*models.PaginatedSnapshotsResponse, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var total int
	if err := s.DB.QueryRow(config.SelectInstanceSnapshotsCountQuery, instanceID).Scan(&total); err != nil {
		return nil, fmt.Errorf("counting snapshots: %w", err)
	}
	rows, err := s.DB.Query(config.SelectInstanceSnapshotsQuery, instanceID, limit, (page-1)*limit)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	defer rows.Close()
	snaps := []models.InstanceSnapshot{}
	for rows.Next() {
		snap, err := scanSnapshotMeta(rows.Scan, false)
		if err != nil {
			return nil, fmt.Errorf("scanning snapshot row: %w", err)
		}
		snaps = append(snaps, *snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	totalPages := (total + limit - 1) / limit
	return &models.PaginatedSnapshotsResponse{
		Data: snaps,
		Pagination: models.PaginationMeta{
			Page:       page,
			Limit:      limit,
			TotalCount: total,
			TotalPages: totalPages,
			HasNext:    page < totalPages,
			HasPrev:    page > 1,
		},
	}, nil
}

// GetSnapshot returns one snapshot's metadata including the restore audit
// record.
func (s *SnapshotService) GetSnapshot(id string) (*models.InstanceSnapshot, error) {
	row := s.DB.QueryRow(config.SelectSnapshotByIDQuery, id)
	snap, err := scanSnapshotMeta(row.Scan, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSnapshotNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading snapshot: %w", err)
	}
	return snap, nil
}

// GetSnapshotDocument loads, decrypts and decompresses one snapshot's raw
// document, verifying the stored checksum. On integrity failure the row is
// marked checksum_ok=false and ErrSnapshotCorrupted is returned — a
// tampered blob must be rejected before it ever reaches a gateway.
func (s *SnapshotService) GetSnapshotDocument(id string) ([]byte, *models.InstanceSnapshot, error) {
	row := s.DB.QueryRow(config.SelectSnapshotBlobQuery, id)
	var blob []byte
	snap, err := scanSnapshotMeta(func(dest ...interface{}) error {
		return row.Scan(append(dest, &blob)...)
	}, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSnapshotNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("loading snapshot blob: %w", err)
	}
	raw, err := s.OpenBlob(blob, snap.Encrypted)
	if err == nil && RawChecksum(raw) != snap.StoredChecksum {
		err = ErrSnapshotCorrupted
	}
	if err != nil {
		if errors.Is(err, ErrSnapshotCorrupted) {
			s.markChecksum(id, false)
		}
		return nil, nil, err
	}
	if !snap.ChecksumOK {
		// A previously-flagged row that now verifies (e.g. key restored) heals.
		s.markChecksum(id, true)
		snap.ChecksumOK = true
	}
	return raw, snap, nil
}

func (s *SnapshotService) markChecksum(id string, ok bool) {
	if _, err := s.DB.Exec(config.MarkSnapshotChecksumQuery, ok, id); err != nil {
		utils.LogError("Failed to update snapshot checksum_ok: " + err.Error())
	}
	if !ok {
		utils.LogError("snapshot " + id + " failed integrity verification — marked checksum_ok=false")
	}
}

// UpdateSnapshot applies a metadata patch (name/description/pinned).
func (s *SnapshotService) UpdateSnapshot(id string, req models.UpdateSnapshotRequest) (*models.InstanceSnapshot, error) {
	snap, err := s.GetSnapshot(id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		snap.Name = *req.Name
		if len(snap.Name) > 128 {
			snap.Name = snap.Name[:128]
		}
	}
	if req.Description != nil {
		snap.Description = *req.Description
	}
	if req.Pinned != nil {
		snap.Pinned = *req.Pinned
	}
	if _, err := s.DB.Exec(config.UpdateSnapshotMetaQuery, snap.Name, snap.Description, snap.Pinned, id); err != nil {
		return nil, fmt.Errorf("updating snapshot: %w", err)
	}
	return s.GetSnapshot(id)
}

// DeleteSnapshot removes a snapshot row. Pinned snapshots require force.
func (s *SnapshotService) DeleteSnapshot(id string, force bool) error {
	snap, err := s.GetSnapshot(id)
	if err != nil {
		return err
	}
	if snap.Pinned && !force {
		return ErrSnapshotPinned
	}
	if _, err := s.DB.Exec(config.DeleteSnapshotQuery, id); err != nil {
		return fmt.Errorf("deleting snapshot: %w", err)
	}
	utils.LogInfo(fmt.Sprintf("snapshot deleted: id=%s instance=%d name=%q (forced=%v)", id, snap.InstanceID, snap.Name, force))
	return nil
}

// GetSchedule returns the instance's schedule row, or the defaults
// (disabled, 24h, keep 10) when none exists yet.
func (s *SnapshotService) GetSchedule(instanceID int) (*models.InstanceSnapshotSchedule, error) {
	if _, err := s.loxilbService.FetchLoxiLBInstanceByID(instanceID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstanceNotFound, err)
	}
	row := s.DB.QueryRow(config.SelectSnapshotScheduleQuery, instanceID)
	var sched models.InstanceSnapshotSchedule
	var lastRunAt sql.NullTime
	var lastResult sql.NullString
	err := row.Scan(&sched.InstanceID, &sched.Enabled, &sched.IntervalHours, &sched.RetainCount, &lastRunAt, &lastResult)
	if errors.Is(err, sql.ErrNoRows) {
		return &models.InstanceSnapshotSchedule{InstanceID: instanceID, IntervalHours: 24, RetainCount: 10}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading snapshot schedule: %w", err)
	}
	if lastRunAt.Valid {
		t := lastRunAt.Time
		sched.LastRunAt = &t
	}
	if lastResult.Valid {
		v := lastResult.String
		sched.LastRunResult = &v
	}
	return &sched, nil
}

// Restore modes accepted by the gateway (and by RestoreSnapshot).
const (
	RestoreModeDryRun = "dry-run"
	RestoreModeCommit = "commit"
)

// MapGatewayRestoreResult maps the gateway's RestoreResult.result spelling
// onto the last_restore_result DB enum. Empty means the pipeline stopped
// before APPLY (nothing was mutated) — recorded as NULL.
func MapGatewayRestoreResult(result string) *string {
	var v string
	switch result {
	case "ok":
		v = models.RestoreResultOK
	case "rolled-back":
		v = models.RestoreResultRolledBack
	case "ROLLBACK-FAILED":
		v = models.RestoreResultRollbackFailed
	default:
		return nil
	}
	return &v
}

// RestoreSnapshot pushes a stored snapshot to a gateway.
//
// Dry-run (the default) validates and returns the gateway's plan without
// mutating anything, and leaves no OAM-side trace beyond logs. Commit
// first takes an automatic pre_restore snapshot of the TARGET instance —
// if that safety net cannot be taken, the restore is aborted — then calls
// the gateway and records the full response as the audit record.
func (s *SnapshotService) RestoreSnapshot(id string, req models.RestoreSnapshotRequest, actor string) (*models.RestoreOutcome, error) {
	mode := req.Mode
	if mode == "" {
		mode = RestoreModeDryRun
	}
	if mode != RestoreModeDryRun && mode != RestoreModeCommit {
		return nil, fmt.Errorf("%w: mode must be %q or %q", ErrInvalidSnapshotDoc, RestoreModeDryRun, RestoreModeCommit)
	}
	raw, snap, err := s.GetSnapshotDocument(id)
	if err != nil {
		return nil, err
	}
	targetID := snap.InstanceID
	if req.TargetInstanceID != nil {
		targetID = *req.TargetInstanceID
	}
	target, err := s.loxilbService.FetchLoxiLBInstanceByID(targetID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstanceNotFound, err)
	}
	outcome := &models.RestoreOutcome{
		SnapshotID:    id,
		InstanceID:    target.ID,
		Mode:          mode,
		CrossInstance: target.ID != snap.InstanceID,
	}
	if mode == RestoreModeCommit {
		pre, err := s.TakeSnapshot(target.ID, models.TakeSnapshotRequest{
			Name:        fmt.Sprintf("pre-restore-%s", time.Now().UTC().Format("20060102-150405")),
			Description: fmt.Sprintf("automatic safety snapshot before restoring %q (%s)", snap.Name, id),
			TriggerType: models.SnapshotTriggerPreRestore,
		}, actor)
		if err != nil {
			return nil, fmt.Errorf("aborting restore: could not take the pre-restore safety snapshot: %w", err)
		}
		outcome.PreRestoreSnapshotID = pre.ID
	}
	status, body, err := s.gateway.Restore(target, raw, mode)
	if err != nil {
		if mode == RestoreModeCommit {
			s.recordRestore(id, nil, fmt.Sprintf(`{"error":%q}`, err.Error()))
		}
		return nil, err
	}
	outcome.GatewayStatus = status
	if json.Valid(body) {
		outcome.GatewayResponse = json.RawMessage(body)
	} else {
		// Never let a non-JSON gateway body break our own response encoding.
		quoted, _ := json.Marshal(string(body))
		outcome.GatewayResponse = quoted
	}
	if mode == RestoreModeCommit {
		var gwResult struct {
			Result string `json:"result"`
		}
		_ = json.Unmarshal(body, &gwResult)
		s.recordRestore(id, MapGatewayRestoreResult(gwResult.Result), string(outcome.GatewayResponse))
		utils.LogInfo(fmt.Sprintf("snapshot restore committed: snapshot=%s target=%d status=%d result=%q by=%s",
			id, target.ID, status, gwResult.Result, actor))
	} else {
		utils.LogInfo(fmt.Sprintf("snapshot restore dry-run: snapshot=%s target=%d status=%d by=%s", id, target.ID, status, actor))
	}
	return outcome, nil
}

// recordRestore persists the audit record of a commit restore attempt.
func (s *SnapshotService) recordRestore(id string, result *string, response string) {
	var res sql.NullString
	if result != nil {
		res = sql.NullString{String: *result, Valid: true}
	}
	if _, err := s.DB.Exec(config.RecordSnapshotRestoreQuery, res, response, id); err != nil {
		utils.LogError("Failed to record snapshot restore result: " + err.Error())
	}
}

// PutSchedule upserts the instance's schedule row.
func (s *SnapshotService) PutSchedule(instanceID int, req models.SnapshotScheduleRequest) (*models.InstanceSnapshotSchedule, error) {
	if _, err := s.loxilbService.FetchLoxiLBInstanceByID(instanceID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstanceNotFound, err)
	}
	interval := req.IntervalHours
	if interval < 1 {
		interval = 24
	}
	retain := req.RetainCount
	if retain < 1 {
		retain = 10
	}
	if _, err := s.DB.Exec(config.UpsertSnapshotScheduleQuery, instanceID, req.Enabled, interval, retain); err != nil {
		return nil, fmt.Errorf("saving snapshot schedule: %w", err)
	}
	return s.GetSchedule(instanceID)
}

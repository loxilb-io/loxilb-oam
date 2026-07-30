package handlers

// Instance snapshot orchestration handlers.
//
// RBAC: take/upload/restore/delete/patch/schedule-write/download require
// config_write (admin); list and metadata reads are available to every
// authenticated role. Download is write-gated too — the document carries
// IPsec PSKs and certificate private keys (design §6).
//
// Every gateway failure surfaces the gateway's own error text verbatim —
// no OAM-side rewording (lesson from the legacy download-404 UX).

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/gin-gonic/gin"
)

// callerUsername resolves the acting username from the auth middleware's
// claims; empty string when absent (cannot happen behind TokenAuthMiddleware).
func callerUsername(c *gin.Context) string {
	if v, ok := c.Get("username"); ok {
		if claims, ok := v.(*utils.Claims); ok {
			return claims.Username
		}
	}
	return ""
}

// writeSnapshotError maps service-layer errors onto HTTP responses.
// *services.GatewayError passes the gateway's status and body through.
func writeSnapshotError(c *gin.Context, err error) {
	var gwErr *services.GatewayError
	switch {
	case errors.As(err, &gwErr):
		if gwErr.StatusCode == 0 {
			// Gateway unreachable — connection error verbatim.
			c.JSON(http.StatusBadGateway, gin.H{"error": gwErr.Body})
			return
		}
		// The gateway answered with an error; relay its status and body.
		body := gwErr.Body
		if strings.HasPrefix(strings.TrimSpace(body), "{") {
			c.Data(gwErr.StatusCode, "application/json", []byte(body))
		} else {
			c.JSON(gwErr.StatusCode, gin.H{"error": body})
		}
	case errors.Is(err, services.ErrSnapshotNotFound), errors.Is(err, services.ErrInstanceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrSnapshotPinned):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrSnapshotTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrSnapshotCorrupted):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrInvalidSnapshotDoc):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		utils.LogError("snapshot operation failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func instanceIDParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid LoxiLB instance ID"})
		return 0, false
	}
	return id, true
}

// TakeSnapshot handles POST /oam/instances/:id/snapshots.
// @Summary Take an instance config snapshot now
// @Description Calls the gateway's GET /config/snapshot on the managed instance and stores the document (gzip, AES-256-GCM at rest when SNAPSHOT_ENC_KEY is set) in the OAM database. Returns metadata only, never the blob.
// @Tags snapshots
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param request body models.TakeSnapshotRequest false "Snapshot name/description/trigger"
// @Success 201 {object} models.InstanceSnapshot
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 413 {object} models.ErrorResponse
// @Failure 502 {object} models.ErrorResponse "Gateway unreachable (connection error passed through verbatim)"
// @Security BearerAuth
// @Router /oam/instances/{id}/snapshots [post]
func (h *Handler) TakeSnapshot(c *gin.Context) {
	id, ok := instanceIDParam(c)
	if !ok {
		return
	}
	var req models.TakeSnapshotRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
			return
		}
	}
	snap, err := h.snapshotService.TakeSnapshot(id, req, callerUsername(c))
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusCreated, snap)
}

// ListSnapshots handles GET /oam/instances/:id/snapshots.
// @Summary List snapshots of an instance
// @Description Returns snapshot metadata (never blobs) for one instance, newest first, paginated.
// @Tags snapshots
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param page query int false "Page number (default 1)"
// @Param limit query int false "Page size (default 20, max 100)"
// @Success 200 {object} models.PaginatedSnapshotsResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/instances/{id}/snapshots [get]
func (h *Handler) ListSnapshots(c *gin.Context) {
	id, ok := instanceIDParam(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	resp, err := h.snapshotService.ListSnapshots(id, page, limit)
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetSnapshot handles GET /oam/snapshots/:sid.
// @Summary Get one snapshot's metadata
// @Description Returns snapshot metadata including restore history and the full gateway response of the last restore (the audit record).
// @Tags snapshots
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param sid path string true "Snapshot ID (UUID)"
// @Success 200 {object} models.InstanceSnapshot
// @Failure 401 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/snapshots/{sid} [get]
func (h *Handler) GetSnapshot(c *gin.Context) {
	snap, err := h.snapshotService.GetSnapshot(c.Param("sid"))
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, snap)
}

// DownloadSnapshot handles GET /oam/snapshots/:sid/download.
// @Summary Download a snapshot document
// @Description Streams the decrypted, decompressed snapshot JSON. The document contains IPsec PSKs and certificate private keys, so this is write-gated and audit-logged. X-Snapshot-Checksum carries the gateway's document checksum; X-Content-Checksum is sha256 over the exact bytes served.
// @Tags snapshots
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param sid path string true "Snapshot ID (UUID)"
// @Success 200 {string} string "snapshot document JSON"
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 422 {object} models.ErrorResponse "Stored blob failed integrity verification"
// @Security BearerAuth
// @Router /oam/snapshots/{sid}/download [get]
func (h *Handler) DownloadSnapshot(c *gin.Context) {
	sid := c.Param("sid")
	raw, snap, err := h.snapshotService.GetSnapshotDocument(sid)
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	instanceName := fmt.Sprintf("instance-%d", snap.InstanceID)
	if inst, err := h.loxilbService.FetchLoxiLBInstanceByID(snap.InstanceID); err == nil {
		instanceName = inst.Name
	}
	filename := fmt.Sprintf("%s-%s-%s.json", instanceName, snap.Name, snap.CreatedAt.UTC().Format("20060102-150405"))
	utils.LogInfo(fmt.Sprintf("snapshot downloaded: id=%s instance=%d by=%s", sid, snap.InstanceID, callerUsername(c)))
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("X-Snapshot-Checksum", snap.Checksum)
	c.Header("X-Content-Checksum", snap.StoredChecksum)
	c.Data(http.StatusOK, "application/json", raw)
}

// UploadSnapshot handles POST /oam/instances/:id/snapshots/upload.
// @Summary Re-import an off-box snapshot archive
// @Description Accepts a previously downloaded snapshot document (multipart field "file"). Only the envelope (schema_version, gateway_version, checksum) is parsed — deep validation stays the gateway's job at restore time.
// @Tags snapshots
// @Accept multipart/form-data
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param file formData file true "Snapshot document JSON"
// @Param name formData string false "Snapshot name"
// @Param description formData string false "Description"
// @Success 201 {object} models.InstanceSnapshot
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 413 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/instances/{id}/snapshots/upload [post]
func (h *Handler) UploadSnapshot(c *gin.Context) {
	id, ok := instanceIDParam(c)
	if !ok {
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart field \"file\" is required"})
		return
	}
	if file.Size > services.MaxSnapshotBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": services.ErrSnapshotTooLarge.Error()})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reading upload: " + err.Error()})
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, services.MaxSnapshotBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reading upload: " + err.Error()})
		return
	}
	snap, err := h.snapshotService.ImportSnapshot(id, c.PostForm("name"), c.PostForm("description"), raw, callerUsername(c))
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusCreated, snap)
}

// RestoreSnapshot handles POST /oam/snapshots/:sid/restore.
// @Summary Restore a stored snapshot to a gateway
// @Description Default mode is dry-run: the gateway validates and returns its plan without mutating anything. Commit first takes an automatic pre_restore safety snapshot of the target, then applies. The gateway's response is returned verbatim in gateway_response. Cross-instance restore is allowed and flagged with cross_instance=true.
// @Tags snapshots
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param sid path string true "Snapshot ID (UUID)"
// @Param request body models.RestoreSnapshotRequest false "mode: dry-run (default) | commit; optional target_instance_id"
// @Success 200 {object} models.RestoreOutcome
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 422 {object} models.ErrorResponse "Stored blob failed integrity verification (never sent to the gateway)"
// @Failure 502 {object} models.ErrorResponse "Gateway unreachable (connection error passed through verbatim)"
// @Security BearerAuth
// @Router /oam/snapshots/{sid}/restore [post]
func (h *Handler) RestoreSnapshot(c *gin.Context) {
	var req models.RestoreSnapshotRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
			return
		}
	}
	outcome, err := h.snapshotService.RestoreSnapshot(c.Param("sid"), req, callerUsername(c))
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, outcome)
}

// UpdateSnapshot handles PATCH /oam/snapshots/:sid.
// @Summary Update snapshot metadata
// @Description Updates name, description and/or pinned. Pinned snapshots are exempt from retention.
// @Tags snapshots
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param sid path string true "Snapshot ID (UUID)"
// @Param request body models.UpdateSnapshotRequest true "Fields to update"
// @Success 200 {object} models.InstanceSnapshot
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/snapshots/{sid} [patch]
func (h *Handler) UpdateSnapshot(c *gin.Context) {
	var req models.UpdateSnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	snap, err := h.snapshotService.UpdateSnapshot(c.Param("sid"), req)
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, snap)
}

// DeleteSnapshot handles DELETE /oam/snapshots/:sid.
// @Summary Delete a snapshot
// @Description Deletes a stored snapshot. Pinned snapshots require force=true.
// @Tags snapshots
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param sid path string true "Snapshot ID (UUID)"
// @Param force query bool false "Required to delete a pinned snapshot"
// @Success 200 {object} models.MessageResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse "Snapshot is pinned and force was not set"
// @Security BearerAuth
// @Router /oam/snapshots/{sid} [delete]
func (h *Handler) DeleteSnapshot(c *gin.Context) {
	force := c.Query("force") == "true"
	if err := h.snapshotService.DeleteSnapshot(c.Param("sid"), force); err != nil {
		writeSnapshotError(c, err)
		return
	}
	utils.LogInfo(fmt.Sprintf("snapshot deleted via API: id=%s by=%s (forced=%v)", c.Param("sid"), callerUsername(c), force))
	c.JSON(http.StatusOK, models.MessageResponse{Message: "Snapshot deleted"})
}

// GetSnapshotSchedule handles GET /oam/instances/:id/snapshot-schedule.
// @Summary Read an instance's snapshot schedule
// @Description Returns the scheduled-snapshot/retention settings; defaults (disabled, every 24h, keep 10) when never configured.
// @Tags snapshots
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Success 200 {object} models.InstanceSnapshotSchedule
// @Failure 401 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/instances/{id}/snapshot-schedule [get]
func (h *Handler) GetSnapshotSchedule(c *gin.Context) {
	id, ok := instanceIDParam(c)
	if !ok {
		return
	}
	sched, err := h.snapshotService.GetSchedule(id)
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, sched)
}

// PutSnapshotSchedule handles PUT /oam/instances/:id/snapshot-schedule.
// @Summary Update an instance's snapshot schedule
// @Description Enables/disables scheduled snapshots and sets interval and per-instance retention (keep-N unpinned; pre_upgrade and pinned snapshots are exempt).
// @Tags snapshots
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param request body models.SnapshotScheduleRequest true "Schedule settings"
// @Success 200 {object} models.InstanceSnapshotSchedule
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/instances/{id}/snapshot-schedule [put]
func (h *Handler) PutSnapshotSchedule(c *gin.Context) {
	id, ok := instanceIDParam(c)
	if !ok {
		return
	}
	var req models.SnapshotScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	sched, err := h.snapshotService.PutSchedule(id, req)
	if err != nil {
		writeSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, sched)
}

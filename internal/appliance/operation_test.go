package appliance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanRequestValidate(t *testing.T) {
	valid := []PlanRequest{
		{Type: OperationBackup},
		{Type: OperationBackup, Note: "before the upgrade"},
		{Type: OperationRestore, ArchiveRef: "backup-2026-10-01"},
		{Type: OperationUpdate, TargetReleaseRef: "v1.2.3"},
		{Type: OperationRollback},
		{Type: OperationReset},
	}
	for _, r := range valid {
		assert.NoError(t, r.Validate(), "%+v", r)
	}

	invalid := map[string]PlanRequest{
		"no type":                      {},
		"unknown type":                 {Type: "reboot"},
		"diagnostics is not an op":     {Type: "diagnostics"},
		"restore without archive":      {Type: OperationRestore},
		"backup with archive":          {Type: OperationBackup, ArchiveRef: "x"},
		"update without release":       {Type: OperationUpdate},
		"rollback with release":        {Type: OperationRollback, TargetReleaseRef: "x"},
		"restore with release as well": {Type: OperationRestore, ArchiveRef: "a", TargetReleaseRef: "r"},
		"oversized reference":          {Type: OperationRestore, ArchiveRef: strings.Repeat("a", maxRefLength+1)},
		"oversized note":               {Type: OperationBackup, Note: strings.Repeat("n", maxNoteLength+1)},
	}
	for name, r := range invalid {
		err := r.Validate()
		assert.True(t, errors.Is(err, ErrInvalidRequest), "%s: got %v", name, err)
	}
}

// The hash is the idempotency identity: equal exactly when the same thing is
// asked for, and not fooled by moving text between fields.
func TestPlanRequestHash(t *testing.T) {
	base := PlanRequest{SchemaVersion: SchemaVersion, Type: OperationRestore, ArchiveRef: "a", Note: "n"}
	same := base
	same.SchemaVersion = "something-else"
	assert.Equal(t, base.Hash(), same.Hash(), "schema_version is not part of what is asked")

	for name, other := range map[string]PlanRequest{
		"type":    {Type: OperationBackup, Note: "n"},
		"archive": {Type: OperationRestore, ArchiveRef: "b", Note: "n"},
		"note":    {Type: OperationRestore, ArchiveRef: "a", Note: "m"},
		"shifted": {Type: OperationRestore, ArchiveRef: "", TargetReleaseRef: "a", Note: "n"},
	} {
		assert.NotEqual(t, base.Hash(), other.Hash(), name)
	}
}

func TestValidIdempotencyKey(t *testing.T) {
	assert.True(t, ValidIdempotencyKey("0123456789abcdef"))
	assert.True(t, ValidIdempotencyKey(strings.Repeat("k", maxIdempotencyKeyLen)))
	for _, bad := range []string{"", "short", strings.Repeat("k", maxIdempotencyKeyLen+1), "has space 0123456789", "tab\t0123456789abcdef", "nonascii-é-0123456789"} {
		assert.False(t, ValidIdempotencyKey(bad), "%q", bad)
	}
}

func TestOperationIDIsOrderedUUIDv7(t *testing.T) {
	at := time.UnixMilli(1_800_000_000_000)
	first, err := newOperationID(at)
	require.NoError(t, err)
	later, err := newOperationID(at.Add(time.Millisecond))
	require.NoError(t, err)
	again, err := newOperationID(at)
	require.NoError(t, err)

	for _, id := range []string{first, later, again} {
		assert.True(t, ValidOperationID(id), id)
		assert.Equal(t, byte('7'), id[14], "version nibble")
		assert.Contains(t, "89ab", string(id[19]), "variant nibble")
	}
	assert.Less(t, first, later, "IDs sort by creation time")
	assert.NotEqual(t, first, again, "same millisecond, still unique")

	for _, bad := range []string{"", "not-a-uuid", strings.ToUpper(first), first[:35], first + "0", strings.Replace(first, "-", "_", 1)} {
		assert.False(t, ValidOperationID(bad), "%q", bad)
	}
}

func TestOperationRedaction(t *testing.T) {
	op := Operation{ID: "x", Type: OperationRestore, PlanHash: "h", Note: "n", Plan: &Plan{ArchiveDigest: "d", AffectedResources: []string{"a"}}}
	red := op.redacted()
	assert.True(t, red.Redacted)
	assert.Empty(t, red.PlanHash)
	assert.Empty(t, red.Note)
	assert.Nil(t, red.Plan)
	assert.NotNil(t, op.Plan, "the original is untouched")
	assert.Equal(t, "x", red.ID)
}

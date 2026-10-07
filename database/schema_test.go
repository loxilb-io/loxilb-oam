package database

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// baselineSHA256 pins init/00-init-complete.sql.
//
// If this test fails you edited the baseline. Do not update the hash: revert
// the edit and put the change in a new file under migrations/postgres.
// Databases already at version 1 recorded this checksum, and a server built
// from an edited baseline refuses to start against every one of them.
const baselineSHA256 = "a2a977352ae58c4bb0f745ab9cf45059c90440b512ade42fafb678d0db579590"

func TestBaselineIsFrozen(t *testing.T) {
	sum := sha256.Sum256([]byte(BaselineSQL))
	if got := hex.EncodeToString(sum[:]); got != baselineSHA256 {
		t.Fatalf("database/init/00-init-complete.sql changed (sha256 %s).\n"+
			"The baseline is frozen: revert it and add a migration under database/migrations/postgres instead.", got)
	}
}

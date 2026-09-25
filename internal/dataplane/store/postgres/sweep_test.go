package postgres

import (
	"context"
	"testing"
	"time"
)

// TestUndatedStorageIsJudgedFresh: a storage state the server did not date
// is held back by the grace period, never condemned by it. The zero time
// is year 1, which every horizon is after, so without the explicit case a
// missing Initiated would read as ancient -- and a live in-progress upload
// on a server that omits the date (SeaweedFS 4.47 did) would be aborted.
//
// THE MUTANT: drop the IsZero branch. The zero stamp is then compared to
// the horizon, found ancient, and reported as not fresh.
func TestUndatedStorageIsJudgedFresh(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	s := &Store{now: func() time.Time { return now }}
	horizon := now.Add(-time.Hour)

	if !s.tooFresh(context.Background(), "org/aa/bb/digest", time.Time{}, horizon) {
		t.Fatal("an undated storage state was judged old; the sweep would condemn work whose age nobody measured")
	}
	// The controls: a dated stamp is still judged by the horizon alone.
	if s.tooFresh(context.Background(), "k", horizon.Add(-time.Minute), horizon) {
		t.Fatal("a stamp before the horizon was judged fresh")
	}
	if !s.tooFresh(context.Background(), "k", horizon.Add(time.Minute), horizon) {
		t.Fatal("a stamp after the horizon was judged old")
	}
}

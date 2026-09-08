package search

import (
	"errors"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestCompletedBuildProgressPreservesUsableIndexOnScanFailure(t *testing.T) {
	lastDone := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.Local)
	lastAttempt := lastDone.Add(30 * time.Minute)
	previous := &model.IndexProgress{
		ObjCount:        42,
		LastDoneTime:    &lastDone,
		LastAttemptTime: &lastAttempt,
	}
	now := lastAttempt.Add(time.Hour)
	progress := completedBuildProgress(previous, 0, 7, true, now, errors.New("storage unavailable"))

	if progress.ObjCount != previous.ObjCount {
		t.Fatalf("ObjCount = %d, want preserved count %d", progress.ObjCount, previous.ObjCount)
	}
	if progress.ScannedCount != 7 {
		t.Fatalf("ScannedCount = %d, want 7", progress.ScannedCount)
	}
	if progress.LastDoneTime == nil || !progress.LastDoneTime.Equal(lastDone) {
		t.Fatalf("LastDoneTime = %v, want %v", progress.LastDoneTime, lastDone)
	}
	if progress.LastAttemptTime == nil || !progress.LastAttemptTime.Equal(now) {
		t.Fatalf("LastAttemptTime = %v, want %v", progress.LastAttemptTime, now)
	}
	if progress.Error != "storage unavailable" {
		t.Fatalf("Error = %q, want storage unavailable", progress.Error)
	}
}

func TestCompletedBuildProgressReportsPartialReplacement(t *testing.T) {
	lastDone := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.Local)
	previous := &model.IndexProgress{ObjCount: 42, LastDoneTime: &lastDone}
	now := lastDone.Add(time.Hour)
	progress := completedBuildProgress(previous, 3, 20, false, now, errors.New("batch failed"))

	if progress.ObjCount != 3 {
		t.Fatalf("ObjCount = %d, want partial replacement count 3", progress.ObjCount)
	}
	if progress.LastDoneTime == nil || !progress.LastDoneTime.Equal(lastDone) {
		t.Fatalf("LastDoneTime = %v, want previous successful time %v", progress.LastDoneTime, lastDone)
	}
}

func TestNormalizeIndexProgressMigratesLegacyFailureTime(t *testing.T) {
	legacyFailureTime := time.Date(2026, time.September, 8, 14, 24, 31, 0, time.Local)
	progress := &model.IndexProgress{
		LastDoneTime: &legacyFailureTime,
		Error:        "legacy failure",
	}
	normalizeIndexProgress(progress)
	if progress.LastDoneTime != nil {
		t.Fatalf("LastDoneTime = %v, want nil for a legacy failed build", progress.LastDoneTime)
	}
	if progress.LastAttemptTime == nil || !progress.LastAttemptTime.Equal(legacyFailureTime) {
		t.Fatalf("LastAttemptTime = %v, want %v", progress.LastAttemptTime, legacyFailureTime)
	}
}

func TestValidateIndexRequestRateLimit(t *testing.T) {
	for _, value := range []string{"0", "0.5", "10"} {
		if err := validateIndexRequestRateLimit(value); err != nil {
			t.Errorf("validateIndexRequestRateLimit(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"", "-1", "NaN", "+Inf", "invalid"} {
		if err := validateIndexRequestRateLimit(value); err == nil {
			t.Errorf("validateIndexRequestRateLimit(%q) error = nil", value)
		}
	}
}

func TestLockUpdateSerializesSameParent(t *testing.T) {
	unlockFirst := lockUpdate("/same-parent")
	secondStarted := make(chan struct{})
	secondAcquired := make(chan struct{})
	secondReleased := make(chan struct{})
	go func() {
		close(secondStarted)
		unlockSecond := lockUpdate("/same-parent")
		close(secondAcquired)
		unlockSecond()
		close(secondReleased)
	}()
	<-secondStarted

	select {
	case <-secondAcquired:
		t.Fatal("second update acquired the same parent lock")
	case <-time.After(20 * time.Millisecond):
	}

	unlockFirst()
	select {
	case <-secondReleased:
	case <-time.After(time.Second):
		t.Fatal("second update did not acquire the released parent lock")
	}

	updateLocksMu.Lock()
	defer updateLocksMu.Unlock()
	if len(updateLocks) != 0 {
		t.Fatalf("update locks were not cleaned up: %d", len(updateLocks))
	}
}

func TestLockUpdateAllowsDifferentParents(t *testing.T) {
	unlockFirst := lockUpdate("/first-parent")
	defer unlockFirst()

	secondAcquired := make(chan struct{})
	go func() {
		unlockSecond := lockUpdate("/second-parent")
		unlockSecond()
		close(secondAcquired)
	}()

	select {
	case <-secondAcquired:
	case <-time.After(time.Second):
		t.Fatal("update for a different parent was blocked")
	}
}

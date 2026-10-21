package task

import (
	"testing"
	"time"
)

func TestShouldPurgeTrashIsThrottled(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	if !shouldPurgeTrash(root, now) {
		t.Fatal("first purge should run")
	}
	if shouldPurgeTrash(root, now.Add(30*time.Minute)) {
		t.Fatal("second purge within an hour should be skipped")
	}
	if !shouldPurgeTrash(root, now.Add(2*time.Hour)) {
		t.Fatal("purge after interval should run")
	}
}

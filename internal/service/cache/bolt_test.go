package cache

import (
	"testing"
)

func TestBoltBasic(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer Close()

	k, v := "user/1/cookie", []byte("abc123xyz")
	if err := Set(db, k, v); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := Get(db, k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(v) {
		t.Fatalf("want %q got %q", v, got)
	}
	// 覆盖
	v2 := []byte("newvalue")
	_ = Set(db, k, v2)
	got2, _ := Get(db, k)
	if string(got2) != "newvalue" {
		t.Fatalf("overwrite fail: %s", got2)
	}
	// 不存在的 key
	miss, err := Get(db, "nope")
	if err != nil || miss != nil {
		t.Fatalf("miss got %v err %v", miss, err)
	}
	// Exists
	ok, _ := Exists(db, k)
	if !ok {
		t.Fatalf("Exists false")
	}
	// Delete
	if err := Delete(db, k); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok2, _ := Exists(db, k)
	if ok2 {
		t.Fatalf("still exists after delete")
	}
}

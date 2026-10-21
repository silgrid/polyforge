package core

import (
	"strings"
	"testing"
)

// TestMemoriesWorkItemIndex: the memories(work_item_id) index exists on a
// fresh database and is created by Open on one that predates it.
func TestMemoriesWorkItemIndex(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	has := func() bool {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index'
			AND name='idx_mem_work_item' AND tbl_name='memories'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !has() {
		t.Fatal("idx_mem_work_item missing on a fresh database")
	}
	if _, err := db.Exec(`DROP INDEX idx_mem_work_item`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !has() {
		t.Fatal("Open did not create idx_mem_work_item on an existing database")
	}
	var plan string
	if err := db.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM memories WHERE work_item_id = 'wi_x'`).Scan(new(int), new(int), new(int), &plan); err != nil {
		t.Fatal(err)
	}
	if want := "idx_mem_work_item"; !strings.Contains(plan, want) {
		t.Fatalf("query plan %q does not use %s", plan, want)
	}
}

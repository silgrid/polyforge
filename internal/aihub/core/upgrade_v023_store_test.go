package core_test

// carries: internal/aihub/core/store.go, internal/aihub/memory/store.go, internal/aihub/memory/browse.go, deploy/sql/v0.2.3/upgrade.sql, internal/aihub/core/upgrade_v023_store_test.go

// upgrade_v023_store_test.go — polyforge#107 AC4, review r1#1: the leg
// the hand-copied SELECT could not carry. The CURRENT code — the real
// memory.Store over core.Open — must FAIL on the un-migrated v0.2.2
// shape (no startup column migration is the contract; the window SQL is
// mandatory before the new server starts) and succeed on the migrated
// one, where the backfilled step row reads back through the store with
// its writer, the details parsed from its idempotency key and the
// owner's display name. This file is the EXTERNAL core_test package
// because the memory store imports core — the internal test package
// cannot import it back; the fixture helpers come through export_test.go.

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/memory"
)

func TestW107UpgradeStore(t *testing.T) {
	dataDir, raw := core.W107V022ShapeDB(t)
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("open the un-migrated v0.2.2 shape: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := memory.NewStore(db, t.TempDir())

	// The un-migrated v0.2.2 shape: the store's reads name the writer
	// columns and its write inserts them, so every memory path fails —
	// the migration is mandatory, never auto-run.
	if _, err := store.Get("mem_w107_step"); err == nil {
		t.Fatal("Get succeeded on an un-migrated v0.2.2-shape database; the v0.2.3 SQL is mandatory before the new server starts")
	}
	if _, _, err := store.Browse(nil, "", "", "", "", nil, 20, nil); err == nil {
		t.Fatal("Browse succeeded on an un-migrated v0.2.2-shape database")
	}
	kind := "step"
	if _, err := store.Create(memory.CreateRequest{
		Project: "legacy", Type: "fact.note", Visibility: "project",
		Content: "w107 store create on the un-migrated shape", WriterKind: &kind,
	}, "u-old"); err == nil {
		t.Fatal("Create succeeded on an un-migrated v0.2.2-shape database")
	}

	// The upgrade, on the same database: the same calls work, and the
	// backfilled step row reads back through the store with its writer,
	// its key-parsed details and the owner's display name joined live.
	core.ApplySQLText(t, raw, core.LoadSQLFileOf(t, core.W107SQLVer, "upgrade.sql"))
	entry, err := store.Get("mem_w107_step")
	if err != nil {
		t.Fatalf("Get on the migrated database: %v", err)
	}
	if entry.WriterKind == nil || *entry.WriterKind != "step" {
		t.Fatalf("the backfilled row's WriterKind = %v, want step", entry.WriterKind)
	}
	if string(entry.WriterDetails) != `{"work_item":"wi_olda","step":"build","attempt":2}` {
		t.Fatalf("the backfilled row's WriterDetails = %s, want the wi/step/attempt parsed from the idempotency key", entry.WriterDetails)
	}
	if entry.OwnerDisplayName != "old" {
		t.Fatalf("the backfilled row's OwnerDisplayName = %q, want the seeded owner's display name", entry.OwnerDisplayName)
	}
	items, _, err := store.Browse(nil, "", "", "", "step", nil, 20, nil)
	if err != nil {
		t.Fatalf("Browse on the migrated database: %v", err)
	}
	if len(items) != 1 || items[0].ID != "mem_w107_step" || items[0].OwnerDisplayName != "old" {
		t.Fatalf("browse writer_kind=step = %d item(s), want the one backfilled step row with its display name", len(items))
	}

	// A fresh step write on the migrated shape stores and reads back
	// through the store.
	details := json.RawMessage(`{"work_item":"wi_107","step":"review","attempt":1}`)
	created, err := store.Create(memory.CreateRequest{
		Project: "legacy", Type: "fact.note", Visibility: "project",
		Content:    "w107 store create on the migrated shape",
		WriterKind: &kind, WriterDetails: &details,
	}, "u-old")
	if err != nil {
		t.Fatalf("create on the migrated shape: %v", err)
	}
	if created.WriterKind == nil || *created.WriterKind != "step" || created.OwnerDisplayName != "old" {
		t.Fatalf("a fresh step write reads back as %v / %q, want the step writer and the owner's display name",
			created.WriterKind, created.OwnerDisplayName)
	}
	if back, err := store.Get(created.ID); err != nil || back.WriterKind == nil || *back.WriterKind != "step" {
		t.Fatalf("the fresh step write does not read back: %v (%+v)", err, back)
	}
}

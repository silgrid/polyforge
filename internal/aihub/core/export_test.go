package core

// carries: internal/aihub/core/store.go, internal/aihub/core/upgrade_v023_test.go, internal/aihub/core/export_test.go

// export_test.go — the internal upgrade-test fixture helpers exported to
// the external core_test package (polyforge#107, review r1#1): the AC4
// leg that runs the CURRENT code — the real memory.Store — on the
// un-migrated v0.2.2 shape lives in core_test, because only the
// external test package may import internal/aihub/memory (the memory
// store imports core; an internal test file would cycle). Nothing here
// ships: a _test.go file of the package it tests.

// W107SQLVer is the release directory carrying polyforge#107's window
// SQL, as the internal upgrade tests know it.
const W107SQLVer = w107SQLVer

// W107V022ShapeDB answers the populated v0.2.2-shape database the
// internal upgrade test builds (data dir + raw handle; the handle's
// cleanup is the fixture's).
var W107V022ShapeDB = w107V022ShapeDB

// ApplySQLText executes one SQL text's statements in order, failing the
// test on the first error — the internal helper, shared with core_test.
var ApplySQLText = applySQLText

// LoadSQLFileOf reads one file of deploy/sql/<ver>/ — the SQL deploy.sh
// executes inside the switch window.
var LoadSQLFileOf = loadSQLFileOf

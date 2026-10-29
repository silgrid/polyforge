package memory

// search_bench_test.go: the cost of one FTS candidate page over a
// synthetic corpus shaped like a real library (a few thousand entries,
// multi-KB bodies, mixed types and ages). The weighted page is compared
// against a bm25-only page, the cheapest ordering FTS5 offers: the
// freshness term must stay close to it, since Core's single connection
// serializes every request behind a search.
//
//	GOWORK=off go test -run '^$' -bench FTSPage -benchtime 20x ./internal/aihub/memory/

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

const (
	benchEntries   = 3000
	benchVocab     = 4000
	benchRareEvery = 7 // every 7th entry carries the rare token: ~430 matches
)

// benchCorpus inserts a synthetic corpus of n entries straight into
// memories + memory_fts (ftsPage reads only those) and answers the store
// and an any-mode query of maxAnyTerms corpus words.
func benchCorpus(b testing.TB, n int) (*Store, string) {
	b.Helper()
	db, err := core.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	admin := core.NewAdminManager(db)
	u, err := admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		b.Fatal(err)
	}
	p, err := admin.CreateProject(core.CreateProjectRequest{Name: "alpha"})
	if err != nil {
		b.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	vocab := make([]string, benchVocab)
	for i := range vocab {
		var sb strings.Builder
		for j := 0; j < 5+rng.Intn(6); j++ {
			sb.WriteByte(byte('a' + rng.Intn(26)))
		}
		vocab[i] = sb.String()
	}
	types := []string{"experience.debug", "fact.note", "rule.ops", "doc.design", "doc.review", "methodology.x"}
	now := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		var sb strings.Builder
		for sb.Len() < 2048+rng.Intn(14*1024) {
			sb.WriteString(vocab[rng.Intn(len(vocab))])
			sb.WriteByte(' ')
		}
		if i%benchRareEvery == 0 {
			sb.WriteString("gowork")
		}
		id := fmt.Sprintf("mem_bench%06d", i)
		ts := now.Add(-time.Duration(rng.Intn(2*365*24)) * time.Hour).Format(time.RFC3339Nano)
		if _, err := tx.Exec(`INSERT INTO memories(id, project_id, owner_user_id, type, visibility,
			authority_path, content_sha, content_len, created_at, updated_at, content)
			VALUES (?1, ?2, ?3, ?4, 'project', '', 'sha', ?5, ?6, ?6, ?7)`,
			id, p.ID, u.ID, types[i%len(types)], sb.Len(), ts, sb.String()); err != nil {
			b.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO memory_fts(mem_id, body) VALUES (?1, ?2)`, id, sb.String()); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return NewStore(db, b.TempDir()), ftsJoin(vocab[:maxAnyTerms], " OR ")
}

// bm25Page is the unweighted reference: rank order straight from FTS5.
func bm25Page(s *Store, match string, limit int) (int, error) {
	rows, err := s.db.Query(`SELECT f.mem_id, f.body FROM memories m
		JOIN memory_fts f ON f.mem_id = m.id
		WHERE memory_fts MATCH ?1 AND m.status='active'
		ORDER BY rank LIMIT ?2`, match, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var c candRow
		if err := rows.Scan(&c.id, &c.body); err != nil {
			return 0, err
		}
		n++
	}
	return n, rows.Err()
}

func BenchmarkFTSPage(b *testing.B) {
	s, anyMatch := benchCorpus(b, benchEntries)
	now := time.Now()
	for _, q := range []struct{ name, match string }{
		{"rare-token", ftsQuery([]string{"gowork"})},
		{"any-32-terms", anyMatch},
	} {
		b.Run(q.name+"/bm25", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if n, err := bm25Page(s, q.match, 100); err != nil || n != 100 {
					b.Fatalf("bm25 page: %d rows, %v", n, err)
				}
			}
		})
		b.Run(q.name+"/weighted", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if c, err := s.ftsPage(q.match, nil, now, 100, 0); err != nil || len(c) != 100 {
					b.Fatalf("weighted page: %d rows, %v", len(c), err)
				}
			}
		})
	}
}

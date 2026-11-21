package storage

import (
	"fmt"
	"testing"
)

// sqlStoreOf exposes the concrete store to tests in this package.
func sqlStoreOf(t testing.TB, st Storage) *sqlStore {
	t.Helper()
	s, ok := st.(*sqlStore)
	if !ok {
		t.Fatalf("not a sqlStore: %T", st)
	}
	return s
}

// bulkMessages inserts n messages in one transaction, which is far faster than
// n AppendMessage calls. The oldest message (seq 1) says "needle"; the rest do
// not. CreatedAt equals seq.
func (s *sqlStore) bulkMessages(t testing.TB, convID string, n int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO messages (` + msgCols + `) VALUES (?, ?, ?, 'u', ?, '', ?, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		body := fmt.Sprintf("filler message %d", i)
		if i == 1 {
			body = "the needle"
		}
		if _, err := stmt.Exec(fmt.Sprint("b", i), convID, i, body, i); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO conversation_seq (conversation_id, next_seq) VALUES (?, ?)`, convID, n+1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

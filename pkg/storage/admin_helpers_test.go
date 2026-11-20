package storage

import (
	"fmt"
	"testing"
)

// newJSONStoreForBench exposes the concrete store to tests in this package.
func newJSONStoreForBench(t *testing.T, st Storage) *jsonStore {
	t.Helper()
	js, ok := st.(*jsonStore)
	if !ok {
		t.Fatalf("not a jsonStore: %T", st)
	}
	return js
}

// bulkMessages fills a conversation in memory only, skipping the per-message
// fsync that makes AppendMessage too slow for tens of thousands of rows. The
// oldest message (seq 1) says "needle"; the rest do not.
func (s *jsonStore) bulkMessages(convID string, n int) {
	_, _ = run(s, s.model, func(m *model) (struct{}, error) {
		list := make([]Message, 0, n)
		for i := 1; i <= n; i++ {
			body := fmt.Sprintf("filler message %d", i)
			if i == 1 {
				body = "the needle"
			}
			list = append(list, Message{ID: fmt.Sprint("b", i), ConversationID: convID, Seq: int64(i), AuthorID: "u", Body: body, CreatedAt: int64(i)})
		}
		m.messages[convID] = list
		m.nextSeq[convID] = int64(n + 1)
		return struct{}{}, nil
	})
}

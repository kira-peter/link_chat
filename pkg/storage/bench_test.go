package storage

import (
	"fmt"
	"testing"
)

func benchStore(b *testing.B, preload int) Storage {
	b.Helper()
	st, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	if preload > 0 {
		sqlStoreOf(b, st).bulkMessages(b, "c1", preload)
	}
	return st
}

// BenchmarkAppendMessage measures the cost of one durable send at different
// conversation sizes. Each send is one row insert plus a seq bump, so the cost
// should stay flat as the conversation grows.
func BenchmarkAppendMessage(b *testing.B) {
	for _, preload := range []int{0, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("existing=%d", preload), func(b *testing.B) {
			st := benchStore(b, preload)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := st.AppendMessage(Message{
					ID:             fmt.Sprintf("m-%d", i),
					ConversationID: "c1",
					AuthorID:       "u1",
					Body:           "a message of a realistic length, around eighty characters long here",
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMessagesBefore measures paging latency, which is what a client waits
// on when opening a conversation or scrolling up.
func BenchmarkMessagesBefore(b *testing.B) {
	st := benchStore(b, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := st.MessagesBefore("c1", 0, 50); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMessageByID measures an ID lookup, which used to scan every
// conversation.
func BenchmarkMessageByID(b *testing.B) {
	st := benchStore(b, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.MessageByID("b50000"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenWith100k measures startup. History stays on disk, so this
// should not grow with the number of messages.
func BenchmarkOpenWith100k(b *testing.B) {
	dir := b.TempDir()
	st, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	sqlStoreOf(b, st).bulkMessages(b, "c1", 100000)
	st.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := Open(dir)
		if err != nil {
			b.Fatal(err)
		}
		s.Close()
	}
}

// BenchmarkSearchKeyword measures one search batch over 100k messages, for a
// long keyword the trigram index serves and a two-character one it cannot.
func BenchmarkSearchKeyword(b *testing.B) {
	st := benchStore(b, 100000)
	for _, kw := range []string{"needle", "ne"} {
		b.Run(kw, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := st.SearchMessages(SearchFilter{Keyword: kw}, SearchCursor{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

package storage

import (
	"fmt"
	"testing"
)

// BenchmarkAppendMessage measures the cost of one durable send at different
// conversation sizes.
//
// JSON files were chosen over SQLite deliberately (design.md decision 4); this
// benchmark is what makes that trade-off quantified rather than assumed. Each
// send is one append to a per-conversation log, so the cost should stay flat as
// the log grows — if it does not, the file organisation is wrong.
func BenchmarkAppendMessage(b *testing.B) {
	for _, preload := range []int{0, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("existing=%d", preload), func(b *testing.B) {
			dir := b.TempDir()
			st, err := OpenJSON(dir)
			if err != nil {
				b.Fatal(err)
			}
			defer st.Close()

			if preload > 0 {
				for i := 0; i < preload; i++ {
					if _, err := st.AppendMessage(Message{
						ID:             fmt.Sprintf("pre-%d", i),
						ConversationID: "c1",
						AuthorID:       "u1",
						Body:           "preloaded message body of typical length",
					}); err != nil {
						b.Fatal(err)
					}
				}
			}

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
	dir := b.TempDir()
	st, err := OpenJSON(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	const total = 100000
	for i := 0; i < total; i++ {
		if _, err := st.AppendMessage(Message{
			ID:             fmt.Sprintf("m-%d", i),
			ConversationID: "c1",
			AuthorID:       "u1",
			Body:           "message body",
		}); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := st.MessagesBefore("c1", 0, 50); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenWith100k measures startup: the whole store is loaded into memory,
// so this is the number that decides how large a data directory is practical.
func BenchmarkOpenWith100k(b *testing.B) {
	dir := b.TempDir()
	st, err := OpenJSON(dir)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100000; i++ {
		if _, err := st.AppendMessage(Message{
			ID:             fmt.Sprintf("m-%d", i),
			ConversationID: "c1",
			AuthorID:       "u1",
			Body:           "message body",
		}); err != nil {
			b.Fatal(err)
		}
	}
	st.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := OpenJSON(dir)
		if err != nil {
			b.Fatal(err)
		}
		s.Close()
	}
}

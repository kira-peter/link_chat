package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// File layout under the data directory:
//
//	<root>/users.json                  all accounts
//	<root>/tokens.json                 all session tokens (hashed)
//	<root>/conversations.json          channel + dm metadata
//	<root>/members.json                channel membership
//	<root>/messages/<conversation>.jsonl  one append-only file per conversation
//
// Splitting messages per conversation is what keeps a send from rewriting the
// whole history (design.md, write amplification). Metadata files are small
// enough that a full atomic rewrite per change is the simpler correct choice.
const (
	usersFile         = "users.json"
	tokensFile        = "tokens.json"
	conversationsFile = "conversations.json"
	membersFile       = "members.json"
	messagesDir       = "messages"
)

// jsonStore is an actor: one goroutine owns the in-memory model, and every
// public method sends it a request and waits for the reply. That gives
// serialized writes, consistent reads, and no lock ordering to reason about
// (design.md decision 4).
type jsonStore struct {
	root string
	// model is owned exclusively by the actor goroutine. Nothing outside
	// loop() may touch it.
	model *model

	reqs      chan func(*model)
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// model is the whole in-memory database. It is only ever touched from the
// actor goroutine.
type model struct {
	users         map[string]User   // by user ID
	usernameIndex map[string]string // username key -> user ID
	tokens        map[string]Token  // by token hash
	conversations map[string]Conversation
	channelIndex  map[string]string            // channel name key -> conversation ID
	members       map[string]map[string]Member // conversation ID -> user ID -> member
	messages      map[string][]Message         // conversation ID -> ordered by seq
	nextSeq       map[string]int64
}

// OpenJSON opens (or creates) a JSON-file store rooted at dir.
func OpenJSON(dir string) (Storage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create data dir: %w", err)
	}

	m := &model{
		users:         map[string]User{},
		usernameIndex: map[string]string{},
		tokens:        map[string]Token{},
		conversations: map[string]Conversation{},
		channelIndex:  map[string]string{},
		members:       map[string]map[string]Member{},
		messages:      map[string][]Message{},
		nextSeq:       map[string]int64{},
	}
	if err := loadInto(dir, m); err != nil {
		return nil, err
	}

	s := &jsonStore{
		root:  dir,
		model: m,
		reqs:  make(chan func(*model)),
		done:  make(chan struct{}),
	}
	s.wg.Add(1)
	go s.loop()
	return s, nil
}

func (s *jsonStore) loop() {
	defer s.wg.Done()
	for {
		select {
		case fn := <-s.reqs:
			fn(s.model)
		case <-s.done:
			return
		}
	}
}

// Close stops the actor. Work already queued is not buffered (reqs is
// unbuffered), so every caller that got a reply has already had its write
// applied before Close returns.
func (s *jsonStore) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	s.wg.Wait()
	return nil
}

// loadInto reads every file into m, tolerating a torn final line in a message
// log (spec server-runtime / 数据持久化语义).
func loadInto(dir string, m *model) error {
	if err := readJSONFile(filepath.Join(dir, usersFile), func(b []byte) error {
		var users []User
		if err := json.Unmarshal(b, &users); err != nil {
			return err
		}
		for _, u := range users {
			m.users[u.ID] = u
			m.usernameIndex[u.UsernameKey] = u.ID
		}
		return nil
	}); err != nil {
		return err
	}

	if err := readJSONFile(filepath.Join(dir, tokensFile), func(b []byte) error {
		var tokens []Token
		if err := json.Unmarshal(b, &tokens); err != nil {
			return err
		}
		for _, t := range tokens {
			m.tokens[t.Hash] = t
		}
		return nil
	}); err != nil {
		return err
	}

	if err := readJSONFile(filepath.Join(dir, conversationsFile), func(b []byte) error {
		var convs []Conversation
		if err := json.Unmarshal(b, &convs); err != nil {
			return err
		}
		for _, c := range convs {
			m.conversations[c.ID] = c
			if c.Kind == "channel" {
				m.channelIndex[nameKey(c.Name)] = c.ID
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := readJSONFile(filepath.Join(dir, membersFile), func(b []byte) error {
		var ms []Member
		if err := json.Unmarshal(b, &ms); err != nil {
			return err
		}
		for _, mem := range ms {
			if m.members[mem.ConversationID] == nil {
				m.members[mem.ConversationID] = map[string]Member{}
			}
			m.members[mem.ConversationID][mem.UserID] = mem
		}
		return nil
	}); err != nil {
		return err
	}

	msgDir := filepath.Join(dir, messagesDir)
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: read messages dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(msgDir, e.Name()))
		if err != nil {
			return fmt.Errorf("storage: read %s: %w", e.Name(), err)
		}
		var msgs []Message
		for _, line := range splitJSONLines(b) {
			var mm Message
			if err := json.Unmarshal(line, &mm); err != nil {
				// A torn trailing line is expected after a crash; skip it.
				continue
			}
			msgs = append(msgs, mm)
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].Seq < msgs[j].Seq })
		for _, mm := range msgs {
			m.messages[mm.ConversationID] = append(m.messages[mm.ConversationID], mm)
			if mm.Seq >= m.nextSeq[mm.ConversationID] {
				m.nextSeq[mm.ConversationID] = mm.Seq + 1
			}
		}
	}
	return nil
}

// readJSONFile calls fn with the file contents, treating a missing file as
// empty rather than an error.
func readJSONFile(path string, fn func([]byte) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: read %s: %w", filepath.Base(path), err)
	}
	if len(b) == 0 {
		return nil
	}
	if err := fn(b); err != nil {
		return fmt.Errorf("storage: parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func run[T any](s *jsonStore, m *model, fn func(*model) (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	select {
	case s.reqs <- func(mm *model) {
		v, err := fn(mm)
		ch <- result{v, err}
	}:
	case <-s.done:
		var zero T
		return zero, ErrUnavailable
	}
	r := <-ch
	return r.v, r.err
}

// --- persistence helpers (called from the actor goroutine) ---

func (s *jsonStore) persistUsers(m *model) error {
	list := make([]User, 0, len(m.users))
	for _, u := range m.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return writeJSON(filepath.Join(s.root, usersFile), list)
}

func (s *jsonStore) persistTokens(m *model) error {
	list := make([]Token, 0, len(m.tokens))
	for _, t := range m.tokens {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Hash < list[j].Hash })
	return writeJSON(filepath.Join(s.root, tokensFile), list)
}

func (s *jsonStore) persistConversations(m *model) error {
	list := make([]Conversation, 0, len(m.conversations))
	for _, c := range m.conversations {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return writeJSON(filepath.Join(s.root, conversationsFile), list)
}

func (s *jsonStore) persistMembers(m *model) error {
	var list []Member
	for _, byUser := range m.members {
		for _, mem := range byUser {
			list = append(list, mem)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ConversationID != list[j].ConversationID {
			return list[i].ConversationID < list[j].ConversationID
		}
		return list[i].UserID < list[j].UserID
	})
	return writeJSON(filepath.Join(s.root, membersFile), list)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return wrapErr(writeFileAtomic(path, b))
}

func messagePath(root, conversationID string) string {
	return filepath.Join(root, messagesDir, sanitizeFileName(conversationID)+".jsonl")
}

// sanitizeFileName keeps a conversation ID from escaping the messages directory
// or colliding with a reserved name.
func sanitizeFileName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "unknown"
	}
	return out
}

// nameKey is the case-insensitive key used for username and channel uniqueness.
func nameKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

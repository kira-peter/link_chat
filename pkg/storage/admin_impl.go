package storage

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

// ---------- admin permissions ----------

func (s *jsonStore) SetUserPerms(userID string, perms []string) error {
	return s.updateUser(userID, func(u *User) {
		if len(perms) == 0 {
			u.Perms = nil
			return
		}
		u.Perms = append([]string(nil), perms...)
		sort.Strings(u.Perms)
	})
}

func (s *jsonStore) SetUserSuper(userID string, super bool) error {
	return s.updateUser(userID, func(u *User) { u.Super = super })
}

// updateUser applies fn to one user and persists, rolling back on failure.
func (s *jsonStore) updateUser(userID string, fn func(*User)) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		u, ok := m.users[userID]
		if !ok {
			return struct{}{}, ErrNotFound
		}
		old := u
		fn(&u)
		m.users[userID] = u
		if err := s.persistUsers(m); err != nil {
			m.users[userID] = old
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// ---------- tokens ----------

func (s *jsonStore) DeleteTokensForUserPurpose(userID, purpose string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		removed := map[string]Token{}
		for h, t := range m.tokens {
			if t.UserID == userID && t.EffectivePurpose() == purpose {
				removed[h] = t
				delete(m.tokens, h)
			}
		}
		if len(removed) == 0 {
			return struct{}{}, nil
		}
		if err := s.persistTokens(m); err != nil {
			for h, t := range removed {
				m.tokens[h] = t
			}
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// ---------- sanctions ----------

func (s *jsonStore) AddSanction(sc Sanction) (Sanction, error) {
	return run(s, s.model, func(m *model) (Sanction, error) {
		if sc.ID == "" {
			sc.ID = "s_" + newID()
		}
		if sc.CreatedAt == 0 {
			sc.CreatedAt = Now()
		}
		if _, exists := m.sanctions[sc.ID]; exists {
			return Sanction{}, ErrConflict
		}
		// Supersede whatever of this kind is still in force, in the same step,
		// so two sanctions of one kind are never active together.
		replaced := map[string]Sanction{}
		for _, id := range m.sanctionsByUID[sc.UserID] {
			old := m.sanctions[id]
			if old.Kind == sc.Kind && old.ActiveAt(sc.CreatedAt) {
				replaced[id] = old
				old.SupersededBy = sc.ID
				m.sanctions[id] = old
			}
		}
		m.sanctions[sc.ID] = sc
		m.sanctionsByUID[sc.UserID] = append(m.sanctionsByUID[sc.UserID], sc.ID)
		if err := s.persistSanctions(m); err != nil {
			for id, old := range replaced {
				m.sanctions[id] = old
			}
			delete(m.sanctions, sc.ID)
			ids := m.sanctionsByUID[sc.UserID]
			m.sanctionsByUID[sc.UserID] = ids[:len(ids)-1]
			return Sanction{}, err
		}
		return sc, nil
	})
}

func (s *jsonStore) RevokeSanction(id, by string, at int64) (Sanction, error) {
	return run(s, s.model, func(m *model) (Sanction, error) {
		sc, ok := m.sanctions[id]
		if !ok {
			return Sanction{}, ErrNotFound
		}
		if !sc.ActiveAt(at) {
			return Sanction{}, ErrConflict
		}
		old := sc
		sc.RevokedBy, sc.RevokedAt = by, at
		m.sanctions[id] = sc
		if err := s.persistSanctions(m); err != nil {
			m.sanctions[id] = old
			return Sanction{}, err
		}
		return sc, nil
	})
}

func (s *jsonStore) SanctionsForUser(userID string) ([]Sanction, error) {
	return run(s, s.model, func(m *model) ([]Sanction, error) {
		ids := m.sanctionsByUID[userID]
		out := make([]Sanction, 0, len(ids))
		for i := len(ids) - 1; i >= 0; i-- {
			out = append(out, m.sanctions[ids[i]])
		}
		return out, nil
	})
}

func (s *jsonStore) ActiveSanction(userID, kind string, now int64) (Sanction, bool, error) {
	type res struct {
		sc Sanction
		ok bool
	}
	r, err := run(s, s.model, func(m *model) (res, error) {
		ids := m.sanctionsByUID[userID]
		for i := len(ids) - 1; i >= 0; i-- {
			sc := m.sanctions[ids[i]]
			if sc.Kind == kind && sc.ActiveAt(now) {
				return res{sc, true}, nil
			}
		}
		return res{}, nil
	})
	return r.sc, r.ok, err
}

// ---------- audit ----------

func (s *jsonStore) AppendAudit(e AuditEntry) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		if e.At == 0 {
			e.At = Now()
		}
		line, err := json.Marshal(e)
		if err != nil {
			return struct{}{}, err
		}
		if err := appendLine(filepath.Join(s.root, auditFile), line); err != nil {
			return struct{}{}, wrapErr(err)
		}
		m.audit = append(m.audit, e)
		m.auditOnDisk++
		if len(m.audit) > auditKeep {
			// Drop the oldest; copy so the backing array does not grow forever.
			m.audit = append([]AuditEntry(nil), m.audit[len(m.audit)-auditKeep:]...)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) ListAudit(f AuditFilter) ([]AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// The in-memory tail answers almost every page.
	type res struct {
		out       []AuditEntry
		needsDisk bool
	}
	r, err := run(s, s.model, func(m *model) (res, error) {
		out := filterAudit(m.audit, f, limit)
		// The tail is incomplete only when entries were dropped from memory and
		// the page did not fill from what is there.
		dropped := m.auditOnDisk > len(m.audit)
		return res{out, dropped && len(out) < limit}, nil
	})
	if err != nil || !r.needsDisk {
		return r.out, err
	}
	// Rare path: read the whole file outside the actor, so a deep page does not
	// stall chat traffic. Appends only add at the end, so a concurrent write can
	// at worst leave the newest entry out of this page.
	all, err := readAuditFile(filepath.Join(s.root, auditFile))
	if err != nil {
		return nil, err
	}
	return filterAudit(all, f, limit), nil
}

// filterAudit walks list (oldest first) from the newest end and returns up to
// limit matches, newest first.
func filterAudit(list []AuditEntry, f AuditFilter, limit int) []AuditEntry {
	var out []AuditEntry
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		e := list[i]
		if f.Before > 0 && e.At >= f.Before {
			continue
		}
		if f.ActorID != "" && e.ActorID != f.ActorID {
			continue
		}
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		if f.From > 0 && e.At < f.From {
			continue
		}
		if f.To > 0 && e.At > f.To {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ---------- overview and search ----------

func (s *jsonStore) Counts() (Counts, error) {
	return run(s, s.model, func(m *model) (Counts, error) {
		c := Counts{Users: len(m.users)}
		for _, conv := range m.conversations {
			if conv.Kind == "dm" {
				c.DMs++
			} else {
				c.Channels++
			}
		}
		for _, list := range m.messages {
			c.Messages += int64(len(list))
		}
		return c, nil
	})
}

// SearchMessages scans conversations in ID order and, within each, from the
// newest message down. It stops after SearchMaxScan messages or once the page
// is full and hands back a cursor, so one call holds the actor only briefly
// (design.md decision 6).
func (s *jsonStore) SearchMessages(f SearchFilter, from SearchCursor) (SearchResult, error) {
	limit := f.Limit
	if limit <= 0 || limit > SearchMaxResults {
		limit = SearchMaxResults
	}
	kw := strings.ToLower(strings.TrimSpace(f.Keyword))
	return run(s, s.model, func(m *model) (SearchResult, error) {
		ids := make([]string, 0, len(m.conversations))
		for id := range m.conversations {
			if f.ConversationID == "" || id == f.ConversationID {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)

		var res SearchResult
		scanned := 0
		for ci := from.Conv; ci < len(ids); ci++ {
			list := m.messages[ids[ci]] // ascending by seq
			i := len(list) - 1
			if ci == from.Conv && from.Seq > 0 {
				// Resume at the cursor's seq and go downwards from there.
				i = sort.Search(len(list), func(k int) bool { return list[k].Seq > from.Seq }) - 1
			}
			for ; i >= 0; i-- {
				if scanned >= SearchMaxScan {
					res.Next = &SearchCursor{Conv: ci, Seq: list[i].Seq}
					return res, nil
				}
				scanned++
				msg := list[i]
				if !matchSearch(msg, f, kw) {
					continue
				}
				if len(res.Messages) == limit {
					// A further match exists, so there is a next page; it starts here.
					res.Next = &SearchCursor{Conv: ci, Seq: msg.Seq}
					return res, nil
				}
				res.Messages = append(res.Messages, msg)
			}
		}
		return res, nil
	})
}

// matchSearch reports whether msg satisfies the filter. kw is the lowercased
// keyword.
func matchSearch(msg Message, f SearchFilter, kw string) bool {
	switch {
	case msg.Deleted:
		return false
	case f.AuthorID != "" && msg.AuthorID != f.AuthorID:
		return false
	case f.From > 0 && msg.CreatedAt < f.From:
		return false
	case f.To > 0 && msg.CreatedAt > f.To:
		return false
	case kw != "" && !strings.Contains(strings.ToLower(msg.Body), kw):
		return false
	}
	return true
}

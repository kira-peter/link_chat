package storage

import (
	"cmp"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type SimulationUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	RunID    string `json:"run_id"`
}

type AccountDeletion struct {
	Conversations []Conversation
}

type SimulationBatch struct {
	Users   []User
	Created []User
}

func (s *sqlStore) EnsureSimulationUsers(users []User, runID string) (SimulationBatch, error) {
	var batch SimulationBatch
	err := s.withTx(func(tx *sql.Tx) error {
		for _, u := range users {
			existing, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users WHERE username_key = ?`, nameKey(u.Username)))
			if err == nil {
				marked, err := isSimulationUser(tx, existing.ID)
				if err != nil {
					return err
				}
				if marked && !existing.Super && len(existing.Perms) == 0 {
					batch.Users = append(batch.Users, existing)
				}
				continue
			}
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			if err := insertUser(tx, u); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO simulation_accounts(user_id, run_id) VALUES (?, ?)`, u.ID, runID); err != nil {
				return err
			}
			batch.Users = append(batch.Users, u)
			batch.Created = append(batch.Created, u)
		}
		return nil
	})
	if err != nil {
		return SimulationBatch{}, err
	}
	return batch, nil
}

func isSimulationUser(tx *sql.Tx, id string) (bool, error) {
	var marked bool
	err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM simulation_accounts WHERE user_id = ?)`, id).Scan(&marked)
	return marked, err
}

func (s *sqlStore) SimulationUsers() ([]SimulationUser, error) {
	rows, err := s.db.Query(`SELECT u.id, u.username, a.run_id FROM simulation_accounts a
		JOIN users u ON u.id = a.user_id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []SimulationUser{}
	for rows.Next() {
		var user SimulationUser
		if err := rows.Scan(&user.ID, &user.Username, &user.RunID); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, user)
	}
	slices.SortFunc(out, func(a, b SimulationUser) int {
		prefixA, indexA := simulationNameParts(a.Username)
		prefixB, indexB := simulationNameParts(b.Username)
		return cmp.Or(cmp.Compare(prefixA, prefixB), cmp.Compare(indexA, indexB), cmp.Compare(a.Username, b.Username))
	})
	return out, mapErr(rows.Err())
}

func simulationNameParts(username string) (string, int) {
	name := strings.ToLower(username)
	if i := strings.LastIndexByte(name, '_'); i >= 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil {
			return name[:i], n
		}
	}
	return name, 0
}

// DeleteUser removes the account and its content together. Channels with
// remaining members transfer to the oldest surviving member.
func (s *sqlStore) DeleteUser(userID string, keepComments bool) (AccountDeletion, error) {
	return s.deleteUser(userID, keepComments, false)
}

func (s *sqlStore) DeleteSimulationUser(userID string, keepComments bool) (AccountDeletion, error) {
	return s.deleteUser(userID, keepComments, true)
}

func (s *sqlStore) deleteUser(userID string, keepComments, simulationOnly bool) (AccountDeletion, error) {
	var result AccountDeletion
	err := s.withTx(func(tx *sql.Tx) error {
		if simulationOnly {
			marked, err := isSimulationUser(tx, userID)
			if err != nil {
				return err
			}
			if !marked {
				return ErrNotFound
			}
		}
		user, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, userID))
		if err != nil {
			return err
		}
		if user.Super || len(user.Perms) != 0 {
			return ErrConflict
		}
		if _, err := tx.Exec(`UPDATE conversations AS c SET created_by = COALESCE((
			SELECT m.user_id FROM members m JOIN users u ON u.id = m.user_id
			WHERE m.conversation_id = c.id AND m.user_id <> ?1
			ORDER BY m.joined_at, m.user_id LIMIT 1), '')
			WHERE c.kind = 'channel' AND c.created_by = ?1
			AND EXISTS (SELECT 1 FROM members m JOIN users u ON u.id = m.user_id
				WHERE m.conversation_id = c.id AND m.user_id <> ?1)`, userID); err != nil {
			return fmt.Errorf("transfer channels: %w", err)
		}
		result.Conversations, err = userConversationsToDelete(tx, userID)
		if err != nil {
			return err
		}
		for _, cv := range result.Conversations {
			if err := deleteConversation(tx, cv.ID); err != nil {
				return err
			}
		}

		for _, statement := range []string{
			`DELETE FROM moment_notifications WHERE user_id = ?1 OR actor_id = ?1
				OR moment_id IN (SELECT id FROM moments WHERE author_id = ?1)
				OR comment_id IN (SELECT id FROM moment_comments WHERE author_id = ?1)`,
			`DELETE FROM moment_likes WHERE user_id = ?1 OR moment_id IN (SELECT id FROM moments WHERE author_id = ?1)`,
			`DELETE FROM moment_comments WHERE moment_id IN (SELECT id FROM moments WHERE author_id = ?1)`,
			`DELETE FROM moments WHERE author_id = ?1`,
			`UPDATE moment_comments SET reply_to = '', reply_to_user = '' WHERE reply_to_user = ?1`,
			`DELETE FROM messages WHERE author_id = ?1`,
			`DELETE FROM members WHERE user_id = ?1`,
			`DELETE FROM tokens WHERE user_id = ?1`,
			`DELETE FROM user_settings WHERE user_id = ?1`,
			`DELETE FROM settings_owner WHERE user_id = ?1`,
			`DELETE FROM user_profiles WHERE user_id = ?1`,
			`DELETE FROM friendships WHERE user_a = ?1 OR user_b = ?1`,
			`DELETE FROM friend_requests WHERE from_id = ?1 OR to_id = ?1`,
			`DELETE FROM blocks WHERE blocker_id = ?1 OR blocked_id = ?1`,
			`DELETE FROM friend_removals WHERE user_id = ?1 OR by_id = ?1`,
			`DELETE FROM moment_seen WHERE user_id = ?1`,
			`DELETE FROM sanctions WHERE user_id = ?1`,
			`UPDATE sanctions SET created_by = '' WHERE created_by = ?1`,
			`UPDATE sanctions SET revoked_by = '' WHERE revoked_by = ?1`,
			`DELETE FROM audit WHERE actor_id = ?1 OR target = ?1`,
		} {
			if _, err := tx.Exec(statement, userID); err != nil {
				return err
			}
		}
		comments := `DELETE FROM moment_comments WHERE author_id = ?`
		if keepComments {
			comments = `UPDATE moment_comments SET author_id = '' WHERE author_id = ?`
		}
		if _, err := tx.Exec(comments, userID); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM users WHERE id = ?`, userID)
		return err
	})
	if err != nil {
		return AccountDeletion{}, err
	}
	return result, nil
}

func userConversationsToDelete(tx *sql.Tx, userID string) ([]Conversation, error) {
	rows, err := tx.Query(`SELECT `+convCols+` FROM conversations c WHERE
		(c.kind = 'channel' AND c.created_by = ?1) OR
		(c.kind = 'dm' AND EXISTS (SELECT 1 FROM json_each(
			CASE WHEN json_valid(c.member_ids) THEN c.member_ids ELSE '[]' END) WHERE value = ?1))`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		cv, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cv)
	}
	return out, rows.Err()
}

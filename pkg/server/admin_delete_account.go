package server

import (
	"errors"
	"net/http"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

type deleteAccountInput struct {
	Username     string `json:"username"`
	KeepComments bool   `json:"keep_comments"`
}

func (s *Server) apiDeleteAccount(w http.ResponseWriter, r *http.Request, a adminSession) {
	if !a.user.Super {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return
	}
	var input deleteAccountInput
	if err := decodeBody(w, r, &input); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}

	s.accountWrites.Lock()
	defer s.accountWrites.Unlock()
	target, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	if target.Super || isAdmin(target) {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return
	}
	if target.Username != input.Username {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	s.writeAccountDeletion(w, s.deleteAccount(a.actor(), target, input.KeepComments, false))
}

func (s *Server) writeAccountDeletion(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrConflict) {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, protocol.CodeUserNotFound)
		return
	}
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// deleteAccount is called with accountWrites held, so the deletion and its
// notifications are ordered with concurrent account operations.
func (s *Server) deleteAccount(actor Actor, target storage.User, keepComments, simulationOnly bool) error {
	deleteUser := s.st.DeleteUser
	if simulationOnly {
		deleteUser = s.st.DeleteSimulationUser
	}
	deleted, err := deleteUser(target.ID, keepComments)
	if err != nil {
		return err
	}
	s.simulation.accountsRevision.Add(1)

	connections := s.hub.connectionsForUser(target.ID, nil)
	s.notifyAndClose(target.ID, nil, protocol.EvSessionReplaced, protocol.SessionReplacedEvent{
		Username: target.Username, Location: adminLocation, At: storage.Now(),
	})
	for _, c := range connections {
		s.clearChannelView(c, "")
		s.hub.detach(c)
	}
	for _, cv := range deleted.Conversations {
		s.clearDeletedChannelViews(cv.ID)
		gone, _ := protocol.NewEvent(protocol.EvChannelGone, protocol.ChannelGoneEvent{ConversationID: cv.ID})
		s.hub.broadcast(cv.ID, gone)
		if cv.Kind == protocol.KindChannel {
			s.publishActivity(Activity{Kind: "channel.deleted", Category: ActivitySocial,
				Actor: ActivityUser{ID: target.ID, Name: target.Username}, Channel: &LiveChannel{ID: cv.ID, Name: cv.Name}}, "")
		}
	}
	f, _ := protocol.NewEvent(protocol.EvUserDeleted, protocol.UserDeletedEvent{UserID: target.ID})
	s.hub.broadcastAuthenticated(f)
	s.publishActivity(Activity{Kind: "account.deleted", Category: ActivityAccount,
		Actor: ActivityUser{ID: target.ID, Name: target.Username}}, "")
	s.adminEmit(adminEvent{Type: "account.deleted", Required: PermUsersModerate, Data: map[string]string{"user_id": target.ID}})
	comments := "deleted"
	if keepComments {
		comments = "anonymized"
	}
	s.audit(actor, "delete_account", "", map[string]string{"comments": comments})
	return nil
}

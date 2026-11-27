package app

import (
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
)

func TestFailedSendPreservesCurrentAndOriginalDrafts(t *testing.T) {
	a := &App{st: client.NewAppState()}
	origin := a.st.EnsureConversation("origin")
	other := a.st.EnsureConversation("other")
	a.st.ActiveID = origin.ID
	a.st.RestoreInput("next")
	req := protocol.SendMessageReq{ConversationID: origin.ID, Body: "first"}
	a.recordFailedSend(origin, req, "")
	if a.st.InputText() != "next" || len(origin.FailedSends) != 1 || origin.FailedSends[0].Request != req {
		t.Fatalf("current draft = %q, failures = %+v", a.st.InputText(), origin.FailedSends)
	}

	a.st.StashDraft()
	a.st.ActiveID = other.ID
	a.st.LoadDraft(other.ID)
	a.st.RestoreInput("other draft")
	failed := a.st.EnsureConversation("unopened")
	req = protocol.SendMessageReq{ConversationID: failed.ID, Body: "returned"}
	a.recordFailedSend(failed, req, "")
	if a.st.InputText() != "other draft" || string(failed.Draft) != "returned" {
		t.Fatalf("current draft = %q, original draft = %q", a.st.InputText(), string(failed.Draft))
	}
}

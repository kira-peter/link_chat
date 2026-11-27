package app

import (
	"context"
	"crypto/rand"
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
)

func (a *App) sendCurrent(ctx context.Context) {
	a.sendDraft(ctx, false)
}

func (a *App) sendDraft(ctx context.Context, retry bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	tr, cv := a.tr, a.st.Active()
	if a.st.State != client.StateReady || tr == nil || cv == nil || a.st.Muted(nowTime().UnixMilli()) {
		return
	}

	text := a.st.InputText()
	req := protocol.SendMessageReq{ConversationID: cv.ID, Body: text}
	clearDraft := true
	if len(cv.FailedSends) > 0 {
		failed := cv.FailedSends[0]
		restored := failed.Restored && text == failed.Request.Body
		if retry || restored {
			req = failed.Request
			cv.FailedSends = cv.FailedSends[1:]
			clearDraft = restored
			retry = false
		}
	}
	if retry || req.Body == "" {
		return
	}
	if req.ClientMsgID == "" {
		req.ClientMsgID = rand.Text()
	}
	if clearDraft {
		a.st.TakeInput()
	}
	a.st.InputErr = ""
	uid := a.st.UserID
	go func() {
		f, err := tr.Call(ctx, protocol.OpMessageSend, req, 20*time.Second)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.st.UserID != uid || a.st.Conversations[req.ConversationID] != cv {
			return
		}

		var result protocol.MessageEvent
		if err == nil && f.Type == protocol.TypeRes && decodeData(f, &result) == nil && result.Message.ID != "" {
			a.st.InsertMessage(result.Message, true)
		} else {
			code := ""
			if err == nil && f.Type == protocol.TypeError {
				code = errorCode(f)
			}
			a.recordFailedSend(cv, req, code)
			if code == protocol.CodeMuted && a.tr == tr {
				var info protocol.SanctionInfo
				_ = decodeData(f, &info)
				a.applyMuteLocked(info)
			}
		}
		a.markDirty()
	}()
}

// recordFailedSend keeps every failed message available for retry. It puts the
// first failure back into an empty draft so the user can see and edit it.
func (a *App) recordFailedSend(cv *client.Conversation, req protocol.SendMessageReq, code string) {
	failed := client.FailedSend{Request: req, Code: code}
	if len(cv.FailedSends) == 0 {
		if a.st.ActiveID == cv.ID {
			if len(a.st.Input) == 0 {
				a.st.RestoreInput(req.Body)
				failed.Restored = true
			}
		} else if len(cv.Draft) == 0 {
			cv.Draft = []rune(req.Body)
			failed.Restored = true
		}
	}
	cv.FailedSends = append(cv.FailedSends, failed)
}

func (a *App) composerError() string {
	if a.st.InputErr != "" {
		return a.st.InputErr
	}
	cv := a.st.Active()
	if cv == nil || len(cv.FailedSends) == 0 {
		return ""
	}
	message := i18n.SendFailed.Get(a.lang)
	if code := cv.FailedSends[0].Code; code != "" {
		message = i18n.ErrText(code, "").Get(a.lang)
	}
	return message + " · " + i18n.RetryHint.Get(a.lang)
}

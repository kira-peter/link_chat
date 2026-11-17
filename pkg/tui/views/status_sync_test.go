package views

import (
	"strings"
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
)

// The notice that a setting did not reach the server is read whole on the
// narrowest two-column window, in both languages: the key legend gives way to it.
func TestSettingsNotSyncedFitsTheStatusBar(t *testing.T) {
	w := layout.MinViewWidth + 2*layout.GutterX
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		st := client.NewAppState()
		st.State = client.StateReady
		st.StatusMsg, st.StatusErr = i18n.SettingsNotSynced.Get(lang), true
		s := newScreen(t, w, 4)
		Chrome{T: toks()}.StatusBar(s, st, lang, w, 4)
		s.Show()
		if row := rowText(s, 3, w); !strings.Contains(row, st.StatusMsg) {
			t.Errorf("%s: the notice is cut at width %d: %q", lang, w, row)
		}
	}
}

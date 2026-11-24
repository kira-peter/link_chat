package views

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
)

func catIndex(t *testing.T, title string) int {
	t.Helper()
	for i, c := range Categories() {
		if c.Title.Get("zh") == title {
			return i
		}
	}
	t.Fatalf("no category %q", title)
	return -1
}

func press(n SettingsNav, k tcell.Key, choices int) (SettingsNav, SettingsAction) {
	return n.Key(k, 0, choices)
}

// The catalog covers every setting the client has, each with a kind, and the
// categories that change how chat looks ask for a preview.
func TestSettingsCatalogIsComplete(t *testing.T) {
	seen := map[SettingID]SettingKind{}
	for _, c := range Categories() {
		for _, it := range c.Items {
			if _, dup := seen[it.ID]; dup {
				t.Errorf("setting %v listed twice", it.ID)
			}
			seen[it.ID] = it.Kind
		}
	}
	for id, want := range map[SettingID]SettingKind{
		SetUsername: SettingReadOnly, SetServerAddr: SettingReadOnly, SetCredStore: SettingReadOnly,
		SetRemember: SettingChoice,
		SetTheme:    SettingChoice, SetMotion: SettingChoice,
		SetBubbleWidth: SettingChoice, SetDensity: SettingChoice, SetTimeFormat: SettingChoice,
		SetLanguage:  SettingChoice,
		SetMsgBorder: SettingChoice, SetMsgTime: SettingChoice,
		SetSelfText: SettingChoice, SetOtherText: SettingChoice,
		SetSelfBorder: SettingChoice, SetOtherBorder: SettingChoice,
	} {
		got, ok := seen[id]
		if !ok {
			t.Errorf("setting %v is missing from the catalog", id)
		} else if got != want {
			t.Errorf("setting %v is kind %v, want %v", id, got, want)
		}
	}
	for _, c := range Categories() {
		wantPreview := c.Title.Get("zh") == "外观" || c.Title.Get("zh") == "聊天" || c.Title.Get("zh") == "消息样式"
		if c.Preview != wantPreview {
			t.Errorf("category %s: preview %v, want %v", c.Title.Get("zh"), c.Preview, wantPreview)
		}
	}
	if len(SettingsCatalog) < 2 {
		t.Error("the categories are not grouped into blocks")
	}
}

// ↑ ↓ in the category column cross blocks and wrap round at the ends.
func TestCategoryColumnMovesAcrossBlocksAndWraps(t *testing.T) {
	last := ColumnLen() - 1 // the categories, then the account operations
	n := SettingsNav{}
	n, _ = press(n, tcell.KeyUp, 0)
	if n.Cat != last {
		t.Fatalf("↑ on the first category went to %d, want the last (%d)", n.Cat, last)
	}
	n, _ = press(n, tcell.KeyDown, 0)
	if n.Cat != 0 {
		t.Fatalf("↓ on the last went to %d, want the first", n.Cat)
	}
}

// The items of a category wrap round too.
func TestItemsWrap(t *testing.T) {
	cat := catIndex(t, "聊天")
	k := len(Categories()[cat].Items)
	n := SettingsNav{Cat: cat, Level: LevelItem, Item: k - 1}
	if n, _ = press(n, tcell.KeyDown, 0); n.Item != 0 {
		t.Fatalf("↓ on the last item went to %d, want 0", n.Item)
	}
	if n, _ = press(n, tcell.KeyUp, 0); n.Item != k-1 {
		t.Fatalf("↑ on the first item went to %d, want %d", n.Item, k-1)
	}
}

func TestRightEntersItemsOnTheFirstActionableOne(t *testing.T) {
	n := SettingsNav{Cat: catIndex(t, "账号")}
	n, act := press(n, tcell.KeyRight, 0)
	if n.Level != LevelItem || act != ActNone {
		t.Fatalf("→ in the categories: level %v, action %v", n.Level, act)
	}
	it := Categories()[n.Cat].Items[n.Item]
	if it.Kind == SettingReadOnly {
		t.Fatalf("→ landed on the read-only %v", it.ID)
	}
	if it.ID != SetRemember {
		t.Fatalf("→ landed on %v, want the first changeable item (remember me)", it.ID)
	}
}

func TestLeftAndEscWalkBackALevel(t *testing.T) {
	cat := catIndex(t, "聊天")
	n := SettingsNav{Cat: cat, Level: LevelItem, Item: 1}
	n, _ = press(n, tcell.KeyLeft, 0)
	if n.Level != LevelCategory || n.Cat != cat {
		t.Fatalf("← from the items: level %v cat %d", n.Level, n.Cat)
	}
	if _, act := press(n, tcell.KeyLeft, 0); act != ActLeave {
		t.Fatalf("← on the categories gives %v, want leave", act)
	}
	if _, act := press(n, tcell.KeyEscape, 0); act != ActLeave {
		t.Fatalf("Esc on the categories gives %v, want leave", act)
	}
	n = SettingsNav{Cat: cat, Level: LevelItem}
	if n2, _ := press(n, tcell.KeyEscape, 0); n2.Level != LevelCategory {
		t.Fatal("Esc in the items did not go back to the categories")
	}
}

// Read-only items can be reached but not changed.
func TestReadOnlyItemsDoNotEnterTheChoiceMode(t *testing.T) {
	n := SettingsNav{Cat: catIndex(t, "账号"), Level: LevelItem, Item: 0}
	if Categories()[n.Cat].Items[0].Kind != SettingReadOnly {
		t.Fatal("the first account item is expected to be read-only")
	}
	for _, k := range []tcell.Key{tcell.KeyRight, tcell.KeyEnter} {
		n2, act := press(n, k, 0)
		if n2 != n || act != ActNone {
			t.Fatalf("key %v on a read-only item: %+v, action %v", k, n2, act)
		}
	}
	// And ↑ ↓ do reach them.
	n = SettingsNav{Cat: catIndex(t, "账号"), Level: LevelItem, Item: 3}
	n, _ = press(n, tcell.KeyUp, 0)
	if n.Item != 2 {
		t.Fatalf("↑ skipped a read-only item: at %d", n.Item)
	}
}

// The account operations moved to the "me" page: the settings column holds
// the categories alone, and no row of it is an action.
func TestSettingsColumnHoldsOnlyCategories(t *testing.T) {
	if ColumnLen() != len(Categories()) {
		t.Fatalf("column %d rows, %d categories", ColumnLen(), len(Categories()))
	}
	for i := 0; i < ColumnLen(); i++ {
		if _, ok := ActionAt(i); ok {
			t.Fatalf("row %d is an action", i)
		}
	}
}

func TestChoiceModeMovesCommitsAndCancels(t *testing.T) {
	cat := catIndex(t, "聊天")
	n := SettingsNav{Cat: cat, Level: LevelItem, Item: 0}
	n, act := press(n, tcell.KeyRight, 3)
	if n.Level != LevelChoose || act != ActEnterChoose {
		t.Fatalf("→ on a choice: level %v, action %v", n.Level, act)
	}
	n.Choice, n.Original = 1, 1 // the app fills these from the current value

	n, _ = press(n, tcell.KeyDown, 3)
	if n.Choice != 2 {
		t.Fatalf("↓ from 1 of 3 gives %d, want 2", n.Choice)
	}
	if w, _ := press(n, tcell.KeyDown, 3); w.Choice != 0 {
		t.Fatalf("↓ on the last candidate gives %d, want 0 (wrapping round)", w.Choice)
	}

	// Cancel goes back to the original and says so.
	c, act := press(n, tcell.KeyLeft, 3)
	if act != ActCancel || c.Choice != 1 || c.Level != LevelItem {
		t.Fatalf("← in the choice mode: %+v, action %v", c, act)
	}
	c, act = press(n, tcell.KeyEscape, 3)
	if act != ActCancel || c.Choice != 1 {
		t.Fatalf("Esc in the choice mode: %+v, action %v", c, act)
	}

	// Commit keeps the candidate.
	for _, k := range []tcell.Key{tcell.KeyEnter, tcell.KeyRight} {
		c, act = press(n, k, 3)
		if act != ActCommit || c.Choice != 2 || c.Level != LevelItem {
			t.Fatalf("key %v in the choice mode: %+v, action %v", k, c, act)
		}
	}
}

// Every item's label fits its column in both languages, so none is cut off.
func TestItemLabelsFitTheirColumn(t *testing.T) {
	for _, c := range Categories() {
		for _, it := range c.Items {
			for _, l := range []i18n.Lang{i18n.ZH, i18n.EN} {
				if w := layout.StringWidth(it.Label.Get(l)); w > settingsLabelW {
					t.Errorf("%q (%s) is %d cells, the column is %d", it.Label.Get(l), l, w, settingsLabelW)
				}
			}
		}
	}
}

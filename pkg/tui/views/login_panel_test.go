package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/theme"
)

const (
	panelW = 80
	panelH = 30
)

// distinctToks returns tokens whose accent, muted and highlight colours all
// differ. The stock ink theme gives Accent and Text the same colour, which would
// make a shimmer between them invisible and the assertions vacuous.
func distinctToks() theme.Tokens {
	t := toks()
	t.Accent = tcell.NewRGBColor(10, 200, 10)
	t.Text = tcell.NewRGBColor(250, 250, 250)
	t.TextMuted = tcell.NewRGBColor(120, 120, 120)
	return t
}

func renderLogin(t *testing.T, tk theme.Tokens, m LoginModel, lang i18n.Lang) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, panelW, panelH)
	Dialog{T: tk}.RenderLogin(s, panelW, panelH, m, lang)
	s.Show()
	return s
}

func screenString(s tcell.SimulationScreen) string {
	var b strings.Builder
	for y := 0; y < panelH; y++ {
		b.WriteString(rowText(s, y, panelW))
		b.WriteByte('\n')
	}
	return b.String()
}

// findRune returns the first cell on row y holding r, or -1.
func findRune(s tcell.SimulationScreen, y int, r rune) int {
	for x := 0; x < panelW; x++ {
		if got, _, _, _ := s.GetContent(x, y); got == r {
			return x
		}
	}
	return -1
}

// rowOf returns the first row containing r, or -1.
func rowOf(s tcell.SimulationScreen, r rune) int {
	for y := 0; y < panelH; y++ {
		if findRune(s, y, r) >= 0 {
			return y
		}
	}
	return -1
}

func styleAt(s tcell.SimulationScreen, x, y int) (tcell.Color, tcell.AttrMask) {
	_, _, st, _ := s.GetContent(x, y)
	fg, _, attrs := st.Decompose()
	return fg, attrs
}

// panelEdges returns the outer panel's left and right border columns, read from
// its title row.
func panelEdges(t *testing.T, s tcell.SimulationScreen) (left, right int) {
	t.Helper()
	y := rowOf(s, '┌')
	if y < 0 {
		t.Fatalf("panel border not found:\n%s", screenString(s))
	}
	left = findRune(s, y, '┌')
	right = findRune(s, y, '┐')
	if right < 0 {
		t.Fatalf("panel right border not found:\n%s", screenString(s))
	}
	return left, right
}

// buttonRect locates the button frame: the second '┌' on the screen going down,
// the first being the panel itself.
func buttonRect(t *testing.T, s tcell.SimulationScreen) (x0, x1, y0, y1 int) {
	t.Helper()
	panelY := rowOf(s, '┌')
	y0 = -1
	for y := panelY + 1; y < panelH; y++ {
		if x := findRune(s, y, '┌'); x >= 0 {
			x0, y0 = x, y
			break
		}
	}
	if y0 < 0 {
		t.Fatalf("button frame not found:\n%s", screenString(s))
	}
	x1 = -1
	for x := x0 + 1; x < panelW; x++ {
		if r, _, _, _ := s.GetContent(x, y0); r == '┐' {
			x1 = x
			break
		}
	}
	y1 = y0 + 2
	if x1 < 0 {
		t.Fatalf("button top-right corner not found:\n%s", screenString(s))
	}
	return x0, x1, y0, y1
}

// labelCells returns the column, foreground and attributes of each glyph of the
// "登录" label, plus the row they sit on.
func labelCells(t *testing.T, s tcell.SimulationScreen) (xs []int, fgs []tcell.Color, attrs []tcell.AttrMask, y int) {
	t.Helper()
	_, _, y0, _ := buttonRect(t, s)
	y = y0 + 1
	for x := 0; x < panelW; x++ {
		if r, _, _, _ := s.GetContent(x, y); r == '登' || r == '录' {
			fg, at := styleAt(s, x, y)
			xs, fgs, attrs = append(xs, x), append(fgs, fg), append(attrs, at)
		}
	}
	if len(xs) != 2 {
		t.Fatalf("found %d label glyphs, want 2:\n%s", len(xs), screenString(s))
	}
	return xs, fgs, attrs, y
}

// ---------- 2.1 / 2.2 layout ----------

// The label and its input share a row, and both inputs start in the same column
// in either language, even though the labels differ in width.
func TestLoginInputsAlignInBothLanguages(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		s := renderLogin(t, toks(), LoginModel{
			Username: "zed", Password: "pw", Focus: LoginFocusSubmit,
		}, lang)

		uy := rowOf(s, 'z')
		py := rowOf(s, '•')
		if uy < 0 || py < 0 {
			t.Fatalf("%s: input rows not found (user=%d pass=%d):\n%s", lang, uy, py, screenString(s))
		}
		if uy == py {
			t.Fatalf("%s: both inputs on one row", lang)
		}
		if ux, px := findRune(s, uy, 'z'), findRune(s, py, '•'); ux != px {
			t.Fatalf("%s: inputs start at columns %d and %d", lang, ux, px)
		}
		if !strings.Contains(rowText(s, uy, panelW), i18n.LoginUsername.Get(lang)) {
			t.Fatalf("%s: username label not on its input's row", lang)
		}
		if !strings.Contains(rowText(s, py, panelW), i18n.LoginPassword.Get(lang)) {
			t.Fatalf("%s: password label not on its input's row", lang)
		}
	}
}

// The panel is shorter than the old 15-row layout and nothing is cut off.
func TestLoginPanelIsCompactAndComplete(t *testing.T) {
	if loginPanelHeight >= 15 {
		t.Fatalf("panel height %d, want fewer than the old 15 rows", loginPanelHeight)
	}
	s := renderLogin(t, toks(), LoginModel{
		Username: "zed", Focus: LoginFocusUsername, ServerShown: "ws://x/ws",
	}, i18n.ZH)
	whole := screenString(s)
	for _, want := range []string{
		i18n.LoginTitle.Get(i18n.ZH), i18n.LoginSubmit.Get(i18n.ZH),
		i18n.ForgotPassword.Get(i18n.ZH), i18n.RegisterNow.Get(i18n.ZH),
		i18n.LoginHint.Get(i18n.ZH), "ws://x/ws",
	} {
		if !strings.Contains(whole, want) {
			t.Fatalf("panel missing %q:\n%s", want, whole)
		}
	}
	if !strings.ContainsRune(whole, '└') {
		t.Fatalf("no bottom border:\n%s", whole)
	}
}

// ---------- 2.3 remembered ----------

func TestRememberedReplacesPasswordInput(t *testing.T) {
	for _, focus := range []LoginFocus{LoginFocusSubmit, LoginFocusPassword} {
		s := renderLogin(t, toks(), LoginModel{
			Username: "zed", Remembered: true, Focus: focus,
		}, i18n.ZH)
		whole := screenString(s)
		if !strings.Contains(whole, i18n.RememberedLogin.Get(i18n.ZH)) {
			t.Fatalf("focus %v: remembered note missing:\n%s", focus, whole)
		}
		if strings.ContainsRune(whole, '•') {
			t.Fatalf("focus %v: mask characters drawn for a login with no password:\n%s", focus, whole)
		}
		if strings.ContainsRune(whole, '█') {
			t.Fatalf("focus %v: a caret is shown where nothing can be typed:\n%s", focus, whole)
		}
	}
}

// ---------- 3.1 button shape ----------

func TestLoginButtonIsCompactCentredAndFramed(t *testing.T) {
	s := renderLogin(t, toks(), LoginModel{Username: "zed", Focus: LoginFocusSubmit}, i18n.ZH)
	x0, x1, y0, y1 := buttonRect(t, s)

	if y1-y0 != 2 {
		t.Fatalf("button is %d rows tall, want 3", y1-y0+1)
	}
	for x := x0 + 1; x < x1; x++ {
		if r, _, _, _ := s.GetContent(x, y0); r != '─' {
			t.Fatalf("top edge broken at %d: %q", x, r)
		}
		if r, _, _, _ := s.GetContent(x, y1); r != '─' {
			t.Fatalf("bottom edge broken at %d: %q", x, r)
		}
	}
	if r, _, _, _ := s.GetContent(x0, y0+1); r != '│' {
		t.Fatalf("left edge missing: %q", r)
	}
	if r, _, _, _ := s.GetContent(x1, y0+1); r != '│' {
		t.Fatalf("right edge missing: %q", r)
	}

	// Narrower than the input field, so it does not read as a banner.
	left, right := panelEdges(t, s)
	uy := rowOf(s, 'z')
	inputStart := findRune(s, uy, 'z') - 1
	fieldW := (right - 4) - inputStart
	if bw := x1 - x0 + 1; bw >= fieldW {
		t.Fatalf("button width %d is not narrower than the field width %d", bw, fieldW)
	}

	// Centred on the panel, give or take a cell of rounding.
	if d := (x0+x1)/2 - (left+right)/2; d < -1 || d > 1 {
		t.Fatalf("button centre %d is off the panel centre %d", (x0+x1)/2, (left+right)/2)
	}
}

// ---------- 3.2 focus colours ----------

func TestLoginButtonColoursFollowFocus(t *testing.T) {
	tk := distinctToks()
	focused := renderLogin(t, tk, LoginModel{Username: "zed", Focus: LoginFocusSubmit}, i18n.ZH)
	idle := renderLogin(t, tk, LoginModel{Username: "zed", Focus: LoginFocusUsername}, i18n.ZH)

	fx0, _, fy0, _ := buttonRect(t, focused)
	ix0, _, iy0, _ := buttonRect(t, idle)

	if fg, _ := styleAt(focused, fx0, fy0); fg != tk.Accent {
		t.Fatalf("focused frame colour %v, want accent %v", fg, tk.Accent)
	}
	if fg, _ := styleAt(idle, ix0, iy0); fg != tk.TextMuted {
		t.Fatalf("idle frame colour %v, want muted %v", fg, tk.TextMuted)
	}

	_, ffg, _, _ := labelCells(t, focused)
	_, ifg, _, _ := labelCells(t, idle)
	for i := range ffg {
		if ffg[i] != tk.Accent {
			t.Fatalf("focused label glyph %d is %v, want accent", i, ffg[i])
		}
		if ifg[i] != tk.TextMuted {
			t.Fatalf("idle label glyph %d is %v, want muted", i, ifg[i])
		}
	}
}

// ---------- 3.3 shimmer ----------

// A shimmer mid-pass recolours label cells; frame and glyph positions stay put.
func TestLoginButtonShimmerColoursCellsWithoutMovingThem(t *testing.T) {
	tk := distinctToks()
	base := LoginModel{Username: "zed", Focus: LoginFocusSubmit, Depth: anim.DepthTrue}

	still := renderLogin(t, tk, base, i18n.ZH)

	mid := base
	mid.Shimmer = true
	mid.Elapsed = anim.ButtonShimmer.Travel / 2
	swept := renderLogin(t, tk, mid, i18n.ZH)

	sx, sfg, _, sy := labelCells(t, still)
	wx, wfg, _, wy := labelCells(t, swept)
	if sy != wy || sx[0] != wx[0] || sx[1] != wx[1] {
		t.Fatalf("shimmer moved the label: %v/%d -> %v/%d", sx, sy, wx, wy)
	}
	changed := false
	for i := range sfg {
		if sfg[i] != wfg[i] {
			changed = true
		}
	}
	if !changed {
		t.Fatalf("mid-pass shimmer left every label cell at %v", sfg)
	}

	x0, _, y0, _ := buttonRect(t, swept)
	if fg, _ := styleAt(swept, x0, y0); fg != tk.Accent {
		t.Fatalf("shimmer recoloured the frame: %v", fg)
	}

	// In the rest between passes the label is back to its resting colour.
	gap := base
	gap.Shimmer = true
	gap.Elapsed = anim.ButtonShimmer.Travel + anim.ButtonShimmer.Pause/2
	_, pfg, _, _ := labelCells(t, renderLogin(t, tk, gap, i18n.ZH))
	for i, c := range pfg {
		if c != tk.Accent {
			t.Fatalf("glyph %d is %v during the rest, want accent", i, c)
		}
	}
}

// ---------- 3.4 degradation ----------

func TestLoginButtonShimmerDegrades(t *testing.T) {
	tk := distinctToks()
	elapsed := anim.ButtonShimmer.Travel / 2

	// Animation off: a static colour, whatever the clock says.
	off := renderLogin(t, tk, LoginModel{
		Username: "zed", Focus: LoginFocusSubmit, Shimmer: false,
		Elapsed: elapsed, Depth: anim.DepthTrue,
	}, i18n.ZH)
	_, ofg, oat, _ := labelCells(t, off)
	for i := range ofg {
		if ofg[i] != tk.Accent || oat[i]&tcell.AttrUnderline != 0 {
			t.Fatalf("animation off: glyph %d is %v attrs %v, want static accent", i, ofg[i], oat[i])
		}
	}

	// Not focused: no sweep even if the flag were set.
	idle := renderLogin(t, tk, LoginModel{
		Username: "zed", Focus: LoginFocusUsername, Shimmer: true,
		Elapsed: elapsed, Depth: anim.DepthTrue,
	}, i18n.ZH)
	_, ifg, _, _ := labelCells(t, idle)
	for i, c := range ifg {
		if c != tk.TextMuted {
			t.Fatalf("unfocused: glyph %d is %v, want muted", i, c)
		}
	}

	// Monochrome: colours unchanged, exactly one glyph carries the marker.
	mono := renderLogin(t, tk, LoginModel{
		Username: "zed", Focus: LoginFocusSubmit, Shimmer: true,
		Elapsed: elapsed, Depth: anim.DepthMono,
	}, i18n.ZH)
	_, mfg, mat, _ := labelCells(t, mono)
	marked := 0
	for i := range mfg {
		if mfg[i] != tk.Accent {
			t.Fatalf("mono: glyph %d recoloured to %v", i, mfg[i])
		}
		if mat[i]&tcell.AttrUnderline != 0 {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("mono: %d glyphs marked, want exactly 1", marked)
	}
}

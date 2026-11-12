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

// The panel stays compact: no stray blank rows, nothing cut off. The height is
// the rows the elements need plus a fixed handful of spacing rows, and one of
// those is the error line, which is reserved so the panel never jumps.
func TestLoginPanelIsCompactAndComplete(t *testing.T) {
	const (
		borders  = 2 // top and bottom frame
		fields   = 2 // username, password
		errorRow = 1 // reserved between the password and the button
		button   = 3
		links    = 1
		spacing  = 5 // under the title, between the fields, above and below the button, under the links
	)
	if want := borders + fields + errorRow + button + links + spacing; loginPanelHeight > want {
		t.Fatalf("panel height %d exceeds what its elements need (%d): there are stray rows", loginPanelHeight, want)
	}
	s := renderLogin(t, toks(), LoginModel{
		Username: "zed", Focus: LoginFocusUsername, ServerShown: "ws://x/ws",
	}, i18n.ZH)
	whole := screenString(s)
	for _, want := range []string{
		i18n.LoginTitle.Get(i18n.ZH), i18n.LoginSubmit.Get(i18n.ZH),
		i18n.ForgotPassword.Get(i18n.ZH), i18n.RegisterNow.Get(i18n.ZH),
	} {
		if !strings.Contains(whole, want) {
			t.Fatalf("panel missing %q:\n%s", want, whole)
		}
	}
	if !strings.ContainsRune(whole, '└') {
		t.Fatalf("no bottom border:\n%s", whole)
	}
}

// ---------- 3.4 remembered password row ----------

// countRune counts a rune across the whole screen.
func countRune(s tcell.SimulationScreen, r rune) int {
	n := 0
	for y := 0; y < panelH; y++ {
		for x := 0; x < panelW; x++ {
			if got, _, _, _ := s.GetContent(x, y); got == r {
				n++
			}
		}
	}
	return n
}

// A remembered login shows a fixed run of dots in an ordinary-width box: no hint
// text, no caret.
func TestRememberedShowsFixedDotsAndNoCaret(t *testing.T) {
	for _, focus := range []LoginFocus{LoginFocusSubmit, LoginFocusPassword} {
		s := renderLogin(t, toks(), LoginModel{
			Username: "zed", Remembered: true, Focus: focus,
		}, i18n.ZH)
		if got := countRune(s, '•'); got != rememberedDots {
			t.Fatalf("focus %v: %d placeholder dots, want %d:\n%s", focus, got, rememberedDots, screenString(s))
		}
		if countRune(s, '█') != 0 {
			t.Fatalf("focus %v: a caret is shown where nothing can be typed:\n%s", focus, screenString(s))
		}
		if strings.Contains(screenString(s), "已记住") {
			t.Fatalf("focus %v: the old hint text is still shown:\n%s", focus, screenString(s))
		}
	}
}

// The number of dots belongs to the panel, not to any password: it is the same
// whatever the model holds, so it cannot reveal a length.
func TestRememberedDotsIgnoreThePassword(t *testing.T) {
	a := renderLogin(t, toks(), LoginModel{Username: "zed", Remembered: true, Focus: LoginFocusSubmit, Password: ""}, i18n.ZH)
	b := renderLogin(t, toks(), LoginModel{Username: "zed", Remembered: true, Focus: LoginFocusSubmit, Password: "a-very-long-password-indeed"}, i18n.ZH)
	if countRune(a, '•') != countRune(b, '•') {
		t.Fatalf("dots depend on the password: %d vs %d", countRune(a, '•'), countRune(b, '•'))
	}
}

// Remembered and ordinary share one layout: the box sits in the same place at the
// same width, and the rest of the panel is identical cell for cell.
func TestRememberedAndPlainShareLayout(t *testing.T) {
	tk := distinctToks()
	plain := renderLogin(t, tk, LoginModel{Username: "zed", Focus: LoginFocusSubmit}, i18n.ZH)
	rem := renderLogin(t, tk, LoginModel{Username: "zed", Remembered: true, Focus: LoginFocusSubmit}, i18n.ZH)

	py := rowOf(plain, 'z') + 2
	for y := 0; y < panelH; y++ {
		for x := 0; x < panelW; x++ {
			pr, _, ps, _ := plain.GetContent(x, y)
			rr, _, rs, _ := rem.GetContent(x, y)
			if y == py && rr == '•' {
				continue // the only cells allowed to differ are the dots themselves
			}
			if pr != rr || ps != rs {
				t.Fatalf("cell (%d,%d) differs: plain %q vs remembered %q", x, y, pr, rr)
			}
		}
	}
	if countRune(rem, '•') != rememberedDots {
		t.Fatalf("expected the dots to be the only difference")
	}
}

// ---------- 3.1 / 3.2 / 3.3 the error line ----------

// The error sits between the password and the button, centred, in the danger
// colour.
func TestLoginErrorIsBetweenPasswordAndButtonCentred(t *testing.T) {
	tk := distinctToks()
	tk.Danger = tcell.NewRGBColor(220, 40, 40)
	// Deliberately free of the words "用户名" and "密码": the test locates the
	// rows by those labels, and an error that contained one would be found as a
	// label.
	const msg = "凭据有误请重试"
	s := renderLogin(t, tk, LoginModel{Username: "zed", Err: msg, Focus: LoginFocusPassword}, i18n.ZH)

	errY := -1
	for y := 0; y < panelH; y++ {
		if strings.Contains(rowText(s, y, panelW), msg) {
			errY = y
		}
	}
	if errY < 0 {
		t.Fatalf("error not shown:\n%s", screenString(s))
	}
	// The first row carrying the label, not the last: "忘记密码" further down also
	// contains "密码".
	passY := -1
	for y := 0; y < panelH && passY < 0; y++ {
		if strings.Contains(rowText(s, y, panelW), i18n.LoginPassword.Get(i18n.ZH)) {
			passY = y
		}
	}
	_, _, btnY, _ := buttonRect(t, s)
	if !(passY < errY && errY < btnY) {
		t.Fatalf("error row %d is not between password row %d and button row %d:\n%s", errY, passY, btnY, screenString(s))
	}

	// Centred on the panel.
	left, right := panelEdges(t, s)
	first, last := -1, -1
	for x := 0; x < panelW; x++ {
		if r, _, _, _ := s.GetContent(x, errY); r != ' ' && r != 0 && x > left && x < right {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	if d := (first+last)/2 - (left+right)/2; d < -2 || d > 2 {
		t.Fatalf("error spans %d..%d, off the panel centre %d", first, last, (left+right)/2)
	}
	// Danger colour.
	if fg, _ := styleAt(s, first, errY); fg != tk.Danger {
		t.Fatalf("error colour %v, want danger %v", fg, tk.Danger)
	}
}

// The panel does not move when an error appears or goes.
func TestLoginPanelDoesNotJumpOnError(t *testing.T) {
	tk := toks()
	with := renderLogin(t, tk, LoginModel{Username: "zed", Err: "出错了", Focus: LoginFocusSubmit}, i18n.ZH)
	without := renderLogin(t, tk, LoginModel{Username: "zed", Focus: LoginFocusSubmit}, i18n.ZH)

	_, _, wy0, _ := buttonRect(t, with)
	_, _, oy0, _ := buttonRect(t, without)
	if wy0 != oy0 {
		t.Fatalf("button moved from row %d to %d when the error appeared", oy0, wy0)
	}
	if rowOf(with, '└') != rowOf(without, '└') {
		t.Fatal("the panel's height changed with the error")
	}
	for _, link := range []string{i18n.ForgotPassword.Get(i18n.ZH), i18n.RegisterNow.Get(i18n.ZH)} {
		var wy, oy int = -1, -1
		for y := 0; y < panelH; y++ {
			if strings.Contains(rowText(with, y, panelW), link) {
				wy = y
			}
			if strings.Contains(rowText(without, y, panelW), link) {
				oy = y
			}
		}
		if wy != oy || wy < 0 {
			t.Fatalf("link %q moved from row %d to %d", link, oy, wy)
		}
	}
}

// A message wider than the panel is cut, stays on its one row, and does not break
// the frame.
func TestLoginErrorTooLongIsTruncatedOnOneRow(t *testing.T) {
	long := strings.Repeat("很长的错误信息", 12)
	s := renderLogin(t, toks(), LoginModel{Username: "zed", Err: long, Focus: LoginFocusSubmit}, i18n.ZH)

	rows := 0
	for y := 0; y < panelH; y++ {
		if strings.Contains(rowText(s, y, panelW), "很长的错误") {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("the error spans %d rows, want exactly 1:\n%s", rows, screenString(s))
	}
	// The right border is intact on every panel row.
	left, right := panelEdges(t, s)
	top := rowOf(s, '┌')
	bottom := rowOf(s, '└')
	for y := top + 1; y < bottom; y++ {
		if r, _, _, _ := s.GetContent(right, y); r != '│' {
			t.Fatalf("row %d: right border broken, found %q", y, r)
		}
		if r, _, _, _ := s.GetContent(left, y); r != '│' {
			t.Fatalf("row %d: left border broken, found %q", y, r)
		}
	}
}

// The panel carries no address and no key hint any more. "Signing in" takes the
// error line's place (a new attempt clears the old error, so the two never meet),
// and the address only appears inside an unreachable-server error.
func TestLoginPanelHasNoAddressOrHintRows(t *testing.T) {
	s := renderLogin(t, toks(), LoginModel{Username: "zed", ServerShown: "ws://x/ws", Focus: LoginFocusSubmit}, i18n.ZH)
	whole := screenString(s)
	if strings.Contains(whole, "ws://x/ws") {
		t.Fatalf("the address is shown on a panel with no error:\n%s", whole)
	}
	if strings.Contains(whole, i18n.LoginHint.Get(i18n.ZH)) {
		t.Fatalf("the key hint is still drawn:\n%s", whole)
	}

	errRow := func(m LoginModel, want string) int {
		sc := renderLogin(t, toks(), m, i18n.ZH)
		for y := 0; y < panelH; y++ {
			if strings.Contains(rowText(sc, y, panelW), want) {
				return y
			}
		}
		t.Fatalf("%q is not on the panel:\n%s", want, screenString(sc))
		return -1
	}
	eY := errRow(LoginModel{Username: "zed", Err: "出错了", Focus: LoginFocusSubmit}, "出错了")
	bY := errRow(LoginModel{Username: "zed", Busy: true, Focus: LoginFocusSubmit}, i18n.LoginSigning.Get(i18n.ZH))
	if eY != bY {
		t.Fatalf("signing in is on row %d, the error line is row %d", bY, eY)
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

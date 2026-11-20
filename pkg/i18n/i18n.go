// Package i18n holds every user-facing string in both languages.
//
// Strings live here rather than inline in views so that switching language at
// runtime is a repaint, not a restart, and so a missing translation shows up as
// an obvious fallback instead of a half-translated screen
// (spec tui-chat-client / 语言切换).
package i18n

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Lang is the interface language.
type Lang string

const (
	ZH Lang = "zh"
	EN Lang = "en"
)

// Parse turns a config value into a language, defaulting to Chinese.
func Parse(s string) Lang {
	if strings.EqualFold(s, "en") {
		return EN
	}
	return ZH
}

// T holds one key's translation.
type T struct{ zh, en string }

// Get renders the string in the given language.
func (t T) Get(l Lang) string {
	if l == EN {
		return t.en
	}
	return t.zh
}

// F renders the string then applies fmt formatting.
func (t T) F(l Lang, args ...any) string {
	return fmt.Sprintf(t.Get(l), args...)
}

func (t T) String() string { return t.zh }

// --- chrome ---

var (
	AppName = T{"link_chat", "link_chat"}
	Tagline = T{"终端里的聊天", "chat, in your terminal"}

	Connecting     = T{"连接中", "connecting"}
	Authenticating = T{"认证中", "authenticating"}
	Connected      = T{"在线", "online"}
	Disconnected   = T{"已断开", "disconnected"}
	Reconnecting   = T{"重连中", "reconnecting"}
	Offline        = T{"离线", "offline"}
	ServerDown     = T{"服务端不可用", "server unreachable"}
	// ServerNoReply is a request the server accepted but never answered.
	ServerNoReply = T{"服务端无响应，请稍后重试", "the server did not answer, try again"}
	// ConnLost is a connection that closed while a request was waiting.
	ConnLost = T{"连接已断开，请重试", "connection lost, try again"}
	// WaitRetry is the sign-in panel line while a backoff is counting down.
	// Use with fmt.Sprintf(i18n.WaitDuration(...)).
	WaitRetry = T{"等待 %s 后可重试", "retry in %s"}
	// ServerDownAt names the address that could not be reached, so a mistyped
	// address can be told apart from a server that is down. Use with fmt.Sprintf.
	ServerDownAt = T{"连不上 %s", "cannot reach %s"}
	Unconfigured = T{"未配置服务器", "no server configured"}
)

// --- login ---

var (
	LoginTitle    = T{"登录", "Sign in"}
	RegisterTitle = T{"注册", "Create account"}
	LoginUsername = T{"用户名", "Username"}
	LoginPassword = T{"密码", "Password"}
	LoginSubmit   = T{"登录", "Sign in"}
	LoginHint     = T{"Tab 切换 · Enter 确认 · Esc 退出", "Tab move · Enter confirm · Esc quit"}
	LoginConnect  = T{"正在连接…", "connecting…"}
	LoginSigning  = T{"正在登录…", "signing in…"}
	LoginFailed   = T{"登录失败", "sign-in failed"}
	// ClientOutdated is shown when the server refuses this client's version.
	// Use with fmt.Sprintf(current, minimum).
	// AccountBanned is the panel text for a banned account: the reason, then
	// until when. Use with fmt.Sprintf(reason, until).
	AccountBanned = T{"已被封禁：%s（至 %s）", "banned: %s (until %s)"}
	// Forever stands in for a date when a sanction has no end.
	Forever = T{"永久", "permanent"}
	// MutedUntil is shown in the composer while muted. Use with
	// fmt.Sprintf(until).
	MutedUntil = T{"禁言至 %s，草稿已保留", "muted until %s — draft kept"}
	// MutedForever is the composer text for a permanent mute.
	MutedForever   = T{"已被禁言，草稿已保留", "muted — draft kept"}
	ClientOutdated = T{"客户端 %s 过旧，请升级到 %s", "client %s too old, need %s+"}
	Registered     = T{"账号已创建", "account created"}
	NeedLogin      = T{"登录已过期，请重新登录", "session expired, sign in again"}

	// The two links under the button.
	ForgotPassword = T{"忘记密码", "Forgot password"}
	RegisterNow    = T{"立即注册", "Create account"}
)

// --- registration ---

var (
	RegHint         = T{"Tab 切换 · Enter 提交 · Esc 返回登录", "Tab move · Enter submit · Esc back"}
	RegQuestion     = T{"安全问题", "Security question"}
	RegAnswer       = T{"安全答案", "Answer"}
	RegPickHint     = T{"↑↓ 选择 · Enter 确认 · Esc 取消", "↑↓ choose · Enter confirm · Esc cancel"}
	RegAnswerHelp   = T{"请记住这个答案，忘记密码时需要它", "Remember this answer — it is how you recover your password"}
	RegRequired     = T{"请填写所有字段", "all fields are required"}
	RegCustomPrompt = T{"自行输入问题…", "type your own question…"}

	// Preset questions. Users may also type their own, but most people should
	// not have to invent one.
	SecurityQuestions = []T{
		{"你第一只宠物叫什么名字？", "What was the name of your first pet?"},
		{"你的出生城市是？", "Which city were you born in?"},
		{"你小学的名字是？", "What was the name of your primary school?"},
		{"你最喜欢的书是哪一本？", "What is your favourite book?"},
		{"你母亲的娘家姓是？", "What is your mother's maiden name?"},
	}
)

// --- session displacement ---
//
// The wording has to make clear this was not a network failure: someone signed in
// as this account somewhere else, and that is why this window went away.

var (
	SessionReplacedTitle  = T{"已在别处登录", "Signed in elsewhere"}
	SessionReplaced       = T{"这个账号在另一处登录了，本窗口已退出。如果那不是你，请立即修改密码。", "This account signed in somewhere else and this window was signed out. If that was not you, change your password now."}
	SessionReplacedWho    = T{"账号：%s", "Account: %s"}
	SessionReplacedAction = T{"Enter 重新登录 · Esc 退出", "Enter sign in again · Esc quit"}
	RecoverLabelLocation  = T{"登录地点", "Location"}
	RecoverLabelTime      = T{"登录时间", "Time"}
	RecoverLabelDevice    = T{"客户端", "Client"}
)

// --- password recovery ---

var (
	RecoverTitle    = T{"找回密码", "Reset password"}
	RecoverStep1    = T{"第 1 步，共 3 步 · 输入用户名", "Step 1 of 3 · your username"}
	RecoverStep2    = T{"第 2 步，共 3 步 · 回答问题", "Step 2 of 3 · answer the question"}
	RecoverStep3    = T{"第 3 步，共 3 步 · 设置新密码", "Step 3 of 3 · new password"}
	RecoverHint     = T{"Enter 继续 · Esc 返回上一步", "Enter continue · Esc back"}
	RecoverNew      = T{"新密码", "New password"}
	RecoverRepeat   = T{"确认密码", "Confirm password"}
	RecoverMismatch = T{"两次输入的密码不一致", "the passwords do not match"}
	RecoverCannot   = T{"该账号无法找回，请确认用户名或联系管理员", "this account cannot be recovered — check the username or ask an administrator"}
	RecoverDone     = T{"密码已重置，请用新密码登录", "password reset — sign in with the new password"}
	RecoverExpired  = T{"重置链接已过期，请重新开始", "the reset link expired, please start again"}
)

// --- conversation list ---

var (
	ListTitle     = T{"会话", "Conversations"}
	ListEmpty     = T{"还没有会话", "no conversations yet"}
	ListEmptySub  = T{"按 n 新建频道，按 o 发起私聊", "press n for a channel, o for a direct message"}
	ListFilter    = T{"筛选", "filter"}
	NewChannel    = T{"新建频道", "new channel"}
	OpenDM        = T{"发起私聊", "direct message"}
	ChannelName   = T{"频道名", "channel name"}
	PeerName      = T{"用户名", "username"}
	Create        = T{"创建", "create"}
	Open          = T{"打开", "open"}
	Cancel        = T{"取消", "cancel"}
	Delete        = T{"删除", "delete"}
	DeleteConfirm = T{"确认删除？", "delete for good?"}
)

// --- sidebar sections and foot entries ---

var (
	SectionDMTitle      = T{"私聊", "Direct messages"}
	SectionChannelTitle = T{"群聊", "Channels"}
	// The button at the top of each section that starts a new one. Kept short:
	// the narrowest sidebar leaves 15 cells for a row.
	SectionDMNew      = T{"+ 发起私聊", "+ New message"}
	SectionChannelNew = T{"+ 新建频道", "+ New channel"}

	EntrySettings = T{"设置", "Settings"}
	EntryHelp     = T{"帮助", "Help"}
	EntryAbout    = T{"关于", "About"}

	AboutTitle = T{"关于", "About"}
	AboutBlurb = T{"终端里的聊天，界面克制，细节讲究。", "Chat in the terminal, kept simple."}
	AboutVer   = T{"版本", "Version"}
	// The help page and a channel the user has not joined.
	HelpPageTitle  = T{"帮助", "Help"}
	PreviewMembers = T{"%d 位成员", "%d members"}
	PreviewLast    = T{"最近活动 %s", "last active %s"}
	// The pages of the sections' "new" buttons.
	NewDMBlurb      = T{"和某个人单独聊天。输入对方的用户名。", "Talk to one person. Enter their username."}
	NewChannelBlurb = T{"建一个频道，所有人都能找到并加入。", "Start a channel anyone can find and join."}
	NewPageEnter    = T{"按 → 或 Enter 开始输入", "Press → or Enter to start typing"}
	NewPageSubmit   = T{"Enter 确定 · Esc 返回", "Enter to confirm · Esc to go back"}
	NewPageBusy     = T{"正在创建…", "creating…"}
	PreviewJoin     = T{"按 → 或 Enter 加入这个频道", "Press → or Enter to join this channel"}
	PreviewNoTopic  = T{"还没有简介", "No description yet"}
	PreviewNotYet   = T{"你还不是这个频道的成员，加入后才能看到消息。", "You are not a member yet; join to see the messages."}
)

// --- transcript ---

var (
	NoMessages     = T{"还没有消息", "no messages yet"}
	NoMessagesSub  = T{"说点什么开启对话", "say something to start"}
	LoadingOlder   = T{"加载更早的消息…", "loading earlier messages…"}
	ReachedStart   = T{"到达会话开头", "start of conversation"}
	NewBelow       = T{"有新消息 · 按 End 跳到底部", "new messages · End to jump"}
	MessageDeleted = T{"消息已删除", "message deleted"}
	MessageEdited  = T{"已编辑", "edited"}
	JoinedChannel  = T{"加入了频道", "joined"}
	LeftChannel    = T{"离开了频道", "left"}
	You            = T{"你", "you"}
)

// --- composer ---

var (
	// ComposeHint is the empty-composer prompt that a band of light sweeps across.
	// It must be long enough for the band to read as *travelling*: the band is
	// about 2.6 cells wide, so a seven-letter word is lit all at once and looks
	// like a flash, not a slide. Both languages stay above ComposeHintMinCells.
	ComposeHint     = T{"在这里输入消息…", "type in a message"}
	SendHint        = T{"Enter 发送 · Shift+Enter 换行", "Enter send · Shift+Enter newline"}
	InputTooLong    = T{"内容超出长度限制", "message too long"}
	SendFailed      = T{"发送失败", "send failed"}
	RetryHint       = T{"按 Ctrl+R 重试", "Ctrl+R retries"}
	ReadOnlyOffline = T{"离线中，草稿已保留", "offline — draft kept"}
	ScrollHint      = T{"Alt+↑↓ 滚动历史", "Alt+↑↓ scroll"}
)

// ComposeHintMinCells is the shortest the composer hint may be, in terminal cells.
// A test holds both languages to it, so a later rewording cannot quietly shrink
// the hint until the animation stops looking like a sweep.
const ComposeHintMinCells = 14

// --- status bar ---
//
// Every command carries an Alt prefix. Letters are never commands: the composer
// is the main surface, and a letter that sometimes types and sometimes opens a
// dialog is a defect, not a shortcut.
var (
	KeyPrefix      = T{"Alt+", "Alt+"}
	KeyHelp        = T{"Alt+H 帮助", "Alt+H help"}
	KeyQuit        = T{"Alt+Q 退出", "Alt+Q quit"}
	KeyNew         = T{"Alt+N 新频道", "Alt+N channel"}
	KeyDM          = T{"Alt+O 私聊", "Alt+O dm"}
	KeySettings    = T{"Alt+S 设置", "Alt+S settings"}
	KeySwitch      = T{"Tab 切换区域", "Tab switch pane"}
	KeyFilter      = T{"Alt+F 筛选", "Alt+F filter"}
	KeyBack        = T{"Esc 返回", "Esc back"}
	KeySettingsNav = T{"↑↓ 选择 · ← → 切换层级 · Esc 返回", "↑↓ choose · ← → levels · Esc back"}
	KeyPageBack    = T{"← 或 Esc 返回列表", "← or Esc back to the list"}
	KeyNewPage     = T{"Enter 确定 · Esc 返回列表", "Enter to confirm · Esc back to the list"}
	KeyHelpScroll  = T{"↑↓ PgUp PgDn 滚动 · ← 或 Esc 返回列表", "↑↓ PgUp PgDn scroll · ← or Esc back"}
	KeyMotion      = T{"Alt+M 动效", "Alt+M motion"}
	KeyLang        = T{"Alt+T 语言", "Alt+T language"}
	KeySend        = T{"Enter 发送", "Enter send"}
)

// --- settings ---

var (
	SettingsTitle = T{"设置", "Settings"}
	SetGeneral    = T{"通用", "General"}
	// Settings blocks and the chat category (spec tui-settings).
	SetBlockPersonal  = T{"个人", "Personal"}
	SetBlockInterface = T{"界面", "Interface"}
	SetBlockSystem    = T{"系统", "System"}
	SetChat           = T{"聊天", "Chat"}
	SetBubbleWidth    = T{"气泡最大宽度", "Bubble width"}
	SetDensity        = T{"消息密度", "Density"}
	SetTimeFormat     = T{"时间格式", "Time format"}
	SetMsgStyle       = T{"消息样式", "Messages"}
	SetMsgBorder      = T{"消息边框", "Border"}
	SetMsgTime        = T{"消息时间", "Message time"}
	SetSelfText       = T{"我方文字颜色", "My text"}
	SetOtherText      = T{"对方文字颜色", "Their text"}
	SetSelfBorder     = T{"我方边框颜色", "My border"}
	SetOtherBorder    = T{"对方边框颜色", "Their border"}
	ColorDefault      = T{"默认", "Default"}
	ColorCyan         = T{"青", "Cyan"}
	ColorBlue         = T{"蓝", "Blue"}
	ColorGreen        = T{"绿", "Green"}
	ColorAmber        = T{"琥珀", "Amber"}
	ColorRose         = T{"玫红", "Rose"}
	ColorViolet       = T{"紫", "Violet"}
	SetShow           = T{"显示", "Shown"}
	SetHide           = T{"隐藏", "Hidden"}
	DensityComfort    = T{"舒适", "Comfortable"}
	DensityCompact    = T{"紧凑", "Compact"}
	Time24h           = T{"24 小时", "24-hour"}
	Time12h           = T{"12 小时", "12-hour"}
	ThemeInk          = T{"墨（深色）", "Ink (dark)"}
	ThemePaper        = T{"纸（浅色）", "Paper (light)"}
	PreviewTitle      = T{"预览", "Preview"}
	SetAppearance     = T{"外观", "Appearance"}
	SetAccount        = T{"账号", "Account"}
	SetTheme          = T{"主题", "Theme"}
	SetLanguage       = T{"语言", "Language"}
	SetMotion         = T{"动画", "Animation"}
	SetServer         = T{"服务器", "Server"}
	SetUser           = T{"当前用户", "Signed in as"}
	SetLogout         = T{"退出登录", "Sign out"}
	SetSwitchAccount  = T{"切换账号", "Switch account"}
	SwitchAccountNote = T{"打开账号列表，选择本机登录过的其他账号。当前账号的登录记录会保留。", "Open the account list to pick another account used on this device. This account stays remembered."}
	SignOutNote       = T{"结束当前会话并返回登录界面。本机保存的登录信息会保留，可以直接再次登录。", "End this session and return to the sign-in screen. The saved sign-in is kept, so you can sign straight back in."}
	// The sign-out confirmation.
	LogoutTitle   = T{"退出登录", "Sign out"}
	LogoutMessage = T{"确定要退出当前账号吗？\n本机保存的登录信息会保留。", "Sign out of this account?\nThe saved sign-in on this device is kept."}
	LogoutConfirm = T{"退出", "Sign out"}
	ActionHint    = T{"按 → 或 Enter 继续", "Press → or Enter to continue"}
	SwitchHint    = T{"按 → 或 Enter 打开账号列表", "Press → or Enter to open the account list"}
	// The account list.
	AccountsTitle = T{"选择账号", "Choose an account"}
	AccountsOther = T{"使用其他账号", "Use another account"}
	AccountsSaved = T{"已记住", "remembered"}
	AccountsNone  = T{"需要密码", "password needed"}
	SetRemember   = T{"记住登录", "Remember me"}
	SetCredStore  = T{"凭据存储", "Credentials"}

	// Credential storage.
	CredentialSaveFailed = T{"无法保存登录凭据，下次启动需要重新输入密码", "could not save the credential; you will be asked for your password next time"}
	CredKeyring          = T{"系统凭据库", "system keyring"}
	CredFile             = T{"本地文件（无凭据库可用）", "local file (no keyring available)"}
	RememberOffHint      = T{"已关闭：每次启动都需要输入密码", "off: a password is required on every launch"}
	SetOn                = T{"开启", "on"}
	SetOff               = T{"关闭", "off"}
	SetSaved             = T{"已保存", "saved"}
	SettingsNotSynced    = T{"已保存在本机，未能同步到服务器", "saved here, not synced to the server"}
	SetUnknown           = T{"此值不受支持，已回退默认", "unsupported value, using default"}
)

// --- help ---

var (
	HelpTitle   = T{"键盘", "Keyboard"}
	HelpGlobal  = T{"全局", "Global"}
	HelpList    = T{"会话列表", "Conversation list"}
	HelpCompose = T{"输入", "Composer"}
	HelpClose   = T{"按 ? 或 Esc 关闭", "? or Esc to close"}
)

// Help rows: key, description.
type HelpRow struct {
	Key  string
	Desc T
}

var HelpGlobalRows = []HelpRow{
	{"Tab", T{"在列表与输入框之间切换", "switch between list and composer"}},
	{"Alt+H", T{"显示这个帮助", "show this help"}},
	{"Alt+S", T{"打开设置", "open settings"}},
	{"Alt+N", T{"新建频道", "create channel"}},
	{"Alt+O", T{"发起私聊", "start a direct message"}},
	{"Alt+F", T{"按名称筛选会话", "filter conversations"}},
	{"Alt+↑↓", T{"向上 / 向下翻阅历史", "scroll history"}},
	{"Alt+End", T{"跳到底部", "jump to newest"}},
	{"Alt+T", T{"切换中英文", "switch language"}},
	{"Alt+M", T{"开关动画", "toggle motion"}},
	{"Alt+Q", T{"退出", "quit"}},
	{"Esc", T{"返回 / 关闭对话框", "back / close dialog"}},
}

var HelpListRows = []HelpRow{
	{"↑ ↓ / k j", T{"移动选择，右侧随之显示", "move; the right side follows"}},
	{"→ / Enter", T{"进入右侧（未加入的频道会加入）", "enter the right side (joins a channel)"}},
	{"←", T{"从右侧回到列表", "back to the list"}},
}

var HelpComposeRows = []HelpRow{
	{"Enter", T{"发送消息", "send"}},
	{"Shift+Enter", T{"插入换行", "insert newline"}},
	{"← →", T{"移动光标", "move caret"}},
	{"Ctrl+W", T{"删除前一个词", "delete previous word"}},
	{"Ctrl+U", T{"清空输入", "clear input"}},
	{"Home/End", T{"行首 / 行尾", "line start / end"}},
	{"↑ ↓", T{"输入为空时翻阅历史", "scroll history when empty"}},
}

// --- errors ---

// ErrText maps a server error code onto a sentence a person can act on. Unknown
// codes fall back to the server's own message so a newer server still explains
// itself.
func ErrText(code, serverMessage string) T {
	switch code {
	case "invalid_credentials":
		return T{"用户名或密码不正确", "username or password is incorrect"}
	case "username_taken":
		return T{"用户名已被占用", "that username is taken"}
	case "weak_password":
		return T{"密码至少 8 个字符", "password needs at least 8 characters"}
	case "invalid_username":
		return T{"用户名只能用字母、数字和 _ - .", "use only letters, digits, _ - ."}
	case "invalid_channel_name":
		return T{"频道名只能用字母、数字和 _ - .", "use only letters, digits, _ - ."}
	case "channel_exists":
		return T{"频道已存在", "that channel exists"}
	case "channel_not_found":
		return T{"频道不存在", "no such channel"}
	case "conversation_not_found":
		return T{"会话不存在", "no such conversation"}
	case "forbidden":
		return T{"没有权限", "not permitted"}
	case "unauthorized":
		return T{"需要登录", "sign-in required"}
	case "token_expired":
		return T{"登录已过期", "session expired"}
	case "rate_limited":
		return T{"操作太频繁，请稍后再试", "too many attempts, wait a moment"}
	case "message_too_long":
		return T{"消息太长", "message too long"}
	case "user_not_found":
		return T{"找不到这个用户", "no such user"}
	case "storage_unavailable":
		return T{"服务端存储异常", "server storage problem"}
	case "server_shutting_down":
		return T{"服务端正在重启", "server restarting"}
	case "account_banned":
		return T{"账号已被封禁", "account banned"}
	case "muted":
		return T{"你已被禁言", "you are muted"}
	}
	if serverMessage != "" {
		return T{serverMessage, serverMessage}
	}
	return T{"出了点问题", "something went wrong"}
}

// --- formatting helpers ---

// RelTime renders a timestamp the way a chat list should: today shows the clock
// only, older days show a date, so no row wastes space on redundant info
// (spec tui-chat-client / 消息中的时间格式).
func RelTime(millis int64, now int64, l Lang) string {
	if millis == 0 {
		return ""
	}
	d := now - millis
	switch {
	case d < 60_000:
		if l == EN {
			return "now"
		}
		return "刚刚"
	case d < 3_600_000:
		m := d / 60_000
		if l == EN {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%d分", m)
	case sameDay(millis, now):
		return clock(millis)
	case d < 7*86_400_000:
		days := d / 86_400_000
		if l == EN {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%d天", days)
	default:
		return date(millis)
	}
}

// ClockOnly is used inside the transcript where the day is already implied.
func ClockOnly(millis int64) string { return clock(millis) }

// FullTime is used in tooltips and the details dialog.
func FullTime(millis int64) string {
	if millis == 0 {
		return ""
	}
	t := timeFromMillis(millis)
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d",
		t.Year, int(t.Month), t.Day, t.Hour, t.Minute)
}

func clock(millis int64) string {
	t := timeFromMillis(millis)
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

func date(millis int64) string {
	t := timeFromMillis(millis)
	return fmt.Sprintf("%d/%d", int(t.Month), t.Day)
}

func sameDay(a, b int64) bool {
	ta, tb := timeFromMillis(a), timeFromMillis(b)
	return ta.Year == tb.Year && ta.Month == tb.Month && ta.Day == tb.Day
}

// WaitDuration renders a wait for the sign-in countdown: seconds under a
// minute, minutes and seconds under an hour, hours and minutes above it.
func WaitDuration(d time.Duration, l Lang) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second) / time.Second)
	h, m, s := total/3600, (total%3600)/60, total%60
	switch {
	case h > 0:
		if l == EN {
			return fmt.Sprintf("%dh %dm", h, m)
		}
		return fmt.Sprintf("%d 小时 %d 分", h, m)
	case m > 0:
		if l == EN {
			return fmt.Sprintf("%dm %ds", m, s)
		}
		return fmt.Sprintf("%d 分 %d 秒", m, s)
	}
	if l == EN {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%d 秒", s)
}

// Num renders an integer for embedding in a label.
func Num(n int) string { return strconv.Itoa(n) }

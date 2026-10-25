// Package tui is the lnch dashboard.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lnch.sh/internal/browser"
	"lnch.sh/internal/present"
	"lnch.sh/internal/state"
)

// Source is the running stack the dashboard displays.
type Source interface {
	Refresh(context.Context) (state.Runtime, map[string][]string, error)
	Restart(context.Context, string) error
	Share(context.Context) (string, error)
	Shutdown(context.Context) error
}

// Options controls what quitting the dashboard does.
type Options struct {
	QuitShutsDown bool
}

// Run starts the full-screen dashboard and blocks until it closes.
func Run(src Source, opt Options) error {
	m := newModel(src, opt)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

var (
	cAccent = lipgloss.Color("#D6FF4A")
	cMuted  = lipgloss.Color("#8B957F")
	cWarn   = lipgloss.Color("#FFB15A")
	cBad    = lipgloss.Color("#FF6B57")
	cText   = lipgloss.Color("#E6EDD9")

	titleStyle  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(cMuted)
	textStyle   = lipgloss.NewStyle().Foreground(cText)
	warnStyle   = lipgloss.NewStyle().Foreground(cWarn)
	badStyle    = lipgloss.NewStyle().Foreground(cBad)
	okStyle     = lipgloss.NewStyle().Foreground(cAccent)
	selectStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

type tickMsg time.Time
type shareMsg struct {
	url string
	err error
}
type noteMsg struct{ text string }

type model struct {
	src       Source
	quitDown  bool
	services  []state.Service
	logs      map[string][]string
	project   string
	share     string
	hint      string
	errText   string
	selected  int
	filter    textinput.Model
	filtering bool
	vp        viewport.Model
	width     int
	height    int
	showQR    bool
	qr        string
	qrFor     string
	ready     bool
}

func newModel(src Source, opt Options) model {
	ti := textinput.New()
	ti.Placeholder = "filter"
	ti.CharLimit = 80
	ti.Prompt = "/ "
	ti.Width = 24
	return model{
		src:      src,
		quitDown: opt.QuitShutsDown,
		filter:   ti,
		vp:       viewport.New(40, 8),
	}
}

func (m model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.syncLogs()
		return m, nil
	case tickMsg:
		m.pull()
		return m, tick()
	case shareMsg:
		if msg.err != nil && msg.url == "" {
			m.errText = msg.err.Error()
		} else {
			m.share = msg.url
			m.errText = ""
		}
		return m, nil
	case noteMsg:
		m.errText = msg.text
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			return m.updateFilter(msg)
		}
		return m.updateKey(msg)
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter.Blur()
		m.syncLogs()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		m.syncLogs()
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.syncLogs()
	return m, cmd
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "X":
		if m.quitDown {
			return m, tea.Quit
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			_ = m.src.Shutdown(ctx)
			return tea.Quit()
		}
	case "up", "k":
		if m.selected > 0 {
			m.selected--
			m.syncLogs()
		}
		return m, nil
	case "down", "j":
		if m.selected < len(m.services)-1 {
			m.selected++
			m.syncLogs()
		}
		return m, nil
	case "r":
		name := m.currentName()
		if name == "" {
			return m, nil
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := m.src.Restart(ctx, name)
			if err != nil {
				return noteMsg{err.Error()}
			}
			return noteMsg{"restarted " + name}
		}
	case "s":
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			url, err := m.src.Share(ctx)
			return shareMsg{url: url, err: err}
		}
	case "o", "enter":
		if u := m.openURL(); u != "" {
			if err := browser.Open(u); err != nil {
				m.errText = err.Error()
			} else {
				m.errText = "opened " + u
			}
		}
		return m, nil
	case "g":
		m.showQR = !m.showQR
		m.syncLogs()
		return m, nil
	case "/":
		m.filtering = true
		m.showQR = false
		m.filter.Focus()
		return m, textinput.Blink
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) pull() {
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	rt, logs, err := m.src.Refresh(ctx)
	if err != nil {
		m.errText = err.Error()
		return
	}
	if m.errText != "" && !strings.HasPrefix(m.errText, "opened ") && !strings.HasPrefix(m.errText, "restarted ") {
		m.errText = ""
	}
	m.project = rt.Project
	m.services = rt.Services
	m.logs = logs
	m.share = rt.ShareURL
	m.hint = rt.ShareHint
	m.ready = true
	if m.selected >= len(m.services) {
		m.selected = 0
	}
	if m.showQR && m.share != "" && m.share != m.qrFor && present.WantQR() {
		if qr, err := present.QR(m.share); err == nil {
			m.qr = qr
			m.qrFor = m.share
		}
	}
	m.layout()
	m.syncLogs()
}

func (m *model) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	overhead := 4 + len(m.services)
	if m.share != "" {
		overhead++
	}
	h := m.height - overhead
	if h < 3 {
		h = 3
	}
	w := m.width
	if w < 20 {
		w = 20
	}
	m.vp.Width = w
	m.vp.Height = h
}

func (m *model) syncLogs() {
	follow := m.vp.AtBottom() || !m.ready
	var body string
	if m.showQR {
		if m.qr == "" {
			body = "No share URL yet. Press s to share.\n"
		} else if m.width > 0 && m.width < 40 {
			body = "Terminal is too narrow for a QR code.\n" + m.share + "\n"
		} else {
			body = m.qr
		}
	} else {
		body = strings.Join(m.visibleLogs(), "\n")
		if body == "" {
			body = mutedStyle.Render("waiting for logs")
		}
	}
	m.vp.SetContent(body)
	if follow {
		m.vp.GotoBottom()
	}
}

func (m model) visibleLogs() []string {
	name := m.currentName()
	lines := m.logs[name]
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		return lines
	}
	var out []string
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), q) {
			out = append(out, line)
		}
	}
	return out
}

func (m model) currentName() string {
	if m.selected < 0 || m.selected >= len(m.services) {
		return ""
	}
	return m.services[m.selected].Name
}

func (m model) openURL() string {
	if m.selected >= 0 && m.selected < len(m.services) {
		if u := state.BestLocal(m.services[m.selected]); u != "" {
			return u
		}
	}
	return state.OpenURL(state.Runtime{Services: m.services, ShareURL: m.share})
}

func (m model) View() string {
	if m.width == 0 {
		return "lnch"
	}
	var b strings.Builder
	healthy := 0
	for _, s := range m.services {
		if s.Status == state.Healthy || s.Status == state.Running {
			healthy++
		}
	}
	head := titleStyle.Render("lnch") + "  " + textStyle.Render(m.project)
	right := mutedStyle.Render(fmt.Sprintf("%d/%d up", healthy, len(m.services)))
	fmt.Fprintf(&b, "%s\n", place(head, right, m.width))
	if m.share != "" {
		fmt.Fprintf(&b, "%s\n", truncStyle(okStyle, "share  "+m.share, m.width))
	}
	b.WriteByte('\n')
	if len(m.services) == 0 {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("  detecting services"))
	}
	for i, s := range m.services {
		mark := "  "
		if i == m.selected {
			mark = selectStyle.Render("▌ ")
		}
		url := state.BestLocal(s)
		if url == "" {
			url = "-"
		}
		line := fmt.Sprintf("%-12s %-10s %s", s.Name, s.Status, url)
		fmt.Fprintf(&b, "%s%s\n", mark, styleStatus(s.Status, line, m.width-2))
	}
	label := "logs"
	if name := m.currentName(); name != "" {
		label = "logs  " + name
	}
	if m.showQR {
		label = "qr"
	}
	filter := ""
	if m.filtering {
		filter = m.filter.View()
	} else if m.filter.Value() != "" {
		filter = mutedStyle.Render("/ " + m.filter.Value())
	}
	fmt.Fprintf(&b, "\n%s\n", place(mutedStyle.Render(label), filter, m.width))
	b.WriteString(m.vp.View())
	b.WriteByte('\n')
	if m.errText != "" {
		fmt.Fprintf(&b, "%s\n", truncStyle(warnStyle, m.errText, m.width))
	} else if m.hint != "" && m.share != "" {
		fmt.Fprintf(&b, "%s\n", truncStyle(mutedStyle, m.hint, m.width))
	}
	fmt.Fprintf(&b, "%s", mutedStyle.Render(m.footer()))
	return b.String()
}

func (m model) footer() string {
	quit := "q shutdown"
	if !m.quitDown {
		quit = "q close   X shutdown"
	}
	qr := "g qr"
	if m.showQR {
		qr = "g logs"
	}
	return quit + "   r restart   j/k select   / filter   " + qr + "   o open   s share"
}

func styleStatus(status, line string, width int) string {
	var style lipgloss.Style
	switch status {
	case state.Healthy:
		style = okStyle
	case state.Running:
		style = textStyle
	case state.Starting, state.Pending, state.Restarting:
		style = warnStyle
	case state.Unhealthy, state.Exited:
		style = badStyle
	default:
		style = mutedStyle
	}
	return truncStyle(style, line, width)
}

func truncStyle(style lipgloss.Style, s string, width int) string {
	if width > 0 {
		s = trunc(s, width)
	}
	return style.Render(s)
}

func place(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncStyle(textStyle, left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

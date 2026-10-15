// Package ui renders the two-region dashboard (standing cluster | burst nodes).
// The interactive shell lives in a separate tmux pane below it.
package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/scttfrdmn/burstlab-demo/internal/source"
	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

// Rows highlight briefly when their Slurm or EC2 state changes, so the
// audience's eye is drawn to what just happened.
const flashFor = 3 * time.Second

// Minimum data rows per pane; matches the 10 pre-enumerated burst slots and
// gives the left pane room for the queue under the compute nodes.
const minRows = 10

type tickMsg time.Time

type Model struct {
	src    state.Source
	sim    *source.Sim // non-nil in simulator mode
	snap   state.Snapshot
	width  int
	height int
	last   map[string]string
	flash  map[string]time.Time
}

func New(src state.Source) Model {
	m := Model{src: src, last: map[string]string{}, flash: map[string]time.Time{}, width: 140}
	if s, ok := src.(*source.Sim); ok {
		m.sim = s
	}
	return m
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Init() tea.Cmd { return tick() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case " ":
			if m.sim != nil {
				m.sim.TogglePause()
			}
		case "r":
			if m.sim != nil {
				m.sim.Restart()
			}
		case "right", "n":
			if m.sim != nil {
				m.sim.Skip(15)
			}
		}
	case tickMsg:
		m.snap = m.src.Snapshot()
		now := time.Time(msg)
		for _, n := range m.allNodes() {
			key := n.EC2State + "|" + n.SlurmState
			if prev, ok := m.last[n.Name]; ok && prev != key {
				m.flash[n.Name] = now
			}
			m.last[n.Name] = key
		}
		return m, tick()
	}
	return m, nil
}

func (m Model) allNodes() []state.Node {
	ns := append([]state.Node{m.snap.Head}, m.snap.Compute...)
	return append(ns, m.snap.Burst...)
}

// ---------------------------------------------------------------------------
// Layout

// column widths: NODE EC2 SLURM LOAD CPU UP
var cols = []int{12, 14, 16, 11, 6, 6}

func (m Model) View() string {
	if m.snap.Taken.IsZero() {
		return "connecting…"
	}
	w := m.width
	lw := w / 2
	rw := w - lw
	rows := max(minRows, len(m.snap.Burst), len(m.snap.Compute)+5)

	left := m.leftPane(lw, rows)
	right := m.rightPane(rw, rows)
	out := []string{m.titleBar(w)}
	for i := range left {
		out = append(out, left[i]+right[i])
	}
	if m.height > 0 && len(out) > m.height {
		out = out[:m.height]
	}
	return strings.Join(out, "\n")
}

func (m Model) titleBar(w int) string {
	s := m.snap
	left := " " + s.Title
	var right string
	switch {
	case m.sim != nil:
		right = "SIM  space pause · → skip · r restart"
	case s.SlurmErr != nil:
		right = sErr.Render("slurm: " + s.SlurmErr.Error())
	case s.EC2Err != nil:
		right = sErr.Render("ec2: " + s.EC2Err.Error())
	default:
		right = "LIVE"
	}
	right += "   " + s.Taken.Format("15:04:05") + " "
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// errors can be long; never let them push the clock off-screen
		right = truncate(right, max(0, w-lipgloss.Width(left)-1))
		gap = max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return sBar.Render(left + strings.Repeat(" ", gap) + right)
}

func (m Model) leftPane(w, rows int) []string {
	s := m.snap
	lines := []string{m.header()}
	lines = append(lines, m.nodeRow(s.Head))
	busy, total := 0, 0
	for _, n := range s.Compute {
		lines = append(lines, m.nodeRow(n))
		busy += n.CPUAlloc
		total += n.CPUTot
	}
	lines = append(lines, sDim.Render(" "+strings.Repeat("·", w-4)))
	lines = append(lines, fmt.Sprintf(" %s   running %s   pending %s",
		sHeader.Render("QUEUE"), sBold.Render(strconv.Itoa(s.Running)), pendingStyle(s.Pending)))
	room := rows + 1 - len(lines)
	lines = append(lines, queueLines(s.Jobs, room)...)

	top := fmt.Sprintf("%d nodes · %d/%d CPUs allocated", len(s.Compute), busy, total)
	return box(w, rows+1, "ON-PREM · standing cluster", top, lines, "partition: local")
}

func (m Model) rightPane(w, rows int) []string {
	s := m.snap
	lines := []string{m.header()}
	var up, starting, stopping int
	for _, n := range s.Burst {
		lines = append(lines, m.nodeRow(n))
		switch n.EC2State {
		case state.EC2Running:
			up++
		case state.EC2Pending:
			starting++
		case state.EC2ShuttingDown, state.EC2Stopping:
			stopping++
		}
	}
	if len(s.Burst) == 0 {
		lines = append(lines, sDim.Render("  no cloud nodes defined in slurm.conf"))
	}
	top := fmt.Sprintf("%d up · %d starting · %d stopping", up, starting, stopping)
	foot := "AWS Plugin for Slurm v2 · partition: aws"
	if s.SuspendTime > 0 {
		foot = "SuspendTime " + clock(s.SuspendTime) + " · " + foot
	}
	return box(w, rows+1, "CLOUD BURST", top, lines, foot)
}

func (m Model) header() string {
	names := []string{"NODE", "EC2", "SLURM", "LOAD", "CPU", "UP"}
	var b strings.Builder
	b.WriteString(" ")
	for i, n := range names {
		b.WriteString(sHeader.Render(pad(n, cols[i])))
	}
	return b.String()
}

func (m Model) nodeRow(n state.Node) string {
	now := m.snap.Taken
	nameStyle := sBold
	if n.Role == state.RoleBurst && (n.EC2State == state.EC2None || n.EC2State == state.EC2Terminated) {
		nameStyle = sDim
	}
	if t, ok := m.flash[n.Name]; ok && now.Sub(t) < flashFor {
		nameStyle = sFlash
	}
	ec2, ec2s := ec2Cell(n.EC2State)
	sl, sls := slurmCell(n, m.snap.SuspendTime, now)

	cpu, up := "", ""
	if n.Role == state.RoleHead {
		cpu = "  —"
	} else if n.CPUTot > 0 && n.EC2State != state.EC2None && n.EC2State != state.EC2Terminated {
		cpu = fmt.Sprintf("%d/%d", n.CPUAlloc, n.CPUTot)
	}
	if !n.LaunchTime.IsZero() && n.EC2State != state.EC2Terminated {
		up = clock(now.Sub(n.LaunchTime))
	}

	return " " +
		nameStyle.Render(pad(n.Name, cols[0]-1)) + " " +
		ec2s.Render(pad(ec2, cols[1])) +
		sls.Render(pad(sl, cols[2])) +
		loadBar(n, cols[3]-1) + " " +
		sPlain.Render(pad(cpu, cols[4])) +
		sDim.Render(padLeft(up, cols[5]))
}

func pendingStyle(n int) string {
	if n == 0 {
		return sBold.Render("0")
	}
	return lipgloss.NewStyle().Foreground(cYellow).Bold(true).Render(strconv.Itoa(n))
}

// box draws a rounded frame with a title and a right-aligned note in the top
// border and a note in the bottom border. Every returned line is exactly w wide.
func box(w, h int, title, note string, lines []string, foot string) []string {
	inner := w - 2
	t := "─ " + sTitle.Render(title) + " "
	n := " " + note + " ─"
	fill := inner - lipgloss.Width(t) - lipgloss.Width(n)
	if fill < 1 {
		n, fill = "─", max(1, inner-lipgloss.Width(t)-1)
	}
	out := []string{sBorder.Render("╭") + sBorder.Render("─ ") + sTitle.Render(title) + " " +
		sBorder.Render(strings.Repeat("─", fill)) + sDim.Render(n[:len(n)-len("─")]) + sBorder.Render("─╮")}
	for i := 0; i < h; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		lw := lipgloss.Width(l)
		if lw > inner {
			l = truncate(l, inner)
			lw = lipgloss.Width(l)
		}
		out = append(out, sBorder.Render("│")+l+strings.Repeat(" ", inner-lw)+sBorder.Render("│"))
	}
	f := " " + foot + " "
	out = append(out, sBorder.Render("╰"+strings.Repeat("─", max(0, inner-lipgloss.Width(f)-1)))+
		sDim.Render(f)+sBorder.Render("─╯"))
	return out
}

// ---------------------------------------------------------------------------
// Queue panel: squeue lists every running array task on its own line, which
// would never fit. Group by (job, state, partition) and compress nodelists.

type jobGroup struct {
	base, user, part, name, state string
	count                         int
	nodes                         []string
	reason                        string
	time                          string
}

func queueLines(jobs []state.Job, room int) []string {
	if room <= 0 {
		return nil
	}
	groups := map[string]*jobGroup{}
	var order []string
	for _, j := range jobs {
		base, tasks := splitArray(j.ID)
		key := base + "|" + j.State + "|" + j.Partition
		g, ok := groups[key]
		if !ok {
			g = &jobGroup{base: base, user: j.User, part: j.Partition, name: j.Name, state: j.State, time: j.Time}
			groups[key] = g
			order = append(order, key)
		}
		g.count += tasks
		if strings.HasPrefix(j.Nodes, "(") {
			g.reason = j.Nodes
		} else {
			g.nodes = append(g.nodes, j.Nodes)
		}
	}
	rank := map[string]int{"R": 0, "CG": 1, "CF": 2, "PD": 3}
	sort.SliceStable(order, func(a, b int) bool {
		ga, gb := groups[order[a]], groups[order[b]]
		ia, _ := strconv.Atoi(ga.base)
		ib, _ := strconv.Atoi(gb.base)
		if ia != ib {
			return ia < ib
		}
		return rank[ga.state] < rank[gb.state]
	})
	if len(order) == 0 {
		return []string{sDim.Render("  (queue empty)")}
	}
	var out []string
	for i, k := range order {
		if len(out) == room-1 && len(order)-i > 1 {
			out = append(out, sDim.Render(fmt.Sprintf("  … %d more", len(order)-i)))
			break
		}
		g := groups[k]
		st := g.state
		if g.count > 1 {
			st += fmt.Sprintf(" ×%d", g.count)
		}
		where := g.reason
		if len(g.nodes) > 0 {
			where = compressHosts(g.nodes)
		}
		stStyle := lipgloss.NewStyle().Foreground(cGreen)
		switch g.state {
		case "PD":
			stStyle = lipgloss.NewStyle().Foreground(cYellow)
		case "CF":
			stStyle = lipgloss.NewStyle().Foreground(cYellow).Bold(true)
		}
		out = append(out, "  "+pad(g.base, 6)+" "+pad(g.user, 7)+pad(g.part, 10)+pad(g.name, 9)+
			stStyle.Render(pad(st, 8))+padLeft(g.time, 6)+"  "+where)
	}
	return out
}

// splitArray turns "1001_7" into ("1001", 1) and "1001_[8-23%4]" into ("1001", 16).
func splitArray(id string) (string, int) {
	base, rest, ok := strings.Cut(id, "_")
	if !ok {
		return id, 1
	}
	if !strings.HasPrefix(rest, "[") {
		return base, 1
	}
	rest = strings.Trim(rest, "[]")
	rest, _, _ = strings.Cut(rest, "%")
	n := 0
	for _, r := range strings.Split(rest, ",") {
		a, b, isRange := strings.Cut(r, "-")
		if !isRange {
			n++
			continue
		}
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		n += y - x + 1
	}
	return base, max(n, 1)
}

// compressHosts folds ["compute01","compute02","compute04"] into
// "compute[01-02,04]". Inputs that are already hostlists pass through.
func compressHosts(names []string) string {
	type key struct {
		prefix string
		width  int
	}
	nums := map[key][]int{}
	var keys []key
	var other []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		p, d := splitDigits(n)
		if d == "" || strings.ContainsAny(n, "[,") {
			other = append(other, n)
			continue
		}
		// zero-padded names (compute01) keep their width; unpadded ones
		// (aws-burst-0 … aws-burst-10) share a group with width 0
		w := 0
		if len(d) > 1 && d[0] == '0' {
			w = len(d)
		}
		k := key{p, w}
		if _, ok := nums[k]; !ok {
			keys = append(keys, k)
		}
		v, _ := strconv.Atoi(d)
		nums[k] = append(nums[k], v)
	}
	var parts []string
	for _, k := range keys {
		vs := nums[k]
		sort.Ints(vs)
		if len(vs) == 1 {
			parts = append(parts, fmt.Sprintf("%s%0*d", k.prefix, k.width, vs[0]))
			continue
		}
		var rs []string
		for i := 0; i < len(vs); {
			j := i
			for j+1 < len(vs) && vs[j+1] == vs[j]+1 {
				j++
			}
			if i == j {
				rs = append(rs, fmt.Sprintf("%0*d", k.width, vs[i]))
			} else {
				rs = append(rs, fmt.Sprintf("%0*d-%0*d", k.width, vs[i], k.width, vs[j]))
			}
			i = j + 1
		}
		parts = append(parts, k.prefix+"["+strings.Join(rs, ",")+"]")
	}
	return strings.Join(append(parts, other...), ",")
}

func splitDigits(s string) (string, string) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	return s[:i], s[i:]
}

// ---------------------------------------------------------------------------

func pad(s string, w int) string {
	s = runewidth.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-runewidth.StringWidth(s)))
}

func padLeft(s string, w int) string {
	s = runewidth.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-runewidth.StringWidth(s))) + s
}

// truncate shortens a possibly-styled string to w cells.
func truncate(s string, w int) string {
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

var (
	cGreen   = lipgloss.Color("10")
	cYellow  = lipgloss.Color("11")
	cMagenta = lipgloss.Color("13")
	cCyan    = lipgloss.Color("14")
	cRed     = lipgloss.Color("9")
	cBlue    = lipgloss.Color("12")
	cDim     = lipgloss.Color("8")
	cWhite   = lipgloss.Color("15")

	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sPlain  = lipgloss.NewStyle()
	sBold   = lipgloss.NewStyle().Bold(true).Foreground(cWhite)
	sHeader = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	sBorder = lipgloss.NewStyle().Foreground(cDim)
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cCyan)
	sBar    = lipgloss.NewStyle().Reverse(true)
	sErr    = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	sFlash  = lipgloss.NewStyle().Reverse(true).Bold(true)
)

// ec2Cell returns glyph+state text and its style. Glyphs back up colour for
// projectors that wash it out and for colour-blind viewers.
func ec2Cell(s string) (string, lipgloss.Style) {
	st := lipgloss.NewStyle()
	switch s {
	case state.EC2Running:
		return "● running", st.Foreground(cGreen)
	case state.EC2Pending:
		return "◐ pending", st.Foreground(cYellow).Bold(true)
	case state.EC2Stopping, state.EC2ShuttingDown:
		label := "◑ stopping"
		if s == state.EC2ShuttingDown {
			label = "◑ terminating"
		}
		return label, st.Foreground(cMagenta).Bold(true)
	case state.EC2Stopped:
		return "○ stopped", sDim
	case state.EC2Terminated:
		return "○ terminated", sDim
	case state.EC2None:
		return "", sDim
	}
	return "? " + s, st
}

// slurmCell turns a raw scontrol State ("IDLE+CLOUD+POWERED_DOWN") into the
// words an audience can read. Slurm's own sinfo suffixes are ~ (powered down),
// # (powering up), % (powering down), * (not responding).
func slurmCell(n state.Node, suspend time.Duration, now time.Time) (string, lipgloss.Style) {
	st := lipgloss.NewStyle()
	if n.Role == state.RoleHead {
		return "controller", st.Foreground(cBlue)
	}
	if n.SlurmState == "" {
		return "unknown", sDim
	}
	parts := strings.Split(n.SlurmState, "+")
	base, flags := parts[0], map[string]bool{}
	for _, f := range parts[1:] {
		flags[f] = true
	}
	switch {
	case flags["POWERING_DOWN"] || flags["POWER_DOWN"]:
		return "powering down", st.Foreground(cMagenta).Bold(true)
	case flags["POWERING_UP"]:
		return "powering up", st.Foreground(cYellow).Bold(true)
	case flags["POWERED_DOWN"]:
		// A powered-down CLOUD node has no instance at all, so show nothing
		// ("powered off" is Slurm's word, and suggests a machine exists).
		// On-prem nodes in power save really are switched off.
		if n.Role == state.RoleBurst {
			return "", sDim
		}
		return "powered off", sDim
	case flags["NOT_RESPONDING"]:
		return strings.ToLower(base) + "*", st.Foreground(cRed)
	case flags["DRAIN"] || base == "DRAINED" || base == "DRAINING":
		return "drain", st.Foreground(cRed)
	}
	switch base {
	case "ALLOCATED":
		return "alloc", st.Foreground(cGreen).Bold(true)
	case "MIXED":
		return "mix", st.Foreground(cGreen)
	case "COMPLETING":
		return "completing", st.Foreground(cGreen)
	case "DOWN":
		return "down", st.Foreground(cRed).Bold(true)
	case "IDLE":
		if n.Role == state.RoleBurst && suspend > 0 && !n.LastBusy.IsZero() {
			left := n.LastBusy.Add(suspend).Sub(now)
			if left < 0 {
				left = 0
			}
			return "idle ⏲ " + clock(left), st.Foreground(cCyan).Bold(true)
		}
		return "idle", st.Foreground(cCyan)
	}
	return strings.ToLower(base), st
}

func loadBar(n state.Node, width int) string {
	if n.CPUTot == 0 || (n.Role == state.RoleBurst && n.EC2State != state.EC2Running) {
		return strings.Repeat(" ", width)
	}
	frac := n.CPULoad / float64(n.CPUTot)
	if frac > 1 {
		frac = 1
	}
	if frac < 0 {
		frac = 0
	}
	full := int(frac*float64(width) + 0.5)
	c := cGreen
	switch {
	case frac >= 0.9:
		c = cRed
	case frac >= 0.6:
		c = cYellow
	}
	return lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("█", full)) +
		sDim.Render(strings.Repeat("░", width-full))
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%dh%02d", s/3600, (s%3600)/60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

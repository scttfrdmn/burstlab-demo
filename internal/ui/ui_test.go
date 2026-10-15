package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/scttfrdmn/burstlab-demo/internal/source"
	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

func TestSplitArray(t *testing.T) {
	cases := map[string]struct {
		base string
		n    int
	}{
		"1001":           {"1001", 1},
		"1001_7":         {"1001", 1},
		"1001_[8-23]":    {"1001", 16},
		"1001_[1,3,5-6]": {"1001", 4},
		"1001_[0-99%4]":  {"1001", 100},
	}
	for in, want := range cases {
		b, n := splitArray(in)
		if b != want.base || n != want.n {
			t.Errorf("splitArray(%q) = %q,%d want %q,%d", in, b, n, want.base, want.n)
		}
	}
}

func TestCompressHosts(t *testing.T) {
	cases := map[string][]string{
		"compute[01-04]":     {"compute01", "compute02", "compute03", "compute04", "compute01"},
		"compute[01-02,04]":  {"compute04", "compute01", "compute02"},
		"aws-burst-0":        {"aws-burst-0"},
		"aws-burst-[0-3,10]": {"aws-burst-0", "aws-burst-1", "aws-burst-2", "aws-burst-3", "aws-burst-10"},
		"aws-burst-[0-1]":    {"aws-burst-[0-1]"},
	}
	for want, in := range cases {
		if got := compressHosts(in); got != want {
			t.Errorf("compressHosts(%v) = %q want %q", in, got, want)
		}
	}
}

func TestSlurmLabels(t *testing.T) {
	now := time.Now()
	cases := map[string]string{
		"IDLE+CLOUD+POWERED_DOWN":     "",
		"ALLOCATED+CLOUD+POWERING_UP": "powering up",
		"IDLE+CLOUD+POWERING_DOWN":    "powering down",
		"MIXED":                       "mix",
		"IDLE+DRAIN":                  "drain",
		"DOWN+CLOUD+NOT_RESPONDING":   "down*",
	}
	for raw, want := range cases {
		got, _ := slurmCell(state.Node{SlurmState: raw, Role: state.RoleBurst}, 0, now)
		if got != want {
			t.Errorf("%s → %q want %q", raw, got, want)
		}
	}
	idle := state.Node{SlurmState: "IDLE+CLOUD", Role: state.RoleBurst, LastBusy: now.Add(-50 * time.Second)}
	if got, _ := slurmCell(idle, 650*time.Second, now); got != "idle ⏲ 10:00" {
		t.Errorf("countdown = %q", got)
	}
}

// Every line of the dashboard must be exactly the terminal width at the
// presenter's 140 columns, at every point in the simulated demo.
func TestViewWidthAcrossTimeline(t *testing.T) {
	sim := source.NewSim(1)
	sim.TogglePause()
	m := New(sim)
	m.width, m.height = 140, 14
	for step := 0; step < 60; step++ {
		m.snap = sim.Snapshot()
		lines := strings.Split(m.View(), "\n")
		if len(lines) != 14 {
			t.Fatalf("step %d: %d lines, want 14", step, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != 140 {
				t.Fatalf("step %d line %d width %d: %q", step, i, w, l)
			}
		}
		sim.Skip(10)
	}
}

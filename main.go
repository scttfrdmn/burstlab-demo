// burstlab-demo — a live dashboard for BurstLab cloud-bursting demos.
//
//	burstlab-demo [up]        tmux layout: dashboard on top, ssh shell below
//	burstlab-demo dash        just the dashboard (in the current terminal)
//	burstlab-demo push-jobs   install the demo job scripts in ~alice/demo
//
// Add --sim to any of up/dash to run the built-in simulated demo instead.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/scttfrdmn/burstlab-demo/internal/launch"
	"github.com/scttfrdmn/burstlab-demo/internal/source"
	"github.com/scttfrdmn/burstlab-demo/internal/state"
	"github.com/scttfrdmn/burstlab-demo/internal/ui"
	"github.com/scttfrdmn/burstlab-demo/jobs"
)

type opts struct {
	cluster, region, profile, host, user, key string
	sim                                       bool
	speed                                     float64
	session                                   string
	fresh                                     bool
	dashHeight                                int
}

func (o *opts) register(fs *flag.FlagSet) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-west-2"
	}
	fs.StringVar(&o.cluster, "cluster", "burstlab-gen2", "BurstLab cluster_name (Terraform variable)")
	fs.StringVar(&o.region, "region", region, "AWS region")
	fs.StringVar(&o.profile, "profile", os.Getenv("AWS_PROFILE"), "AWS profile (default $AWS_PROFILE)")
	fs.StringVar(&o.host, "host", "", "head node address (default: discovered from EC2 tags)")
	fs.StringVar(&o.user, "user", "alice", "ssh user on the head node")
	fs.StringVar(&o.key, "key", "", "ssh private key (default: first of "+strings.Join(defaultKeys, ", ")+" that exists)")
	fs.BoolVar(&o.sim, "sim", false, "run the simulated demo (no AWS, no cluster)")
	fs.Float64Var(&o.speed, "speed", 1, "simulator speed multiplier")
	fs.StringVar(&o.session, "session", "burstlab", "tmux session name")
	fs.BoolVar(&o.fresh, "fresh", false, "replace an existing tmux session")
	fs.IntVar(&o.dashHeight, "dash-height", 14, "dashboard rows (14 fits 10 burst slots)")
}

func main() {
	cmd := "up"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var o opts
	fset := flag.NewFlagSet("burstlab-demo "+cmd, flag.ExitOnError)
	o.register(fset)
	fset.Parse(args)
	o.key = findKey(o.key)

	var err error
	switch cmd {
	case "up":
		err = up(o)
	case "dash":
		err = dash(o)
	case "push-jobs":
		err = pushJobs(o)
	case "help", "-h", "--help":
		fmt.Println("usage: burstlab-demo [up|dash|push-jobs] [flags]")
		fset.PrintDefaults()
	default:
		err = fmt.Errorf("unknown command %q (want up, dash or push-jobs)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "burstlab-demo:", err)
		os.Exit(1)
	}
}

func resolveHost(ctx context.Context, o *opts) (*source.EC2, error) {
	e, err := source.NewEC2(ctx, o.cluster, o.region, o.profile)
	if err != nil {
		return nil, err
	}
	if o.host == "" {
		if o.host, err = e.HeadPublicIP(ctx); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func up(o opts) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Pass everything explicitly: panes are started by the tmux server, which
	// may predate this shell and not have its environment (e.g. AWS_PROFILE).
	dashArgs := []string{exe, "dash", "--cluster", o.cluster, "--region", o.region,
		"--profile", o.profile, "--user", o.user, "--key", o.key}
	var shell string
	if o.sim {
		dashArgs = append(dashArgs, "--sim", "--speed", fmt.Sprint(o.speed))
		shell = `printf '\033[2mSimulator mode — this is a local shell; the dashboard is replaying a scripted demo.\033[0m\n'; exec "${SHELL:-/bin/sh}" -l`
	} else {
		if _, err := resolveHost(context.Background(), &o); err != nil {
			return err
		}
		dashArgs = append(dashArgs, "--host", o.host)
		ssh := launch.Quote(append([]string{"ssh", "-t"}, source.SSH{Host: o.host, User: o.user, Key: o.key}.Args()...)...)
		// After logout, drop to a local shell rather than closing the pane;
		// re-run the ssh with the up-arrow.
		shell = ssh + `; printf '\n\033[2m(ssh session ended — local shell)\033[0m\n'; exec "${SHELL:-/bin/sh}" -l`
	}
	return launch.Run(launch.Layout{Session: o.session, DashArgs: dashArgs, ShellCmd: shell,
		DashHeight: o.dashHeight, Fresh: o.fresh})
}

func dash(o opts) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var src state.Source
	if o.sim {
		src = source.NewSim(o.speed)
	} else {
		e, err := resolveHost(ctx, &o)
		if err != nil {
			return err
		}
		src = source.NewLive(ctx, source.SSH{Host: o.host, User: o.user, Key: o.key}, e)
	}
	_, err := tea.NewProgram(ui.New(src), tea.WithAltScreen()).Run()
	return err
}

// aliceKeySetup gives alice an intra-cluster ssh key so the dashboard probe
// can sample CPU usage on every node from the head node. /home is shared EFS,
// so this one key works on compute and burst nodes alike. Idempotent.
const aliceKeySetup = `mkdir -p ~/.ssh && chmod 700 ~/.ssh &&
[ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -C 'alice intra-cluster (burstlab-demo)' -f ~/.ssh/id_ed25519
grep -qxF "$(cat ~/.ssh/id_ed25519.pub)" ~/.ssh/authorized_keys 2>/dev/null || cat ~/.ssh/id_ed25519.pub >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys && echo "intra-cluster ssh key: ok"`

// pushJobs streams the embedded job scripts to ~/demo on the head node.
func pushJobs(o opts) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := resolveHost(ctx, &o); err != nil {
		return err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := fs.WalkDir(jobs.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := jobs.FS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o755, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		return err
	}
	tw.Close()
	args := append(source.SSH{Host: o.host, User: o.user, Key: o.key}.Args(),
		"mkdir -p ~/demo/out && tar -xf - -C ~/demo && ls -l ~/demo && "+aliceKeySetup)
	c := exec.CommandContext(ctx, "ssh", args...)
	c.Stdin, c.Stdout, c.Stderr = &buf, os.Stdout, os.Stderr
	return c.Run()
}

// defaultKeys are tried in order when --key isn't given.
var defaultKeys = []string{"~/.ssh/burstlab-demo-key.pem", "~/.ssh/burstlab-key.pem"}

func findKey(flagVal string) string {
	if flagVal != "" {
		return expandHome(flagVal)
	}
	for _, k := range defaultKeys {
		if p := expandHome(k); fileExists(p) {
			return p
		}
	}
	return "" // fall back to ssh-agent / ~/.ssh/config
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

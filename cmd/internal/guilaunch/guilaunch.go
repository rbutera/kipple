// Package guilaunch builds the GUI's view-model for this process: it runs
// the same launch steps as the CLI (the root refusal, home resolution and
// the KIPPLE_HOME guard, config, receipt store, scan and apply options) and
// turns any refusal into the model's refusal screen. It imports no GUI
// toolkit, so the default build and its tests can use it.
package guilaunch

import (
	"errors"

	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/rules"
)

// Options is the process environment.
type Options struct {
	Runtime launch.Runtime
	Geteuid func() int
	Version string
	// Procs answers owner and agent checks (procs.New() when nil).
	Procs procs.Inspector
	// CommandLineTools reports whether Apple's Command Line Tools are
	// installed (launch.CommandLineTools when nil).
	CommandLineTools func() bool
}

// Model returns the GUI's model: on the start screen, or on a refusal.
func Model(o Options) *guimodel.Model {
	cfg := guimodel.Config{Version: o.Version}
	if err := policy.CheckNotRoot(o.Geteuid()); err != nil {
		return guimodel.Refuse(cfg, guimodel.ProblemRoot, err)
	}
	h, err := launch.Resolve(o.Runtime)
	if err != nil {
		return guimodel.Refuse(cfg, problemKind(o, err), err)
	}
	store, err := launch.Store(o.Runtime, h)
	if err != nil {
		return guimodel.Refuse(cfg, guimodel.ProblemState, err)
	}
	clt := o.CommandLineTools
	if clt == nil {
		clt = launch.CommandLineTools
	}
	git := clt()
	cfg.Home = h.Path
	cfg.HomeLabel = output.SafeText(launch.HomeLabel(h.Path, h.FromEnv))
	cfg.TestHome = h.FromEnv
	cfg.GitAvailable = git
	// Level safe, whatever the config file says: the GUI pre-selects
	// exactly what kipple scan --level safe selects.
	cfg.Scan = h.ScanOptions(rules.LevelSafe, o.Version)
	cfg.Apply = h.ApplyOptions(store, o.Version, o.Procs, nil)
	cfg.Store = store
	if !git {
		cfg.Scan.Git = launch.NoGit{}
		cfg.Apply.Git = launch.NoGit{}
	}
	return guimodel.New(cfg)
}

func problemKind(o Options, err error) guimodel.ProblemKind {
	var le *launch.Error
	if errors.As(err, &le) && le.Code == launch.CodeConfig {
		return guimodel.ProblemConfig
	}
	if o.Runtime.Getenv != nil && o.Runtime.Getenv("KIPPLE_HOME") != "" {
		return guimodel.ProblemHomeRefused
	}
	return guimodel.ProblemHomeUnreadable
}

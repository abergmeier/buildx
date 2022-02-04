package commands

import (
	"context"
	"time"

	"github.com/docker/buildx/store"
	"github.com/docker/buildx/store/storeutil"
	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

type RmOptions struct {
	Builder     string
	KeepState   bool
	KeepDaemon  bool
	AllInactive bool
	Force       bool
}

const (
	rmInactiveWarning = `WARNING! This will remove all builders that are not in running state. Are you sure you want to continue?`
)

func runRm(dockerCli command.Cli, in RmOptions) error {
	ctx := appcontext.Context()

	if in.AllInactive && !in.Force && !command.PromptForConfirmation(dockerCli.In(), dockerCli.Out(), rmInactiveWarning) {
		return nil
	}

	txn, release, err := storeutil.GetStore(dockerCli)
	if err != nil {
		return err
	}
	defer release()

	if in.AllInactive {
		return rmAllInactive(ctx, txn, dockerCli, in)
	}

	if in.Builder != "" {
		ng, err := storeutil.GetNodeGroup(txn, dockerCli, in.Builder)
		if err != nil {
			return err
		}
		err1 := Rm(ctx, dockerCli, in, ng)
		if err := txn.Remove(ng.Name); err != nil {
			return err
		}
		return err1
	}

	ng, err := storeutil.GetCurrentInstance(txn, dockerCli)
	if err != nil {
		return err
	}
	if ng != nil {
		err1 := Rm(ctx, dockerCli, in, ng)
		if err := txn.Remove(ng.Name); err != nil {
			return err
		}
		return err1
	}

	return nil
}

func rmCmd(dockerCli command.Cli, rootOpts *rootOptions) *cobra.Command {
	var options RmOptions

	cmd := &cobra.Command{
		Use:   "rm [NAME]",
		Short: "Remove a builder instance",
		Args:  cli.RequiresMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.Builder = rootOpts.builder
			if len(args) > 0 {
				if options.AllInactive {
					return errors.New("cannot specify builder name when --all-inactive is set")
				}
				options.Builder = args[0]
			}
			return runRm(dockerCli, options)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&options.KeepState, "keep-state", false, "Keep BuildKit state")
	flags.BoolVar(&options.KeepDaemon, "keep-daemon", false, "Keep the buildkitd daemon running")
	flags.BoolVar(&options.AllInactive, "all-inactive", false, "Remove all inactive builders")
	flags.BoolVarP(&options.Force, "force", "f", false, "Do not prompt for confirmation")

	return cmd
}

func Rm(ctx context.Context, dockerCli command.Cli, in RmOptions, ng *store.NodeGroup) error {
	dis, err := driversForNodeGroup(ctx, dockerCli, ng, "")
	if err != nil {
		return err
	}
	for _, di := range dis {
		if di.Driver == nil {
			continue
		}
		// Do not stop the buildkitd daemon when --keep-daemon is provided
		if !in.KeepDaemon {
			if err := di.Driver.Stop(ctx, true); err != nil {
				return err
			}
		}
		if err := di.Driver.Rm(ctx, true, !in.KeepState, !in.KeepDaemon); err != nil {
			return err
		}
		if di.Err != nil {
			err = di.Err
		}
	}
	return err
}

func rmAllInactive(ctx context.Context, txn *store.Txn, dockerCli command.Cli, in RmOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	ll, err := txn.List()
	if err != nil {
		return err
	}

	builders := make([]*Nginfo, len(ll))
	for i, ng := range ll {
		builders[i] = &Nginfo{Ng: ng}
	}

	eg, _ := errgroup.WithContext(ctx)
	for _, b := range builders {
		func(b *Nginfo) {
			eg.Go(func() error {
				if err := LoadNodeGroupData(ctx, dockerCli, b); err != nil {
					return errors.Wrapf(err, "cannot load %s", b.Ng.Name)
				}
				if b.Ng.Dynamic {
					return nil
				}
				if b.inactive() {
					rmerr := Rm(ctx, dockerCli, in, b.Ng)
					if err := txn.Remove(b.Ng.Name); err != nil {
						return err
					}
					return rmerr
				}
				return nil
			})
		}(b)
	}

	return eg.Wait()
}

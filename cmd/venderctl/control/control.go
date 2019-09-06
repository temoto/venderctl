package control

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/juju/errors"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
)

const cmdUsage = "{MACHINE-ID | all} {report | ping | set-inventory | get-config | set-config | exec SCENARIO | lock DURATION}"

var Cmd = cli.Cmd{
	Name:   "control",
	Desc:   "send commands to vending machines",
	Usage:  cmdUsage,
	Action: Main,
}

func Main(ctx context.Context, flags *flag.FlagSet) error {
	const argOffset = 1 // Arg(0)=.Name
	var targetId int32
	targetAll := false
	target := flags.Arg(argOffset)
	cmd := flags.Arg(argOffset + 1)
	g := state.GetGlobal(ctx)
	g.Log.Debugf("target=%s cmd=%s", target, cmd)
	if target == "" {
		flags.Usage()
		os.Exit(1)
	}

	configPath := flags.Lookup("config").Value.String()
	config := state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	config.Tele.Enable = true
	g.MustInit(ctx, config)
	g.Log.Debugf("config=%+v", g.Config)

	switch target {
	case "all":
		targetAll = true

	default:
		if x, err := strconv.ParseInt(target, 10, 32); err != nil {
			return errors.Annotatef(err, "invalid target=%s", target)
		} else {
			targetId = int32(x)
		}
	}
	if targetId == 0 && !targetAll {
		return fmt.Errorf("invalid target=%s", target)
	}
	switch cmd {
	case "report":
		log.Fatal("TODO send cmd=report, show response")
		return nil

	case "ping":
		log.Fatal("TODO send cmd=ping, show response")
		return nil

	case "set-inventory":
		log.Fatal("TODO send set-inventory, show response")
		return nil

	case "get-config":
		// cli.StringFlag{Name: "name", Required: true},
		return nil
	case "set-config":
		// cli.StringFlag{Name: "name", Required: true},
		// cli.StringFlag{Name: "file", Required: true},
		return nil

	case "exec":
		scenario := flags.Arg(argOffset + 2)
		log.Fatalf("TODO send cmd=exec(%s), show response", scenario)
		return nil

	case "lock":
		durationString := flags.Arg(argOffset + 2)
		duration, err := time.ParseDuration(durationString)
		if err != nil {
			return errors.Annotatef(err, "invalid lock duration=%s", durationString)
		}
		if targetAll {
			g.Log.Infof("===============================================")
			g.Log.Infof("| WARNING! You are going to lock all machines |")
			g.Log.Infof("===============================================")
			safeDelay := 5 * time.Second
			g.Log.Infof("Press Control-C within %v to cancel", safeDelay)
			time.Sleep(safeDelay)
		}
		log.Fatalf("TODO send cmd=lock(%v), show response", duration)
		return nil

	default:
		return fmt.Errorf("unknown control command=%s", cmd)
	}
}

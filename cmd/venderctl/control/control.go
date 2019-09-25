package control

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/juju/errors"
	tele_api "github.com/temoto/vender/head/tele/api"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
)

const replyTimeout = 51 * time.Second

const cmdUsage = "MACHINE-ID {report | ping | set-inventory | get-config | set-config | exec SCENARIO... | lock DURATION}"

var Cmd = cli.Cmd{
	Name:   "control",
	Desc:   "send commands to vending machines",
	Usage:  cmdUsage,
	Action: Main,
}

func Main(ctx context.Context, flags *flag.FlagSet) error {
	const argOffset = 1 // Arg(0)=.Name
	var targetId int32
	target := flags.Arg(argOffset)
	cmd := flags.Arg(argOffset + 1)
	g := state.GetGlobal(ctx)
	g.Log.Debugf("target=%s cmd=%s", target, cmd)
	if target == "" {
		flags.Usage()
		os.Exit(1)
	}
	if x, err := strconv.ParseInt(target, 10, 32); err != nil {
		return errors.Annotatef(err, "invalid target=%s", target)
	} else {
		targetId = int32(x)
	}

	configPath := flags.Lookup("config").Value.String()
	config := state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	config.Tele.Enable = true
	config.Tele.MqttSubscribe = []string{fmt.Sprintf("vm%d/cr/+", targetId)}
	g.MustInit(ctx, config)
	g.Log.Debugf("config=%+v", g.Config)

	switch cmd {
	case "report":
		cmd := &tele_api.Command{
			Task: &tele_api.Command_Report{Report: &tele_api.Command_ArgReport{}},
		}
		_, err := g.Tele.CommandTx(targetId, cmd, replyTimeout)
		return err

	case "ping":
		cmd := &tele_api.Command{
			Task: &tele_api.Command_Exec{Exec: &tele_api.Command_ArgExec{
				Scenario: "",
				Lock:     false,
			}},
		}
		_, err := g.Tele.CommandTx(targetId, cmd, replyTimeout)
		return err

	case "set-inventory":
		g.Log.Fatal("TODO send set-inventory, show response")
		return nil

	case "get-config":
		// cli.StringFlag{Name: "name", Required: true},
		return nil
	case "set-config":
		// cli.StringFlag{Name: "name", Required: true},
		// cli.StringFlag{Name: "file", Required: true},
		return nil

	case "exec":
		scenario := strings.Join(flags.Args()[argOffset+2:], " ")
		cmd := &tele_api.Command{
			Task: &tele_api.Command_Exec{Exec: &tele_api.Command_ArgExec{
				Scenario: scenario,
				Lock:     true,
			}},
		}
		_, err := g.Tele.CommandTx(targetId, cmd, replyTimeout)
		return err

	case "lock":
		durationString := flags.Arg(argOffset + 2)
		duration, err := time.ParseDuration(durationString)
		if err != nil {
			return errors.Annotatef(err, "invalid lock duration=%s", durationString)
		}
		if duration < time.Second {
			return errors.Annotatef(err, "invalid lock duration=%v must be >= 1s", duration)
		}
		sec := int32(duration / time.Second)
		if time.Duration(sec)*time.Second != duration {
			sec++
		}
		g.Log.Infof("duration=%v rounded up to %d seconds", duration, sec)
		cmd := &tele_api.Command{
			Task: &tele_api.Command_Lock{Lock: &tele_api.Command_ArgLock{Duration: sec}},
		}
		_, err = g.Tele.CommandTx(targetId, cmd, replyTimeout+duration)
		return err

	default:
		return fmt.Errorf("unknown control command=%s", cmd)
	}
}

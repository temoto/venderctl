package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/juju/errors"
	"github.com/temoto/vender/log2"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/cmd/venderctl/control"
	"github.com/temoto/venderctl/cmd/venderctl/sponge"
	"github.com/temoto/venderctl/internal/state"
)

var log = log2.NewStderr(log2.LDebug)
var commands = []cli.Cmd{
	control.Cmd,
	sponge.Cmd,
}

func main() {
	errors.SetSourceTrimPrefix(os.Getenv("source_trim_prefix"))
	log.SetFlags(0)

	flags := flag.NewFlagSet("venderctl", flag.ContinueOnError)
	flags.String("config", "venderctl.hcl", "")
	flags.Usage = func() {
		usage := "Usage: venderctl [global options] command [options]\n"
		usage += "\nGlobal options:\n"
		fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
		cmds := "\nCommands:\n"
		for i, c := range commands {
			cmds += fmt.Sprintf("  %s\t%s\n", c.Name, c.Desc)
			if c.Usage != "" {
				cmds += fmt.Sprintf("  %s\t%s\n", strings.Repeat(" ", len(c.Name)), c.Usage)
			}
			if i != len(commands)-1 {
				cmds += "\n"
			}
		}
		fmt.Fprint(flags.Output(), cmds)
	}

	err := flags.Parse(os.Args[1:])
	if err != nil {
		if err == flag.ErrHelp {
			flags.Usage()
			os.Exit(0)
		}
		log.Fatal(errors.ErrorStack(err))
	}

	cmdName := flags.Arg(0)
	if cmdName == "" {
		flags.Usage()
		os.Exit(1)
	}
	if cli.IsHelp(cmdName) {
		flags.Usage()
		os.Exit(0)
	}
	// log.Printf("command='%s'", cmdName)
	for _, c := range commands {
		if c.Name == cmdName {
			flags.Usage = func() {
				usage := fmt.Sprintf("Usage: venderctl [global options] %s %s\n", cmdName, c.Usage)
				usage += "\nGlobal options:\n"
				fmt.Fprint(flags.Output(), usage)
				flags.PrintDefaults()
			}
			if cli.IsHelp(flags.Arg(2)) {
				flags.Usage()
				os.Exit(0)
			}

			ctx, _ := state.NewContext(cmdName, log)

			if cli.SdNotify("start " + cmdName) {
				// under systemd assume systemd journal logging, no timestamp
				log.SetFlags(log2.LServiceFlags)
			} else {
				log.SetFlags(log2.LInteractiveFlags)
			}
			// log.Debugf("starting %s", cmdName)

			err := c.Action(ctx, flags)
			if err != nil {
				log.Fatal(errors.ErrorStack(err))
			}
			os.Exit(0) // success path
		}
	}
	// unknown command
	log.Errorf("unknown command=%s", cmdName)
	flags.Usage()
	os.Exit(1)
}

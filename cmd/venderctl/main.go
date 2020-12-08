package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" //#nosec G108
	"os"
	"strings"

	"github.com/juju/errors"
	"github.com/temoto/vender/log2"
	"github.com/temoto/venderctl/cmd/internal/cli"
	cmd_control "github.com/temoto/venderctl/cmd/venderctl/control"
	cmd_passwd "github.com/temoto/venderctl/cmd/venderctl/passwd"
	cmd_tax "github.com/temoto/venderctl/cmd/venderctl/tax"
	cmd_tele "github.com/temoto/venderctl/cmd/venderctl/tele"
	cmd_sponge "github.com/temoto/venderctl/cmd/venderctl/sponge"
	"github.com/temoto/venderctl/internal/state"
	state_new "github.com/temoto/venderctl/internal/state/new"
	"github.com/temoto/venderctl/internal/tele"
)

var log = log2.NewStderr(log2.LDebug)
var commands = []cli.Cmd{
	cmd_control.Cmd,
	cmd_passwd.Cmd,
	cmd_tax.Cmd,
	cmd_tele.Cmd,
	cmd_sponge.Cmd,
	{Name: "version", Action: versionMain},
}

var BuildVersion string = "unknown" // set by ldflags -X

func main() {
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
		if err == flag.ErrHelp { // usage is already printed
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

			ctx, g := state_new.NewContext(cmdName, log, tele.NewTele())
			g.BuildVersion = BuildVersion

			if cli.SdNotify("start " + cmdName) {
				// under systemd assume systemd journal logging, no timestamp
				log.SetFlags(log2.LServiceFlags)
			} else {
				log.SetFlags(log2.LInteractiveFlags)
			}
			if c.Name != "version" {
				// Sad difference with vender code: config is read inside cmd.Action, not available here
				// Options considered:
				// - avoid config to environ
				// - duplicate pprofStart code in actions
				// - cmd.PreAction hook -- maybe best in the long run, needed quick
				g.Error(pprofStart(g, os.Getenv("pprof_listen")))
				log.Infof("venderctl version=%s starting %s", BuildVersion, cmdName)
			}

			err := c.Action(ctx, flags)
			if err != nil {
				log.Fatalf("%s\nTrace:\n%s", err.Error(), errors.ErrorStack(err))
			}
			os.Exit(0) // success path
		}
	}
	// unknown command
	log.Errorf("unknown command=%s", cmdName)
	flags.Usage()
	os.Exit(1)
}

func pprofStart(g *state.Global, addr string) error {
	if addr == "" {
		return nil
	}

	srv := &http.Server{Addr: addr, Handler: nil} // TODO specific pprof handler
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Annotate(err, "pprof")
	}
	g.Log.Debugf("pprof http://%s/debug/pprof/", ln.Addr().String())
	go pprofServe(g, srv, ln)
	return nil

}

// not inline only for clear goroutine source in panic trace
func pprofServe(g *state.Global, srv *http.Server, ln net.Listener) { g.Error(srv.Serve(ln)) }

func versionMain(ctx context.Context, flags *flag.FlagSet) error {
	fmt.Printf("venderctl %s\n", BuildVersion)
	return nil
}

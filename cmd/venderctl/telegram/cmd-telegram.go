package telegram

import (
	"context"
	"flag"
	"fmt"

	"github.com/coreos/go-systemd/daemon"
	"github.com/juju/errors"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
)

const CmdName = "telegram"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "telegram bot. control vmc via telegram bot",
	Action: telegramMain,
}

func telegramMain(ctx context.Context, flags *flag.FlagSet) error {
	g := state.GetGlobal(ctx)
	g.InitVMC()
	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("telegram")
	// g.Vmc = make(map[int32]bool)
	// g.Vmc[1] = true

	if err := telegramInit(ctx); err != nil {
		return errors.Annotate(err, "telegramInit")
	}
	// return telegramLoop(ctx)
	return nil

}

func telegramInit(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	// if err := g.InitDB(CmdName); err != nil {
	// 	return errors.Annotate(err, "db init")
	// }

	if err := g.Tele.Init(ctx, g.Log, g.Config.Tele); err != nil {
		return errors.Annotate(err, "Tele.Init")
	}

	// if err := g.Telegram.Init(ctx, g.Log, g.Config.Tele); err != nil {
	// 	return errors.Annotate(err, "Telegram.Init")
	// }

	cli.SdNotify(daemon.SdNotifyReady)
	g.Log.Debugf("telegram init complete")
	return telegramLoop(ctx)

}

func telegramLoop(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	ch := g.Tele.Chan()
	stopch := g.Alive.StopChan()

	for {
		select {
		case p := <-ch:
			g.Log.Debugf("tele packet=%s", p.String())

			g.Alive.Add(1)
			err := onPacket(ctx, p)
			g.Alive.Done()
			if err != nil {
				g.Log.Error(errors.ErrorStack(err))
			}

		case <-stopch:
			return nil
		}
	}

}

func onPacket(ctx context.Context, p tele_api.Packet) error {
	g := state.GetGlobal(ctx)
	vmcid := p.VmId
	r := g.Vmc[vmcid]
	switch p.Kind {
	case tele_api.PacketConnect:
		c := false
		if p.Payload[0] == 1 {
			c = true
		}
		r.Connect = c

	case tele_api.PacketState:
		s, err := p.State()
		if err != nil {
			return err
		}
		r.State = s

	default:
		return errors.Errorf("code error invalid packet=%v", p)
	}
	g.Vmc[vmcid] = r
	fmt.Printf("\n\033[41m (%v) \033[0m\n\n", g.Vmc)
	return nil
}

// func onConnect(ctx context.Context, vmid int32, connect bool) error {
// 	g := state.GetGlobal(ctx)
// 	return nil
// }

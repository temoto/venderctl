package sponge

import (
	"context"

	"github.com/coreos/go-systemd/daemon"
	"github.com/juju/errors"
	tele_types "github.com/temoto/vender/head/tele"
	"github.com/temoto/venderctl/internal/state"
	"github.com/temoto/venderctl/internal/subcmd"
	"github.com/temoto/venderctl/internal/tele"
)

var Mod = subcmd.Mod{Name: "sponge", Main: Main}

func Main(ctx context.Context, config *state.Config) error {
	g := state.GetGlobal(ctx)
	config.Tele.Enable = true
	g.MustInit(ctx, config)
	g.Log.Debugf("config=%+v", g.Config)

	subcmd.SdNotify(daemon.SdNotifyReady)
	g.Log.Debugf("sponge init complete")

	app := &app{g: g}
	return app.loop(ctx)
}

// runtime irrelevant in Global
type app struct {
	g  *state.Global
	db string
}

func (app *app) loop(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	ch := g.Tele.Chan()
	stopch := g.Alive.StopChan()

	for {
		select {
		case p := <-ch:
			app.g.Log.Debugf("tele packet=%s", p.String())

			g.Alive.Add(1)
			err := app.onPacket(ctx, p)
			g.Alive.Done()
			if err != nil {
				g.Log.Error(errors.ErrorStack(err))
			}

		case <-stopch:
			return nil
		}
	}
}

func (app *app) onPacket(ctx context.Context, p tele.Packet) error {
	switch p.Kind {
	case tele.PacketState:
		s, err := p.State()
		if err != nil {
			return err
		}
		return app.onState(ctx, p.VmId, s)

	case tele.PacketTelemetry:
		t, err := p.Telemetry()
		if err != nil {
			return err
		}
		return app.onTelemetry(ctx, p.VmId, t)

	default:
		return errors.Errorf("code error invalid packet=%v", p)
	}
}

func (app *app) onState(ctx context.Context, vmid int32, state tele_types.State) error {
	app.g.Log.Infof("vm=%d state=%s", vmid, state.String())
	return nil
}

func (app *app) onTelemetry(ctx context.Context, vmid int32, t *tele_types.Telemetry) error {
	app.g.Log.Infof("vm=%d telemetry=%s", vmid, t.String())
	return nil
}

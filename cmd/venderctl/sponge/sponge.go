// Sponge job is to listen network for incoming telemetry and save into database.
package sponge

import (
	"context"
	"flag"
	"strconv"

	"github.com/coreos/go-systemd/daemon"
	"github.com/go-pg/pg"
	pg_types "github.com/go-pg/pg/types"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	tele_api "github.com/temoto/vender/head/tele/api"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
	"github.com/temoto/venderctl/internal/tele"
)

const CmdName = "sponge"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "telemetry network -> save to database",
	Action: Main,
}

func Main(ctx context.Context, flags *flag.FlagSet) error {
	g := state.GetGlobal(ctx)
	configPath := flags.Lookup("config").Value.String()
	config := state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	config.Tele.Enable = true
	config.Tele.MqttSubscribe = []string{"+/w/+"}
	g.MustInit(ctx, config)
	g.Log.Debugf("config=%+v", g.Config)

	// TODO maybe move this to g.MustInit
	dbOpt, err := pg.ParseURL(config.DB.URL)
	if err != nil {
		g.Log.Fatalf("config db.url err=%v", err)
	}
	dbOpt.MinIdleConns = 1
	dbOpt.IdleTimeout = -1
	dbOpt.IdleCheckFrequency = -1
	dbOpt.ApplicationName = "venderctl/" + CmdName
	// MaxRetries:1,
	// PoolSize:2,
	// TODO maybe move this to g.MustInit
	g.DB = pg.Connect(dbOpt)

	cli.SdNotify(daemon.SdNotifyReady)
	g.Log.Debugf("sponge init complete")

	app := &app{g: g}
	if _, err := g.DB.Exec(`select 1`); err != nil {
		g.Log.Fatal(err)
	}
	return app.loop(ctx)
}

// runtime irrelevant in Global
type app struct {
	g *state.Global
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

func (app *app) onState(ctx context.Context, vmid int32, state tele_api.State) error {
	app.g.Log.Infof("vm=%d state=%s", vmid, state.String())

	const q = `insert into state (vmid,state,received) values (?0,?1,current_timestamp)
on conflict (vmid) do update set state=excluded.state, received=excluded.received`
	_, err := app.g.DB.Exec(q, vmid, state)
	return errors.Annotatef(err, "db query=%s", q)
}

func (app *app) onTelemetry(ctx context.Context, vmid int32, t *tele_api.Telemetry) error {
	app.g.Log.Infof("vm=%d telemetry=%s", vmid, t.String())

	errs := make([]error, 0)
	if t.Error != nil {
		const q = `insert into error (vmid,vmtime,received,code,message,count) values (?0,to_timestamp(?1/1e9),current_timestamp,?2,?3,?4)`
		_, err := app.g.DB.Exec(q, vmid, t.Time, t.Error.Code, t.Error.Message, t.Error.Count)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	if t.Transaction != nil {
		const q = `insert into trans (vmid,vmtime,received,menu_code,options,price,method) values (?0,to_timestamp(?1/1e9),current_timestamp,?2,?3,?4,?5)`
		_, err := app.g.DB.Exec(q, vmid, t.Time, t.Transaction.Code, pg.Array(t.Transaction.Options), t.Transaction.Price, t.Transaction.PaymentMethod)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	if t.Inventory != nil || t.MoneyCashbox != nil {
		const q = `insert into inventory (vmid,at_service,vmtime,received,inventory,cashbox_bill,cashbox_coin,change_bill,change_coin) values (?0,?1,to_timestamp(?2/1e9),current_timestamp,?3,?4,?5,?6,?7)
on conflict (vmid) where at_service=?1 do update set
  vmtime=excluded.vmtime,received=excluded.received,inventory=excluded.inventory,
	cashbox_bill=excluded.cashbox_bill,cashbox_coin=excluded.cashbox_coin,
	change_bill=excluded.change_bill,change_coin=excluded.change_coin`
		invMap := make(map[string]string)
		var cashboxBillMap map[uint32]uint32
		var cashboxCoinMap map[uint32]uint32
		var changeBillMap map[uint32]uint32
		var changeCoinMap map[uint32]uint32
		if t.MoneyCashbox != nil {
			cashboxBillMap = t.MoneyCashbox.Bills
			cashboxCoinMap = t.MoneyCashbox.Coins
		}
		if t.MoneyChange != nil {
			changeBillMap = t.MoneyChange.Bills
			changeCoinMap = t.MoneyChange.Coins
		}
		if t.Inventory != nil {
			for _, item := range t.Inventory.Stocks {
				invMap[item.Name] = strconv.FormatInt(int64(item.Value), 10)
			}
		}
		_, err := app.g.DB.Exec(q, vmid, t.GetAtService(), t.Time, pg.Hstore(invMap),
			mapUint32ToHstore(cashboxBillMap),
			mapUint32ToHstore(cashboxCoinMap),
			mapUint32ToHstore(changeBillMap),
			mapUint32ToHstore(changeCoinMap),
		)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	const q = `insert into ingest (received,vmid,done,raw) values (current_timestamp,?0,?1,?2)`
	raw, _ := proto.Marshal(t)
	_, err := app.g.DB.Exec(q, vmid, len(errs) == 0, raw)
	if err != nil {
		errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
	}
	return helpers.FoldErrors(errs)
}

func mapUint32ToHstore(from map[uint32]uint32) *pg_types.Hstore {
	m := make(map[string]string, len(from))
	for k, v := range from {
		m[strconv.FormatInt(int64(k), 10)] = strconv.FormatInt(int64(v), 10)
	}
	return pg.Hstore(m)
}

package tele

// Tele listens network for incoming telemetry and saves into database.

import (
	"context"
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/coreos/go-systemd/daemon"
	"github.com/go-pg/pg/v9"
	pg_types "github.com/go-pg/pg/v9/types"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	"github.com/temoto/vender/helpers"
	vender_api "github.com/temoto/vender/tele"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
)

const CmdName = "tele"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "telemetry server -> save to database",
	Action: teleMain,
}

func teleMain(ctx context.Context, flags *flag.FlagSet) error {
	g := state.GetGlobal(ctx)

	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	if err := g.Config.Tele.EnableServer(); err != nil {
		return errors.Annotate(err, "tele EnableServer")
	}
	g.Log.Debugf("config=%+v", g.Config)

	if err := teleInit(ctx); err != nil {
		return errors.Annotate(err, "teleInit")
	}
	return teleLoop(ctx)
}

func teleInit(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	if err := g.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "InitDB")
	}
	if err := g.Config.Tele.EnableServer(); err != nil {
		return errors.Annotate(err, "Tele.EnableServer")
	}
	if err := g.Tele.Init(ctx, g.Log, g.Config.Tele); err != nil {
		return errors.Annotate(err, "Tele.Init")
	}

	cli.SdNotify(daemon.SdNotifyReady)
	g.Log.Debugf("tele init complete")
	return nil
}

func teleLoop(ctx context.Context) error {
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
	dbConn := g.DB.Conn()
	defer dbConn.Close()

	switch p.Kind {
	case tele_api.PacketState:
		s, err := p.State()
		if err != nil {
			return err
		}
		return onState(ctx, dbConn, p.VmId, s)

	case tele_api.PacketTelemetry:
		t, err := p.Telemetry()
		if err != nil {
			return err
		}
		return onTelemetry(ctx, dbConn, p.VmId, t)

	default:
		return errors.Errorf("code error invalid packet=%v", p)
	}
}

func onState(ctx context.Context, dbConn *pg.Conn, vmid int32, s vender_api.State) error {
	g := state.GetGlobal(ctx)
	g.Log.Infof("vm=%d state=%s", vmid, s.String())

	dbConn = dbConn.WithParam("vmid", vmid).WithParam("state", s)
	var oldState vender_api.State
	_, err := dbConn.Query(pg.Scan(&oldState), `select state_update(?vmid, ?state)`)
	err = errors.Annotatef(err, "db state_update")
	// g.Log.Infof("vm=%d old_state=%s", vmid, old_state.String())

	if g.Config.Tele.ExecOnState != "" {
		// Exec user supplied program is potential security issue.
		// TODO explore hardening options like sudo
		cmd := exec.Command(g.Config.Tele.ExecOnState) //nolint:gosec
		cmd.Env = []string{
			fmt.Sprintf("db_updated=%t", err == nil),
			fmt.Sprintf("vmid=%d", vmid),
			fmt.Sprintf("new=%d", s),
			fmt.Sprintf("prev=%d", oldState),
		}
		g.Alive.Add(1)
		go func() {
			defer g.Alive.Done()
			execOutput, execErr := cmd.CombinedOutput()
			prettyEnv := strings.Join(cmd.Env, " ")
			if execErr != nil {
				execErr = errors.Annotatef(execErr, "exec_on_state %s %s output=%s", prettyEnv, cmd.Path, execOutput)
				g.Log.Error(execErr)
			}
		}()
	}

	return err
}

func onTelemetry(ctx context.Context, dbConn *pg.Conn, vmid int32, t *vender_api.Telemetry) error {
	g := state.GetGlobal(ctx)
	g.Log.Infof("vm=%d telemetry=%s", vmid, t.String())

	dbConn = dbConn.WithParam("vmid", vmid).WithParam("vmtime", t.Time)
	errs := make([]error, 0)
	if t.Error != nil {
		const q = `insert into error (vmid,vmtime,received,app_version,code,message,count) values (?vmid,to_timestamp(?vmtime/1e9),current_timestamp,?0,?1,?2,?3)`
		_, err := dbConn.Exec(q, t.BuildVersion, t.Error.Code, t.Error.Message, t.Error.Count)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	if t.Transaction != nil {
		const q = `insert into trans (vmid,vmtime,received,menu_code,options,price,method) values (?vmid,to_timestamp(?vmtime/1e9),current_timestamp,?0,?1,?2,?3)
on conflict (vmid,vmtime) do nothing`
		_, err := dbConn.Exec(q, t.Transaction.Code, pg.Array(t.Transaction.Options), t.Transaction.Price, t.Transaction.PaymentMethod)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	if t.Inventory != nil || t.MoneyCashbox != nil {
		const q = `insert into inventory (vmid,at_service,vmtime,received,inventory,cashbox_bill,cashbox_coin,change_bill,change_coin) values (?vmid,?0,to_timestamp(?vmtime/1e9),current_timestamp,?1,?2,?3,?4,?5)
on conflict (vmid) where at_service=?0 do update set
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
				invMap[item.Name] = strconv.FormatFloat(float64(item.Valuef), 'f', -1, 32)
			}
		}
		_, err := dbConn.Exec(q, t.GetAtService(), pg.Hstore(invMap),
			mapUint32ToHstore(cashboxBillMap),
			mapUint32ToHstore(cashboxCoinMap),
			mapUint32ToHstore(changeBillMap),
			mapUint32ToHstore(changeCoinMap),
		)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	const q = `insert into ingest (received,vmid,done,raw) values (current_timestamp,?vmid,?0,?1)`
	raw, _ := proto.Marshal(t)
	_, err := dbConn.Exec(q, len(errs) == 0, raw)
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

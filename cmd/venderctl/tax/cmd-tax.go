// Sends tax information to state. Executes tax_job queue in DB.
package tax

// TODO maybe extract into separate Go module and/or code repository

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/coreos/go-systemd/daemon"
	"github.com/go-pg/pg/v9"
	"github.com/juju/errors"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
	// tele_config "github.com/temoto/venderctl/internal/tele/config"
)

const CmdName = "tax"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "execute tax jobs",
	Action: taxMain,
}

func taxMain(ctx context.Context, flags *flag.FlagSet) error {
	g := state.GetGlobal(ctx)

	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("tax")
	// if err := g.Config.Tele.EnableClient(tele_config.RoleControl); err != nil {
	// 	return err
	// }
	if err := g.Tele.Init(ctx, g.Log, g.Config.Tele); err != nil {
		return err
	}
	// g.Log.Debugf("config=%+v", g.Config)

	if err := taxInit(ctx); err != nil {
		return errors.Annotate(err, "taxInit")
	}
	return taxLoop(ctx)
}

func taxInit(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	if err := g.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "InitDB")
	}

	cli.SdNotify(daemon.SdNotifyReady)
	g.Log.Debugf("taxInit complete")
	return nil
}

func taxLoop(ctx context.Context) error {
	const pollInterval = 53 * time.Second
	g := state.GetGlobal(ctx)
	stopch := g.Alive.StopChan()
	hostname, _ := os.Hostname()
	worker := fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), rand.Int31())

	llSched := g.DB.Listen("tax_job_sched")
	defer llSched.Close()
	chSched := llSched.Channel()

	g.Alive.Add(1)
	db := g.DB.Conn()
	try, err := taxStep(ctx, db, worker)
	g.Log.Debugf("taxStep try=%t err=%v", try, err)
	_ = db.Close()
	g.Alive.Done()
	if err != nil {
		return err
	}

	for {
		if !try {
			select {
			case <-chSched:
				g.Log.Debugf("notified tax_job_sched")

			case <-time.After(pollInterval):

			case <-stopch:
				return nil
			}
		}
		g.Alive.Add(1)
		db = g.DB.Conn()
		try, err = taxStep(ctx, db, worker)
		g.Log.Debugf("taxStep try=%t err=%v", try, err)
		_ = db.Close()
		g.Alive.Done()
		if err != nil {
			g.Error(err)
		}
	}
}

type MTaxJob struct {
	Id        int64 `pg:",pk"`
	State     string
	Created   time.Time
	Modified  time.Time
	Scheduled time.Time
	Worker    string
	Processor string
	ExtId     string
	Ops       []TaxJobOp `pg:"ops"`
	Data      *TaxJobData
	Notes     []string `pg:",array"`
	Gross     int32
}

type TaxJobData struct {
	Ru2019 struct {
		OpTime  string `json:"optime,omitempty"`
		DocNum  uint32 `json:"docnum,omitempty"`
		DocType uint16 `json:"doctype,omitempty"`

		FSStatus struct {
			FSNum            string `json:"fsnum,omitempty"`
			CycleOpen        bool   `json:"cycle_open,omitempty"`
			LastDocNumber    uint32 `json:"last_doc,omitempty"`
			UnsentDocNumber  uint32 `json:"unsent_doc,omitempty"`
			OfflineDocsCount uint32 `json:"offline_docs,omitempty"`
		} `json:"fss,omitempty"`
	} `json:"ru2019,omitempty"`
}

type TaxJobOp struct {
	Time   string  `json:"time"`
	Name   string  `json:"name"`
	Code   string  `json:"code"`
	Amount float64 `json:"amount"`
	Price  uint32  `json:"price"`
	Vmid   int32   `json:"vmid"`

	Method vender_api.PaymentMethod `json:"method"`
}

func (d *TaxJobData) String() string {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Sprintf("(TaxJobData.String err=%v)", err)
	}
	return string(b)
}

func (tj *MTaxJob) OpKeysString() string {
	if tj == nil {
		return ""
	}
	b := strings.Builder{}
	for _, op := range tj.Ops {
		b.WriteString(op.KeyString())
	}
	return b.String()
}

func (tj *MTaxJob) Update(conn *pg.Conn, assign string, params ...interface{}) error {
	conn = conn.
		WithParam("tj_id", tj.Id).
		WithParam("tj_state", tj.State).
		WithParam("tj_data", tj.Data).
		WithParam("tj_extid", tj.ExtId)
	const assignDefault = "state=?tj_state,data=?tj_data,ext_id=?tj_extid"
	if assign != "" {
		assign = "," + assign
	}
	q := fmt.Sprintf("update tax_job set %s%s where id=?tj_id and worker=?worker", assignDefault, assign)
	_, err := conn.ExecOne(q, params...)
	return err
}

func (tj *MTaxJob) UpdateFinal(conn *pg.Conn, note string) error {
	tj.State = "final"
	assign := ""
	var params []interface{}
	if note != "" {
		assign = "notes=array_append(notes,?0)"
		params = append(params, note)
	}
	return tj.Update(conn, assign, params...)
}

func (tj *MTaxJob) UpdateScheduleLater(conn *pg.Conn) error {
	tj.State = "sched"
	return tj.Update(conn, "scheduled=(current_timestamp + '5 second'::interval)")
}

func (o *TaxJobOp) KeyString() string {
	return fmt.Sprintf("vm=%d,time=%s,code=%s,name=%s", o.Vmid, o.Time, o.Code, o.Name)
}

// try to take next job in queue and process it
// error during taxProcess() changes state=help and logs error into tax_job.notes
func taxStep(ctx context.Context, db *pg.Conn, worker string) (bool, error) {
	g := state.GetGlobal(ctx)
	db = db.WithParam("worker", worker)

	var tj MTaxJob
	_, err := db.QueryOne(&tj, `select * from tax_job_take(?worker)`)
	if err == pg.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, errors.Annotate(err, "tax_job_take")
	}
	g.Log.Debugf("tj=%#v data=%s", tj, tj.Data.String())
	if err = taxProcess(ctx, db, &tj); err != nil {
		tj.State = "help"
		e := tj.Update(db, "notes=array_append(notes,?0)", "error:"+errors.Details(err))
		g.Log.Error(e)
		return true, err
	}
	return true, nil
}

func taxProcess(ctx context.Context, db *pg.Conn, tj *MTaxJob) error {
	// Tax processor incapsulates actions required by some local law.
	switch tj.Processor {
	case procRu2019:
		return processRu2019(ctx, db, tj)
	default:
		return fmt.Errorf("unknown processor")
	}
}

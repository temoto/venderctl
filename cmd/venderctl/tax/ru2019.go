package tax

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-pg/pg/v9"
	"github.com/juju/errors"
	ru_nalog "github.com/temoto/ru-nalog-go"
	"github.com/temoto/ru-nalog-go/umka"
	vender_api "github.com/temoto/vender/tele"
	"github.com/temoto/venderctl/internal/state"
)

const procRu2019 = "ru2019"

func mustUmka(ctx context.Context) umka.Umker {
	g := state.GetGlobal(ctx)
	// TODO cache
	uconf := &umka.UmkaConfig{
		BaseURL: g.Config.Tax.Ru2019.Umka.BaseURL,
		RT:      g.Config.Tax.Ru2019.Umka.XXX_testRT, // set only in tests
	}
	if uconf.RT == nil {
		uconf.RT = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			ForceAttemptHTTP2:      true,
			IdleConnTimeout:        10 * time.Second,
			MaxConnsPerHost:        1,
			MaxIdleConns:           1,
			MaxResponseHeaderBytes: 16 << 10,
			ReadBufferSize:         32 << 10,
			TLSHandshakeTimeout:    10 * time.Second,
			WriteBufferSize:        32 << 10,
		}
	}
	u, err := umka.NewUmka(uconf)
	if err != nil {
		g.Error(err)
		panic("umka: " + err.Error())
	}
	return u
}

// - clean data, ops
// - check KKT/FS status
// - cashier cycle dance, irrelevant in this fully automated configuration
// - DocNum set and is sent -> success path to final
// - DocNum set and is not sent yet -> reschedule
// - create new doc, store in KKT/FS, reschedule or final (see waitSendOFD)
func processRu2019(ctx context.Context, db *pg.Conn, tj *MTaxJob) error {
	// waitSendOFD=false: set state=final after FiscalCheck success, race with OFD upload
	// waitSendOFD=true: set state=final after KKT reports doc is sent to OFD
	const waitSendOFD = false

	g := state.GetGlobal(ctx)
	if tj.Data == nil {
		tj.Data = &TaxJobData{}
	}
	if len(tj.Ops) == 0 {
		return processRu2019Final(ctx, db, tj, "error: empty data.ops")
	}
	sameMethod := tj.Ops[0].Method
	sameVmid := tj.Ops[0].Vmid
	for _, op := range tj.Ops {
		if op.Vmid != sameVmid {
			return errors.NotSupportedf("operations with different vmid")
		}
		switch op.Method {
		case vender_api.PaymentMethod_Cash:
			// continue with umka

		// TODO continue with umka, set relevant tags
		// OR skip processing because payment terminal already sent tax data
		// case vender_api.PaymentMethod_Cashless:

		case vender_api.PaymentMethod_Gift:
			return processRu2019Final(ctx, db, tj, fmt.Sprintf("skip payment method=%s", op.Method.String()))

		default:
			return errors.NotSupportedf("operation payment method=%s", op.Method.String())
		}
		if op.Method != sameMethod {
			return errors.NotSupportedf("operations with different payment method")
		}
	}

	u := mustUmka(ctx)
	if err := processRu2019StatusCycleDance(ctx, u, tj); err != nil {
		return err
	}
	if tj.Data.Ru2019.DocNum != 0 {
		if tj.ExtId == "" {
			g.Log.Debugf("%s: skip to final DocNum=%d ExtId=%s", procRu2019, tj.Data.Ru2019.DocNum, tj.ExtId)
			return errors.Errorf("existing docnum=%d but QR is empty", tj.Data.Ru2019.DocNum)
		}
		if !(tj.Data.Ru2019.FSStatus.OfflineDocsCount == 0 || tj.Data.Ru2019.FSStatus.UnsentDocNumber > tj.Data.Ru2019.DocNum) {
			g.Log.Debugf("%s: try later DocNum=%d is not sent", procRu2019, tj.Data.Ru2019.DocNum)
			return tj.UpdateScheduleLater(db)
		}
		return processRu2019Final(ctx, db, tj, "")
	}

	doc := ru_nalog.NewDoc(0, ru_nalog.FDCheck)
	doc.AppendNew(1054, 1)
	doc.AppendNew(1055, g.Config.Tax.Ru2019.Tag1055)
	// d.AppendNew(1008, "client email or SMS number")
	doc.AppendNew(1009, g.Config.Tax.Ru2019.Tag1009) // payment address
	doc.AppendNew(1187, g.Config.Tax.Ru2019.Tag1187) // payment place
	doc.AppendNew(1036, sameVmid)
	for _, op := range tj.Ops {
		stlv := doc.AppendNew(1059, nil)
		stlv.AppendNew(1023, op.Amount)
		stlv.AppendNew(1030, op.Name)
		stlv.AppendNew(1079, op.Price)
		stlv.AppendNew(1199, g.Config.Tax.Ru2019.Tag1199)
		stlv.AppendNew(1212, 1)
		stlv.AppendNew(1214, 1)
	}
	umkaSessionId := tj.OpKeysString() + time.Now().Format(time.RFC3339)
	var err error
	if doc, err = u.FiscalCheck(umkaSessionId, doc); err != nil {
		return errors.Annotatef(err, "umka/FiscalCheck")
	}
	if docNumTag := doc.FindByTag(1040); docNumTag != nil {
		tj.Data.Ru2019.DocNum = docNumTag.Uint32()
	}
	if qrTag := doc.FindByTag(1196); qrTag != nil {
		tj.ExtId = qrTag.String()
	}
	if waitSendOFD {
		g.Log.Debugf("%s: try later DocNum=%d is not sent", procRu2019, tj.Data.Ru2019.DocNum)
		return tj.UpdateScheduleLater(db)
	} else {
		return processRu2019Final(ctx, db, tj, "")
	}
}

func processRu2019Final(ctx context.Context, db *pg.Conn, tj *MTaxJob, note string) error {
	if err := tj.UpdateFinal(db, note); err != nil {
		return err
	}

	if tj.ExtId != "" && len(tj.Ops) > 0 {
		g := state.GetGlobal(ctx)
		const replyTimeout = time.Minute
		vmid := tj.Ops[0].Vmid
		cmd := &vender_api.Command{
			Deadline: time.Now().Add(2 * time.Minute).UnixNano(),
			Task: &vender_api.Command_Show_QR{Show_QR: &vender_api.Command_ArgShowQR{
				Layout: "tax-ru19",
				QrText: tj.ExtId,
			}},
		}
		if _, err := g.Tele.CommandTx(vmid, cmd); err != nil {
			g.Log.Errorf("tax/ru2019 vmid=%d command=show_QR err=%v", vmid, err)
			fmt.Printf("\033[41m ERROR show QR. Exit for restart \033[0m\n")
			panic("FIXTMP")
		}
	}
	return nil
}

func processRu2019StatusCycleDance(ctx context.Context, u umka.Umker, tj *MTaxJob) error {
	for try := 1; try <= 2; try++ {
		status, err := u.Status()
		if err != nil {
			return errors.Annotatef(err, "umka/Status")
		}
		tj.Data.Ru2019.FSStatus.OfflineDocsCount = status.OfdOfflineCount()
		tj.Data.Ru2019.FSStatus.CycleOpen = status.IsCycleOpen()
		tj.Data.Ru2019.FSStatus.LastDocNumber = uint32(status.FsStatus.LastDocNumber)
		tj.Data.Ru2019.FSStatus.UnsentDocNumber = uint32(status.FsStatus.Transport.FirstDocNumber)
		if d, err := umka.EnsureCycleValid(u, status, 24*time.Hour); err != nil {
			return errors.Annotate(err, "umka/EnsureCycleValid")
		} else if d != nil {
			// cycle (re)opened, check status again
			// FIXME probably redundant, try to get required info from d
			continue
		}
		return nil // success path, cycle was valid
	}
	return errors.Errorf("KKT cycle problem")
}

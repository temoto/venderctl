package tax

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-pg/pg/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/temoto/ru-nalog-go/umka"
	state_new "github.com/temoto/venderctl/internal/state/new"
	"github.com/temoto/venderctl/internal/toolpool"
)

func TestRu2019(t *testing.T) {
	t.Parallel()

	ctx, g := state_new.NewTestContext(t, nil, `tax { ru2019 {
tag1055 = 2
tag1199 = 6
umka { base_url="mock:" }
}}`)
	g.Config.Tax.Ru2019.Umka.XXX_testRT = &toolpool.MockHTTP{
		Fun: func(req *http.Request) (*http.Response, error) {
			t.Logf("umka < %s", req.URL.String())
			statusCode := http.StatusOK
			frame := struct {
				Protocol      int            `json:"protocol,omitempty"` // 1=JSON 3=XML
				Version       string         `json:"version,omitempty"`  // "1.0"
				CashboxStatus *umka.Status   `json:"cashboxStatus,omitempty"`
				Document      *umka.Document `json:"document,omitempty"`
			}{
				Protocol: 1,
				Version:  "1.0",
			}
			now := time.Now()
			switch req.URL.Path {
			case "/cashboxstatus.json":
				frame.CashboxStatus = &umka.Status{
					Dt:          now.Format(umka.TimeLayout),
					CycleOpened: now.Add(-5 * time.Minute).Format(umka.TimeLayout),
				}
				frame.CashboxStatus.FsStatus.CycleIsOpen = 1

			case "/fiscalcheck.json":
				frame.Document = &umka.Document{}
				frame.Document.Data.DocNumber = rand.Uint32()
				frame.Document.Data.Props = []umka.Prop{
					{Tag: 1040, Value: frame.Document.Data.DocNumber},
					{Tag: 1196, Value: "qr=ok"},
				}

			default:
				statusCode = 404
			}
			var body []byte
			var err error
			if statusCode == 200 {
				body, err = json.Marshal(frame)
				if !assert.NoError(t, err) {
					statusCode = 500
					body = []byte(err.Error())
				}
			}
			t.Logf("umka > %d %s", statusCode, string(body))
			rb := []byte(fmt.Sprintf("HTTP/1.0 %d %s\r\ncontent-length: %d\r\n\r\n%s",
				statusCode, http.StatusText(statusCode), len(body), body))
			return http.ReadResponse(bufio.NewReader(bytes.NewReader(rb)), req)
		}}
	if g.Config.DB.URL == "" {
		t.Fatal("This test requires access to PostgreSQL server, please set environment venderctl_db_url=postgres://[USER[:PASS]@][HOST[:PORT]]/DATABASE?sslmode=disable")
	}

	require.NoError(t, g.InitDB(CmdName))
	db := g.DB.Conn()
	_, err := db.Exec("begin")
	require.NoError(t, err)
	defer func() {
		_, _ = db.Exec("rollback")
		assert.NoError(t, db.Close())
	}()
	sqlPath, err := filepath.Abs("../../../sql/schema.sql")
	require.NoError(t, err)
	sqlSchema, err := ioutil.ReadFile(sqlPath)
	require.NoError(t, err)
	_, err = db.Exec(string(sqlSchema))
	require.NoError(t, err)

	worker := "test" // TODO random
	db = db.WithParam("worker", worker)

	_, err = db.
		WithParam("vmid", -1).                         // TODO random
		WithParam("code", "123").                      // TODO random
		WithParam("options", pg.Array([]int32{4, 4})). // TODO random
		WithParam("price", 200).                       // TODO random
		WithParam("method", 1).                        // TODO check gift payment method doesn't generate tax_job
		Exec(`insert into trans (vmid,vmtime,received,menu_code,options,price,method) values (?vmid,now(),now(),?code,?options,?price,?method)`)
	require.NoError(t, err)

	tj := &MTaxJob{}
	_, err = db.QueryOne(tj, "select * from tax_job limit 2")
	require.NoError(t, err)
	assert.Len(t, tj.Ops, 1)
	t.Logf("tj=%#v ops=%v data=%s", tj, tj.Ops, tj.Data.String())

	ok, err := taxStep(ctx, db, worker)
	assert.True(t, ok)
	require.NoError(t, err)

	_, err = db.QueryOne(tj, "select * from tax_job limit 2")
	require.NoError(t, err)
	require.NotNil(t, tj.Data)
	t.Logf("tj=%#v ops=%v data=%s", tj, tj.Ops, tj.Data.String())
}

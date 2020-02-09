package tele

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-pg/pg/v9"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele_api "github.com/temoto/vender/tele"
	"github.com/temoto/venderctl/internal/state"
	state_new "github.com/temoto/venderctl/internal/state/new"
)

type MError struct { //nolint:maligned
	VmId       int32     `pg:"vmid"`
	VmTime     time.Time `pg:"vmtime"`
	Received   time.Time
	AppVersion string
	Code       int32
	Message    string
	Count      int32
}

type MIngest struct {
	Received time.Time
	VmId     int32 `pg:"vmid"`
	Done     bool
	Raw      []byte
}

type MState struct {
	VmId     int32 `pg:"vmid"`
	State    tele_api.State
	Received time.Time
}

type MTrans struct {
	VmId     int32     `pg:"vmid"`
	VmTime   time.Time `pg:"vmtime"`
	Received time.Time
	MenuCode string
	Options  []int32
	Price    int32
	Method   int32
	TaxJobId int64
}

func TestTeleDB(t *testing.T) {
	t.Parallel()

	if _, g := state_new.NewTestContext(t, nil, ""); g.Config.DB.URL == "" {
		t.Fatal("This test requires access to PostgreSQL server, please set environment venderctl_db_url=postgres://[USER[:PASS]@][HOST[:PORT]]/DATABASE?sslmode=disable")
	}

	type tenv struct {
		t      testing.TB
		ctx    context.Context
		g      *state.Global
		dbConn *pg.Conn
	}

	cases := []struct {
		name   string
		config string
		check  func(*tenv)
	}{
		{"error", `tele { listen "tcp://" {} }`, func(env *tenv) {
			t := env.t
			b, err := hex.DecodeString("080810ab92edc58d92eaef151a0912076578616d706c653a008a0105302e312e30")
			require.NoError(t, err)
			var tm tele_api.Telemetry
			require.NoError(t, proto.Unmarshal(b, &tm))
			require.NoError(t, onTelemetry(env.ctx, env.dbConn, tm.VmId, &tm))

			var count int
			_, err = env.dbConn.QueryOne(pg.Scan(&count), `select count(*) from trans`)
			require.NoError(t, err)
			assert.Equal(t, 0, count)

			var errorRow MError
			_, err = env.dbConn.QueryOne(&errorRow, `select * from error where vmid=?`, tm.VmId)
			require.NoError(t, err)
			assert.Equal(t, tm.VmId, errorRow.VmId)
			assert.Equal(t, "0.1.0", errorRow.AppVersion)
			assert.Equal(t, "example", errorRow.Message)

			var ingest MIngest
			_, err = env.dbConn.QueryOne(&ingest, `select * from ingest where vmid=?`, tm.VmId)
			require.NoError(t, err)
			assert.Equal(t, b, ingest.Raw)
		}},
		{"state", `tele { exec_on_state="false" listen "tcp://" {} }`, func(env *tenv) {
			t := env.t
			vmid := rand.Int31()
			var count int
			st := &MState{}

			_, err := env.dbConn.QueryOne(pg.Scan(&count), `select count(*) from state`)
			require.NoError(t, err)
			assert.Equal(t, int(0), count)

			require.NoError(t, onState(env.ctx, env.dbConn, vmid, tele_api.State_Boot))
			_, err = env.dbConn.QueryOne(st, `select * from state where vmid=?`, vmid)
			require.NoError(t, err)
			assert.Equal(t, vmid, st.VmId)
			assert.Equal(t, tele_api.State_Boot, st.State)

			require.NoError(t, onState(env.ctx, env.dbConn, vmid, tele_api.State_Nominal))
			_, err = env.dbConn.QueryOne(st, `select * from state where vmid=?`, vmid)
			require.NoError(t, err)
			assert.Equal(t, vmid, st.VmId)
			assert.Equal(t, tele_api.State_Nominal, st.State)
		}},
		{"telemetry", `tele { listen "tcp://" {} }`, func(env *tenv) {
			t := env.t
			b, err := hex.DecodeString("08f78c3d320518fc1108053a00")
			require.NoError(t, err)
			var tm tele_api.Telemetry
			require.NoError(t, proto.Unmarshal(b, &tm))
			// const q = `insert into trans (vmid,vmtime,received,menu_code,options,price,method) values (?0,to_timestamp(?1),current_timestamp,?2,?3,?4,?5)`
			// sq := pg.SafeQuery(q, 1, tm.Time, tm.Transaction.Code, pg.Array(tm.Transaction.Options), tm.Transaction.Price, tm.Transaction.PaymentMethod).Value()
			// t.Log(sq)
			require.NoError(t, onTelemetry(env.ctx, env.dbConn, tm.VmId, &tm))

			var tr MTrans
			_, err = env.dbConn.QueryOne(&tr, `select * from trans where vmid=?`, tm.VmId)
			require.NoError(t, err)
			assert.Equal(t, proto.CompactTextString(&tm), fmt.Sprintf("vm_id:%d transaction:<price:%d 1:5 > stat:<> ", tr.VmId, tr.Price))

			var ingest MIngest
			_, err = env.dbConn.QueryOne(&ingest, `select * from ingest where vmid=?`, tm.VmId)
			require.NoError(t, err)
			assert.Equal(t, b, ingest.Raw)
		}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			env := &tenv{t: t}
			env.ctx, env.g = state_new.NewTestContext(t, nil, c.config)
			if err := teleInit(env.ctx); err != nil {
				if strings.Contains(err.Error(), "db ping") && strings.Contains(err.Error(), "connection refused") {
					err = errors.Annotatef(err, "please check database server at %s", env.g.Config.DB.URL)
				}
				require.NoError(t, err)
			}

			env.dbConn = env.g.DB.Conn()
			_, err := env.dbConn.Exec("begin")
			require.NoError(t, err)
			defer func() {
				_, _ = env.dbConn.Exec("rollback")
				assert.NoError(t, env.dbConn.Close())
			}()
			sqlPath, err := filepath.Abs("../../../sql/schema.sql")
			require.NoError(t, err)
			sqlSchema, err := ioutil.ReadFile(sqlPath)
			require.NoError(t, err)
			_, err = env.dbConn.Exec(string(sqlSchema))
			require.NoError(t, err)

			c.check(env)
		})
	}
}

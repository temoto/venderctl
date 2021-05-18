package tele

import (
	"context"
	"testing"
	"time"

	"github.com/256dpi/gomqtt/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/temoto/vender/log2"
	//	"github.com/temoto/vender/tele/mqtt"
	state_new "github.com/temoto/venderctl/internal/state/new"
	"gopkg.in/hlandau/passlib.v1"
)

type tenv struct {
	t        testing.TB
	ctx      context.Context
	addrs    []string
	log      *log2.Log
	xxx_tele *tele
	mch      chan *packet.Message
}

func TestServer(t *testing.T) {
	t.Parallel()
	const configListenAll = `
tele {
	listen "tcp://127.0.0.1:" {
		allow_roles = ["_all"]
	}
	mqtt_log_debug = true
	log_debug = true
	role_admin = ["adm"]
	role_monitor = ["mon"]
}`
	cases := []struct {
		name   string
		config string
		check  func(t testing.TB, env *tenv)
	}{
		{"connect-close", configListenAll, func(t testing.TB, env *tenv) {
			mc := tenvClient(env)
			require.NoError(t, mc.Init())
			require.NoError(t, mc.Close())
		}},
		{"sub-pub", configListenAll, func(t testing.TB, env *tenv) {
			ch := make(chan *packet.Message, 32)
			cli1 := tenvClient(env)
			cli1.Config.Subscriptions = []packet.Subscription{
				{Topic: "+/w/+", QOS: packet.QOSAtLeastOnce},
			}
			cli1.Config.OnMessage = func(msg *packet.Message) error {
				t.Logf("OnMessage msg=%v", msg)
				ch <- msg
				return nil
			}
			require.NoError(t, cli1.Init())
			cli2 := tenvClient(env)
			cli2.Config.ClientID = "mon"
			require.NoError(t, cli2.Init())
			expect := &packet.Message{
				Topic:   "vm1/w/1s",
				QOS:     packet.QOSAtLeastOnce,
				Payload: []byte{1},
			}
			require.NoError(t, cli2.Publish(env.ctx, expect))
			msg := <-ch
			assert.Equal(t, expect, msg)
		}},
		{"other-topic", configListenAll, func(t testing.TB, env *tenv) {
			ch := make(chan *packet.Message, 32)
			cli1 := tenvClient(env)
			cli1.Config.Subscriptions = []packet.Subscription{
				{Topic: "#", QOS: packet.QOSAtLeastOnce},
			}
			cli1.Config.OnMessage = func(msg *packet.Message) error {
				t.Logf("OnMessage msg=%v", msg)
				ch <- msg
				return nil
			}
			require.NoError(t, cli1.Init())
			cli2 := tenvClient(env)
			cli2.Config.ClientID = "mon"
			require.NoError(t, cli2.Init())
			expect := &packet.Message{
				Topic:   "todo_random",
				QOS:     packet.QOSAtLeastOnce,
				Payload: []byte{1},
			}
			require.NoError(t, cli2.Publish(env.ctx, expect))
			msg := <-ch
			assert.Equal(t, expect, msg)
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			teler := NewTele()
			env := &tenv{
				t:        t,
				xxx_tele: teler.(*tele),
			}
			env.xxx_tele.secrets.m = map[string]string{
				"adm": mustPassHash(t, "secret"),
				"mon": mustPassHash(t, "secret"),
			}
			env.xxx_tele.secrets.valid = time.Now()
			ctx, g := state_new.NewTestContext(t, teler, c.config)
			require.NoError(t, g.Config.Tele.EnableServer())
			require.NoError(t, g.Tele.Init(ctx, g.Log, g.Config.Tele))
			env.ctx = ctx
			env.addrs = g.Tele.Addrs()
			require.GreaterOrEqual(t, len(env.addrs), 1)

			c.check(t, env)

			require.NoError(t, teler.Close())
		})
	}
}

func mustPassHash(t testing.TB, secret string) string {
	t.Helper()
	h, err := passlib.Hash(secret)
	require.NoError(t, err)
	return h
}

func tenvClient(env *tenv) *mqtt.Client {
	mc := &mqtt.Client{Log: env.log}
	if len(env.addrs) >= 1 {
		mc.Config.BrokerURL = "tcp://" + env.addrs[0]
	}
	mc.Config.Username = "adm"
	mc.Config.Password = "secret"
	mc.Config.NetworkTimeout = 5 * time.Second
	mc.Config.KeepaliveSec = uint16(mc.Config.NetworkTimeout / time.Second)
	mc.Config.ReconnectDelay = mc.Config.NetworkTimeout
	mc.Config.OnMessage = func(m *packet.Message) error {
		if env.mch != nil {
			env.mch <- m
		}
		return nil
	}
	return mc
}

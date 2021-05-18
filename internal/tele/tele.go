// Server side of vender tele.
// Goals:
// - receive telemetry
// - receive state
// - send command
// - while hiding transport protocol (MQTT)
package tele

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/256dpi/gomqtt/packet"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	"github.com/temoto/alive/v2"
	"github.com/temoto/vender/log2"
	vender_api "github.com/temoto/vender/tele"
	// mqtt1 "github.com/temoto/vender/tele/mqtt"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

const defaultSendTimeout = 30 * time.Second
const defaultNetworkTimeout = 3 * time.Second

type tele struct { //nolint:maligned
	sync.RWMutex
	alive *alive.Alive
	conf  tele_config.Config
	log   *log2.Log
	pch   chan tele_api.Packet
	// mqttsrv *mqtt1.Server
	// mqttcli *mqtt1.Client
	m       mqtt.Client
	mopt    *mqtt.ClientOptions
	mqttcom interface {
		Close() error
		Publish(context.Context, *packet.Message) error
	}
	secrets Secrets
}

func NewTele() tele_api.Teler { return &tele{} }

func (self *tele) Init(ctx context.Context, log *log2.Log, teleConfig tele_config.Config) error {
	self.Lock()
	defer self.Unlock()

	self.alive = alive.NewAlive()
	self.conf = teleConfig
	self.log = log.Clone(log2.LInfo)
	if self.conf.LogDebug {
		self.log.SetLevel(log2.LDebug)
	}
	self.pch = make(chan tele_api.Packet, 1)

	err := self.mqttInit(ctx, log)
	return errors.Annotate(err, "tele.Init")
}

func (self *tele) Close() error {
	switch self.conf.Mode {
	case tele_config.ModeDisabled:
		return nil
	case tele_config.ModeClient, tele_config.ModeServer:
		return self.mqttcom.Close()
	default:
		panic(self.msgInvalidMode())
	}
}

func (self *tele) Addrs() []string {
	switch self.conf.Mode {
	case tele_config.ModeDisabled, tele_config.ModeClient:
		return nil
	case tele_config.ModeServer:
		self.RLock()
		defer self.RUnlock()
		// return self.mqttsrv.Addrs()
		return nil
	default:
		panic(self.msgInvalidMode())
	}
}

func (self *tele) Chan() <-chan tele_api.Packet { return self.pch }

func (self *tele) SendCommand(vmid int32, c *vender_api.Command) error {
	if c.Id == 0 {
		c.Id = rand.Uint32()
	}
	if c.ReplyTopic == "" {
		c.ReplyTopic = fmt.Sprintf("cr/%d", c.Id)
	}

	payload, err := proto.Marshal(c)
	if err != nil {
		return errors.Trace(err)
	}
	p := tele_api.Packet{Kind: tele_api.PacketCommand, VmId: vmid, Payload: payload}
	ctx, cancel := context.WithTimeout(context.Background(), defaultSendTimeout)
	defer cancel()
	err = self.mqttSend(ctx, p)
	return errors.Annotate(err, "tele.SendCommand")
}

// Will consume and drop irrelevant packets from pch.
func (self *tele) CommandTx(vmid int32, c *vender_api.Command, timeout time.Duration) (*vender_api.Response, error) {
	if c.Deadline == 0 {
		c.Deadline = time.Now().Add(timeout).UnixNano()
	}
	if err := self.SendCommand(vmid, c); err != nil {
		return nil, errors.Annotate(err, "CommandTx")
	}

	tmr := time.NewTimer(timeout)
	defer tmr.Stop()
	for {
		select {
		case p := <-self.pch:
			// if p.Kind == tele.PacketCommandReply {
			if r, err := p.CommandResponse(); err == nil {
				if r.CommandId == c.Id {
					if r.Error == "" {
						return r, nil
					}
					return r, fmt.Errorf(r.Error)
				} else {
					self.log.Errorf("current command.id=%d unexpected response=%#v", c.Id, r)
				}
			} else {
				self.log.Errorf("unexpected packet=%#v", p)
			}

		case <-tmr.C:
			return nil, errors.Timeoutf("response")
		}
	}
}

func (self *tele) msgInvalidMode() string {
	return fmt.Sprintf("code error tele Config.Mode='%s'", self.conf.Mode)
}

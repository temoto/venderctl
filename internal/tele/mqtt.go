package tele

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/256dpi/gomqtt/client/future"
	"github.com/256dpi/gomqtt/packet"
	"github.com/juju/errors"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	"github.com/temoto/vender/tele/mqtt"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
	"gopkg.in/hlandau/passlib.v1"
)

const SecretsStale = 5 * time.Second

func (self *tele) mqttInit(ctx context.Context, log *log2.Log) error {
	mlog := log.Clone(log2.LInfo)
	if self.conf.MqttLogDebug {
		mlog.SetLevel(log2.LDebug)
	}

	switch self.conf.Mode {
	case tele_config.ModeDisabled: // disabled
		return nil

	case tele_config.ModeClient:
		return self.mqttInitClient(ctx, mlog)

	case tele_config.ModeServer:
		return self.mqttInitServer(ctx, mlog)

	default:
		panic(self.msgInvalidMode())
	}
}

func (self *tele) mqttInitClient(ctx context.Context, mlog *log2.Log) error {
	self.mqttcli = &mqtt.Client{Log: mlog}
	self.mqttcom = self.mqttcli
	cfg := &self.mqttcli.Config // short alias
	cfg.BrokerURL = self.conf.Connect.URL
	cfg.ClientID = self.conf.Connect.ClientID
	cfg.ReconnectDelay = 7 * time.Second
	cfg.NetworkTimeout = time.Duration(self.conf.Connect.NetworkTimeoutSec) * time.Second
	cfg.KeepaliveSec = uint16(self.conf.Connect.KeepaliveSec)
	if cfg.KeepaliveSec == 0 {
		cfg.KeepaliveSec = 5
	}
	cfg.Subscriptions = []packet.Subscription{{Topic: "+/cr/+", QOS: packet.QOSAtLeastOnce}}
	cfg.OnMessage = self.mqttClientOnPublish
	var err error
	cfg.TLS, err = self.conf.Connect.TLS.TLSConfig()
	if err != nil {
		return errors.Annotate(err, "TLS")
	}
	return self.mqttcli.Init()
}

func (self *tele) mqttInitServer(ctx context.Context, mlog *log2.Log) error {
	self.mqttsrv = mqtt.NewServer(mqtt.ServerOptions{
		Log: mlog,
		ForceSubs: []packet.Subscription{
			{Topic: "%c/r/#", QOS: packet.QOSAtLeastOnce},
		},
		OnAuth:    self.mqttOnAuth,
		OnPublish: self.mqttServerOnPublish,
	})
	errs := make([]error, 0)
	opts := make([]*mqtt.BackendOptions, 0, len(self.conf.Listens))
	for _, l := range self.conf.Listens {
		tlsconf, err := l.TLS.TLSConfig()
		if err != nil {
			err = errors.Annotate(err, "TLS")
			errs = append(errs, err)
			continue
		}

		opt := &mqtt.BackendOptions{
			URL:            l.URL,
			TLS:            tlsconf,
			CtxData:        l,
			NetworkTimeout: helpers.IntSecondDefault(l.NetworkTimeoutSec, defaultNetworkTimeout),
		}
		opts = append(opts, opt)
	}
	errs = append(errs, self.secrets.CachedReadFile(self.conf.SecretsPath, SecretsStale))
	if err := helpers.FoldErrors(errs); err != nil {
		return err
	}
	err := self.mqttsrv.Listen(ctx, opts)
	if err == nil {
		self.mqttcom = self.mqttsrv
		self.log.Infof("mqtt started listen=%q", self.mqttsrv.Addrs())
	}
	return errors.Annotate(err, "mqtt Server.Init")
}

func (self *tele) mqttOnAuth(ctx context.Context, id, username string, opt *mqtt.BackendOptions, pkt packet.Generic) (bool, error) {
	if err := self.secrets.CachedReadFile(self.conf.SecretsPath, SecretsStale); err != nil {
		return false, err
	}
	switch pt := pkt.(type) {
	case *packet.Connect:
		return self.mqttOnAuthConnect(ctx, id, opt, pt)

	default:
		return false, errors.Errorf("onauth TODO pkt=%s", pkt.String())
	}
}

func (self *tele) mqttOnAuthConnect(ctx context.Context, id string, opt *mqtt.BackendOptions, pkt *packet.Connect) (bool, error) {
	if err := self.secrets.Verify(pkt.Username, pkt.Password); err != nil {
		self.log.Errorf("authenticate user=%s err=%v", pkt.Username, err)
		return false, nil
	}

	allowRoles := opt.CtxData.(tele_config.Listen).AllowRoles
	if _, err := self.mqttAuthorize(pkt.ClientID, pkt.Username, allowRoles, opt.URL); err != nil {
		err = errors.Annotate(err, "tele.mqttAuthorize")
		self.log.Error(err)
		return false, nil
	}
	return true, nil
}

func (self *tele) mqttClientOnPublish(msg *packet.Message) error {
	// defer ack.Cancel()
	if !self.alive.Add(1) {
		return context.Canceled
	}
	defer self.alive.Done()

	p, err := parsePacket(msg)
	self.log.Debugf("parsePacket p=%#v err=%v", p, err)
	switch err {
	case nil:
		if p.Kind != tele_api.PacketCommandReply {
			// ack.Complete()
			return nil
		}

	case errTopicIgnore:
		// ack.Complete()
		return nil

	default:
		return errors.Annotatef(err, "msg=%v", msg)
	}

	select {
	case self.pch <- p:
		// ack.Complete()
		return nil

	case <-time.After(time.Second):
		self.log.Errorf("CRITICAL mqtt receive chan full")
	}
	return nil
}

func (self *tele) mqttServerOnPublish(ctx context.Context, msg *packet.Message, ack *future.Future) error {
	defer ack.Cancel(context.Canceled)
	if !self.alive.Add(1) {
		return context.Canceled
	}
	defer self.alive.Done()

	_ = self.mqttcom.Publish(ctx, msg)

	p, err := parsePacket(msg)
	switch err {
	case nil:
		if p.Kind == tele_api.PacketInvalid {
			// parsePacket -> nil,nil means tele is not interested in that MQTT message
			ack.Complete(nil)
			return nil
		}

	case errTopicIgnore:
		ack.Complete(nil)
		return nil

	default:
		return errors.Annotatef(err, "msg=%v", msg)
	}
	self.log.Debugf("tele packet=%s", p.String())

	// TODO onPublish() trap for CommandTx response; this is only needed for tele server to send commands

	for {
		t := time.NewTimer(time.Second)
		select {
		case self.pch <- p:
			t.Stop()
			ack.Complete(nil)
			return nil

		case <-t.C:
			self.log.Errorf("CRITICAL mqtt receive chan full")
		}
	}
}

func (self *tele) mqttAuthorize(id, username string, allowRoles []string, listenAddress string) (tele_config.Role, error) {
	for _, rstr := range allowRoles {
		r := tele_config.Role(rstr)
		// self.log.Debugf("tele.mqttAuthorize username=%s r=%s", username, r)
		if r == tele_config.RoleAdmin || r == tele_config.RoleAll {
			// FIXME map[string]
			for _, u := range self.conf.RoleMembersAdmin {
				if username == u {
					return tele_config.RoleAdmin, nil
				}
			}
		}

		if r == tele_config.RoleControl || r == tele_config.RoleAll {
			// FIXME map[string]
			for _, u := range self.conf.RoleMembersControl {
				if username == u {
					return tele_config.RoleControl, nil
				}
			}
		}

		if r == tele_config.RoleMonitor || r == tele_config.RoleAll {
			// FIXME map[string]
			for _, u := range self.conf.RoleMembersMonitor {
				if username == u {
					return tele_config.RoleMonitor, nil
				}
			}
		}

		if r == tele_config.RoleVender || r == tele_config.RoleAll {
			if strings.HasPrefix(username, "vm") {
				return tele_config.RoleVender, nil
			}
		}
	}
	return tele_config.RoleInvalid, fmt.Errorf("permission denied username=%s listen=%s", username, listenAddress)
}

func (self *tele) mqttSend(ctx context.Context, p tele_api.Packet) error {
	if p.Kind != tele_api.PacketCommand {
		return errors.Errorf("code error mqtt not implemented Send packet=%v", p)
	}
	topic := fmt.Sprintf("vm%d/r/c", p.VmId)
	msg := &packet.Message{
		Topic:   topic,
		Payload: p.Payload,
		QOS:     packet.QOSAtLeastOnce,
		Retain:  false,
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		select {
		case <-self.alive.StopChan():
		case <-ctx.Done():
		}
	}()
	self.log.Debugf("mqtt publish topic=%s payload=%x", msg.Topic, msg.Payload)
	switch err := self.mqttcom.Publish(ctx, msg); err {
	case nil:
		return nil
	case mqtt.ErrNoSubscribers:
		return errors.Annotatef(err, "mqttSend topic=%s", topic)
	default:
		return errors.Annotatef(err, "mqttSend packet=%v", p)
	}
}

var reTopic = regexp.MustCompile(`^vm(-?\d+)/(\w+)/(.+)$`)
var errTopicIgnore = fmt.Errorf("ignore irrelevant topic")

func parsePacket(msg *packet.Message) (tele_api.Packet, error) {
	// parseTopic vm13/w/1s
	parts := reTopic.FindStringSubmatch(msg.Topic)
	if parts == nil {
		return tele_api.Packet{}, errTopicIgnore
	}

	p := tele_api.Packet{Payload: msg.Payload}
	if x, err := strconv.ParseInt(parts[1], 10, 32); err != nil {
		return p, errors.Annotatef(err, "invalid topic=%s parts=%q", msg.Topic, parts)
	} else {
		p.VmId = int32(x)
	}
	switch {
	case parts[2] == "r":
		return tele_api.Packet{}, errTopicIgnore
	case parts[2] == "cr":
		p.Kind = tele_api.PacketCommandReply
	case parts[3] == "1s":
		p.Kind = tele_api.PacketState
	case parts[3] == "1t":
		p.Kind = tele_api.PacketTelemetry
	default:
		return p, errors.Errorf("invalid topic=%s parts=%q", msg.Topic, parts)
	}
	return p, nil
}

func init() {
	if err := passlib.UseDefaults(passlib.Defaults20180601); err != nil {
		panic("code error passlib.UseDefaults: " + err.Error())
	}
}

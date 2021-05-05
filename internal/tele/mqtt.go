package tele

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/256dpi/gomqtt/client/future"
	"github.com/256dpi/gomqtt/packet"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/juju/errors"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	vender_api "github.com/temoto/vender/tele"
	// mqttl "github.com/temoto/vender/tele/mqtt"
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
	c := self.conf.Connect.URL
	if c == "" {
		panic("error client connect URL blank")
	}
	u, err := url.Parse(c)
	if err != nil {
		panic(fmt.Sprintf("error parse client connect URL['%s']", err))
	}
	scheme := u.Scheme
	host := u.Host
	keepAlive := helpers.IntSecondConfigDefault(self.conf.Connect.KeepaliveSec, 60)

	self.mopt = mqtt.NewClientOptions()
	// FIXME move this to config
	switch self.conf.Mode {
	case tele_config.ModeDisabled: // disabled
		return nil
	case tele_config.ModeCommand:
		self.mopt.SetOnConnectHandler(self.onConnectHandlerCommand)
		self.mopt.SetClientID("command")
		self.mopt.SetUsername("command")
		self.mopt.SetPassword("commandpass")

	case tele_config.ModeTax:
		self.mopt.SetBinaryWill("tax/c", []byte{0x00}, 1, true)
		self.mopt.SetOnConnectHandler(self.onConnectHandlerTax)
		self.mopt.SetClientID("tax")
		self.mopt.SetUsername("tax")
		self.mopt.SetPassword("taxpass")

	case tele_config.ModeSponge:
		self.mopt.SetBinaryWill("sponge/c", []byte{0x00}, 1, true)
		self.mopt.SetOnConnectHandler(self.onConnectHandlerSponge)
		self.mopt.SetUsername("ctl")
		self.mopt.SetClientID("ctl")
		self.mopt.SetPassword("ctlpass")

	// case tele_config.ModeServer:
	// 	return self.mqttInitServer(ctx, mlog)

	default:
		panic(self.msgInvalidMode())
	}

	self.mopt.
		AddBroker(strings.Join([]string{scheme, host}, "://")).
		// FIXME add tsl
		// SetTLSConfig(tlsconf).
		// SetStore(mqtt.NewFileStore(storePath)).
		SetCleanSession(false).
		SetDefaultPublishHandler(self.messageHandler).
		SetKeepAlive(keepAlive).
		SetPingTimeout(keepAlive).
		SetOrderMatters(true).
		SetResumeSubs(true).SetCleanSession(false).
		SetConnectRetryInterval(keepAlive).
		SetConnectionLostHandler(self.connectLostHandler).
		SetConnectRetry(true)

	self.m = mqtt.NewClient(self.mopt)

	token := self.m.Connect()
	for !token.WaitTimeout(1 * time.Second) {
	}
	if err := token.Error(); err != nil {
		panic(err)
	}

	self.log.Infof("connected to broker")
	return nil
}

func (self *tele) messageHandler(c mqtt.Client, msg mqtt.Message) {
	self.log.Debugf("income message: (%s %x) ", msg.Topic(), msg.Payload())
	p, err := parseTopic(msg)
	switch err {
	case nil:
		if p.Kind == tele_api.PacketInvalid {
			return
		}

	case errTopicIgnore:
		return

	default:
		errors.Annotatef(err, "msg=%v", msg)
		return
	}
	select {
	case self.pch <- p:
		return
	}
}

var reTopic = regexp.MustCompile(`^vm(-?\d+)/(\w+)/?(.+)?$`)
var errTopicIgnore = fmt.Errorf("ignore irrelevant topic")

func parseTopic(msg mqtt.Message) (tele_api.Packet, error) {
	parts := reTopic.FindStringSubmatch(msg.Topic())
	if parts == nil {
		return tele_api.Packet{}, errTopicIgnore
	}

	p := tele_api.Packet{Payload: msg.Payload()}
	if x, err := strconv.ParseInt(parts[1], 10, 32); err != nil {
		return p, errors.Annotatef(err, "invalid topic=%s parts=%q", msg.Topic, parts)
	} else {
		p.VmId = int32(x)
	}
	switch {
	case parts[2] == "c":
		p.Kind = tele_api.PacketConnect
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

func (self *tele) onConnectHandlerCommand(c mqtt.Client) {
	if token := c.Subscribe("+/cr/+", 1, nil); token.Wait() && token.Error() != nil {
		self.log.Errorf("commnad subscribe  error")
	} else {
		self.log.Debugf("Subscribe Ok")
	}
}

func (self *tele) onConnectHandlerTax(c mqtt.Client) {
	if token := c.Subscribe("+/cr/+", 1, nil); token.Wait() && token.Error() != nil {
		self.log.Errorf("tax subscribe error")
	} else {
		self.log.Debugf("Subscribe Ok")
		c.Publish("tax/c", 1, true, []byte{0x01})
	}
}

func (self *tele) onConnectHandlerSponge(c mqtt.Client) {
	if token := c.Subscribe("+/#", 1, nil); token.Wait() && token.Error() != nil {
		self.log.Errorf("sponge subscribe error")
	} else {
		self.log.Debugf("Subscribe Ok")
		c.Publish("sponge/c", 1, true, []byte{0x01})
	}

}

func (self *tele) connectLostHandler(c mqtt.Client, err error) {
	self.log.Debugf("broker connection lost.")
}

// func (self *tele) mqttInitServer(ctx context.Context, mlog *log2.Log) error {
// 	self.mqttsrv = mqttl.NewServer(mqttl.ServerOptions{
// 		Log: mlog,
// 		ForceSubs: []packet.Subscription{
// 			{Topic: "%c/r/#", QOS: packet.QOSAtLeastOnce},
// 		},
// 		OnConnect: self.mqttOnConnect,
// 		OnClose:   self.mqttOnClose,
// 		OnPublish: self.mqttServerOnPublish,
// 	})
// 	errs := make([]error, 0)
// 	opts := make([]*mqttl.BackendOptions, 0, len(self.conf.Listens))
// 	for _, l := range self.conf.Listens {
// 		tlsconf, err := l.TLS.TLSConfig()
// 		if err != nil {
// 			err = errors.Annotate(err, "TLS")
// 			errs = append(errs, err)
// 			continue
// 		}

// 		opt := &mqttl.BackendOptions{
// 			URL:            l.URL,
// 			TLS:            tlsconf,
// 			CtxData:        l,
// 			NetworkTimeout: helpers.IntSecondDefault(l.NetworkTimeoutSec, defaultNetworkTimeout),
// 		}
// 		opts = append(opts, opt)
// 	}
// 	errs = append(errs, self.secrets.CachedReadFile(self.conf.SecretsPath, SecretsStale))
// 	if err := helpers.FoldErrors(errs); err != nil {
// 		return err
// 	}
// 	err := self.mqttsrv.Listen(ctx, opts)
// 	if err == nil {
// 		self.mqttcom = self.mqttsrv
// 		self.log.Infof("mqtt started listen=%q", self.mqttsrv.Addrs())
// 	}
// 	return errors.Annotate(err, "mqtt Server.Init")
// }

func (self *tele) mqttOnClose(clientid string, clean bool, e error) {
	fmt.Printf("\033[41m mqttOnClose \033[0m\n")
	// inject new state if clientid ~= vm(-?\d+)
	if strings.HasPrefix(clientid, "vm") {
		if x, err := strconv.ParseInt(clientid[2:], 10, 32); err == nil {
			vmid := int32(x)
			state := vender_api.State_Disconnected
			// if clean {
			// 	// TODO maybe new state "correctly shutdown"
			// 	state = vender_api.State_Invalid
			// }
			self.pch <- tele_api.Packet{
				VmId:    vmid,
				Kind:    tele_api.PacketState,
				Payload: []byte{byte(state)},
			}
		}
	}
}

// func (self *tele) mqttOnConnect(ctx context.Context, opt *mqttl.BackendOptions, pkt *packet.Connect) (bool, error) {
// 	fmt.Printf("\033[41m mqttOnConnect \033[0m\n")
// 	if err := self.secrets.CachedReadFile(self.conf.SecretsPath, SecretsStale); err != nil {
// 		return false, err
// 	}
// 	if err := self.secrets.Verify(pkt.Username, pkt.Password); err != nil {
// 		self.log.Errorf("authenticate user=%s err=%v", pkt.Username, err)
// 		return false, nil
// 	}

// 	allowRoles := opt.CtxData.(tele_config.Listen).AllowRoles
// 	if _, err := self.mqttAuthorize(pkt.ClientID, pkt.Username, allowRoles, opt.URL); err != nil {
// 		err = errors.Annotate(err, "tele.mqttAuthorize")
// 		self.log.Error(err)
// 		return false, nil
// 	}
// 	return true, nil
// }

func (self *tele) mqttClientOnPublish(msg *packet.Message) error {
	fmt.Printf("\033[41m mqttClientOnPublish \033[0m\n")
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
	self.m.Publish(topic, 1, false, p.Payload)
	return nil
	// msg := &packet.Message{
	// 	Topic:   topic,
	// 	Payload: p.Payload,
	// 	QOS:     packet.QOSAtLeastOnce,
	// 	Retain:  false,
	// }
	// ctx, cancel := context.WithCancel(ctx)
	// go func() {
	// 	defer cancel()
	// 	select {
	// 	case <-self.alive.StopChan():
	// 	case <-ctx.Done():
	// 	}
	// }()
	// self.log.Debugf("mqtt publish topic=%s payload=%x", msg.Topic, msg.Payload)
	// switch err := self.mqttcom.Publish(ctx, msg); err {
	// case nil:
	// 	return nil
	// case mqttl.ErrNoSubscribers:
	// 	return errors.Annotatef(err, "mqttSend topic=%s", topic)
	// default:
	// 	return errors.Annotatef(err, "mqttSend packet=%v", p)
	// }
}

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

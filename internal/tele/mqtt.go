package tele

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	// "github.com/256dpi/gomqtt/client/future"
	// "github.com/256dpi/gomqtt/packet"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/juju/errors"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	// vender_api "github.com/temoto/vender/tele"
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
	if token := c.Subscribe("+/cr", 1, nil); token.Wait() && token.Error() != nil {
		self.log.Errorf("commnad subscribe  error")
	} else {
		self.log.Debugf("Subscribe Ok")
	}
}

func (self *tele) onConnectHandlerTax(c mqtt.Client) {
	if token := c.Subscribe("+/cr", 1, nil); token.Wait() && token.Error() != nil {
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

func (self *tele) mqttSend(ctx context.Context, p tele_api.Packet) error {
	if p.Kind != tele_api.PacketCommand {
		return errors.Errorf("code error mqtt not implemented Send packet=%v", p)
	}
	topic := fmt.Sprintf("vm%d/r/c", p.VmId)
	self.m.Publish(topic, 1, false, p.Payload)
	return nil
}

func init() {
	if err := passlib.UseDefaults(passlib.Defaults20180601); err != nil {
		panic("code error passlib.UseDefaults: " + err.Error())
	}
}

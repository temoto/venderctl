package tele

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"time"

	"github.com/AlexTransit/vender/helpers"
	"github.com/AlexTransit/vender/log2"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/juju/errors"

	// vender_api "github.com/AlexTransit/vender/tele"
	// mqttl "github.com/AlexTransit/vender/tele/mqtt"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
	"gopkg.in/hlandau/passlib.v1"
)

// const SecretsStale = 5 * time.Second

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
	pingTimeout := helpers.IntSecondConfigDefault(self.conf.Connect.PingTimeoutSec, 30)
	retryInterval := helpers.IntSecondConfigDefault(self.conf.Connect.KeepaliveSec/2, 30)

	mqtt.ERROR = log
	mqtt.CRITICAL = log
	mqtt.WARN = log
	self.mopt = mqtt.NewClientOptions()
	credFun := func() (string, string) {
		return self.clientId, self.clientPasword
	}

	self.mopt.
		AddBroker(strings.Join([]string{scheme, host}, "://")).
		// FIXME add tsl
		// SetTLSConfig(tlsconf).
		// SetStore(mqtt.NewFileStore(storePath)).
		SetCleanSession(false).
		SetClientID(self.clientId).
		SetCredentialsProvider(credFun).
		SetDefaultPublishHandler(self.messageHandler).
		SetKeepAlive(keepAlive).
		SetPingTimeout(pingTimeout).
		SetOrderMatters(false).
		SetResumeSubs(true).SetCleanSession(false).
		SetConnectRetryInterval(retryInterval).
		SetConnectionLostHandler(self.connectLostHandler).
		SetConnectRetry(true).
		SetOnConnectHandler(self.onConnectHandler).
		SetBinaryWill(self.clientId+"/c", []byte{0x00}, 1, true)

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
	// self.log.Debugf("income message: (%s %x) ", msg.Topic(), msg.Payload())
	p, err := parseTopic(msg)
	switch err {
	case nil:
		if p.Kind == tele_api.PacketInvalid {
			return
		}

	case errTopicIgnore:
		return

	default:
		// errors.Annotatef(err, "msg=%v", msg)
		self.log.Debugf("msg=%v", msg)
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
		return p, errors.Annotatef(err, "invalid topic=%s parts=%v", msg.Topic(), parts)
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
		return p, errors.Errorf("invalid topic=%s parts=%q", msg.Topic(), parts)
	}
	return p, nil
}

func (t *tele) onConnectHandler(c mqtt.Client) {
	if t.clientSubscribe != "" {
		if token := c.Subscribe(t.clientSubscribe, 0, nil); token.Wait() && token.Error() != nil {
			t.log.Errorf("%s subscribe error", t.clientId)
		} else {
			t.log.Debugf("%s subscribe Ok", t.clientId)
			c.Publish(t.clientId+"/c", 1, true, []byte{0x01})
		}

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

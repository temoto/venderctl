package tele

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/256dpi/gomqtt/packet"
	"github.com/juju/errors"
	"github.com/temoto/alive"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	"github.com/temoto/venderctl/internal/mqtt"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

const defaultNetworkTimeout = 3 * time.Second
const vmTimeout = 30 * time.Second

type transportMqtt struct { //nolint:maligned
	sync.Mutex
	alive *alive.Alive
	log   *log2.Log
	pch   chan Packet
	m     mqtt.Client
}

var _ transporter = &transportMqtt{} // compile-time interface check

func (self *transportMqtt) Init(ctx context.Context, log *log2.Log, teleConfig tele_config.Config) error {
	self.alive = alive.NewAlive()
	self.log = log.Clone(log2.LInfo)
	if teleConfig.LogDebug {
		self.log.SetLevel(log2.LDebug)
	}
	self.m.Log = log.Clone(log2.LInfo)
	if teleConfig.MqttLogDebug {
		self.m.Log.SetLevel(log2.LDebug)
	}

	self.pch = make(chan Packet, 1)

	networkTimeout := helpers.IntSecondDefault(teleConfig.NetworkTimeoutSec, defaultNetworkTimeout)
	if teleConfig.KeepaliveSec == 0 {
		teleConfig.KeepaliveSec = int(networkTimeout / time.Second)
	}
	subs := make([]packet.Subscription, len(teleConfig.MqttSubscribe))
	for i, pattern := range teleConfig.MqttSubscribe {
		subs[i] = packet.Subscription{Topic: pattern, QOS: packet.QOSAtLeastOnce}
	}

	if _, err := url.ParseRequestURI(teleConfig.MqttBroker); err != nil {
		return errors.Annotatef(err, "mqtt dial broker=%s", teleConfig.MqttBroker)
	}
	tlsconf := new(tls.Config)
	if teleConfig.TlsCaFile != "" {
		tlsconf.RootCAs = x509.NewCertPool()
		cabytes, err := ioutil.ReadFile(teleConfig.TlsCaFile)
		if err != nil {
			return errors.Annotate(err, "mqtt TLS CA read")
		}
		tlsconf.RootCAs.AppendCertsFromPEM(cabytes)
	}
	if teleConfig.TlsPsk != "" {
		copy(tlsconf.SessionTicketKey[:], helpers.MustHex(teleConfig.TlsPsk))
	}
	self.m.Config.BrokerURL = teleConfig.MqttBroker
	self.m.Config.ClientID = teleConfig.MqttClientId
	self.m.Config.KeepaliveSec = uint16(teleConfig.KeepaliveSec + 1)
	self.m.Config.NetworkTimeout = networkTimeout
	self.m.Config.OnMessage = self.onMessage
	self.m.Config.Password = teleConfig.MqttPassword
	self.m.Config.Subscriptions = subs
	self.m.Config.TLS = tlsconf
	return self.m.Init()
}

func (self *transportMqtt) Close() error {
	self.log.Debugf("mqtt.Close")
	self.Lock()
	defer self.Unlock()

	self.m.Close()
	self.alive.Stop()
	self.alive.Wait()
	close(self.pch)
	return nil
}

func (self *transportMqtt) RecvChan() <-chan Packet {
	return self.pch
}

func (self *transportMqtt) Send(p Packet) error {
	if p.Kind != PacketCommand {
		return errors.Errorf("code error mqtt not implemented Send packet=%v", p)
	}
	topic := fmt.Sprintf("vm%d/r/c", p.VmId)
	err := self.m.Publish(&packet.Message{
		Topic:   topic,
		Payload: p.Payload,
		QOS:     packet.QOSAtLeastOnce,
		Retain:  false,
	})
	if err != nil {
		err = errors.Annotatef(err, "tele.Send topic=%s", topic)
		self.log.Error(err)
	}
	return err
}

var reTopic = regexp.MustCompile(`^vm(-?\d+)/\w+/(.+)$`)

func (self *transportMqtt) onMessage(msg *packet.Message) error {
	self.alive.Add(1)
	defer self.alive.Done()
	self.log.Debugf("mqtt msg=%v", msg)
	// parseTopic vm13/w/1s
	parts := reTopic.FindStringSubmatch(msg.Topic)
	if len(parts) != 3 {
		return errors.Errorf("invalid topic=%s", msg.Topic)
	}

	packet := Packet{Payload: msg.Payload}

	if x, err := strconv.ParseInt(parts[1], 10, 32); err != nil {
		return errors.Annotatef(err, "invalid topic=%s", msg.Topic)
	} else {
		packet.VmId = int32(x)
	}
	switch parts[2] {
	case "1s":
		packet.Kind = PacketState
	case "1t":
		packet.Kind = PacketTelemetry
	default:
		return errors.Errorf("invalid topic=%s", msg.Topic)
	}

	for {
		t := time.NewTimer(time.Second)
		select {
		case self.pch <- packet:
			t.Stop()
			return nil
		case <-t.C:
			self.log.Errorf("CRITICAL mqtt receive chan full")
		}
	}
}

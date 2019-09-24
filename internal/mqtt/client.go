// Stripped down MQTT client.
// - Init() returns with result of first connect
// - Subscribe once on connect
// - Reconnect forever
// - QOS 0,1
// - No in-flight storage (except Publish call stack)
// - No concurrent Publish (serialized)
package mqtt

import (
	"crypto/tls"
	"io"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/256dpi/gomqtt/client"
	"github.com/256dpi/gomqtt/packet"
	"github.com/256dpi/gomqtt/transport"
	"github.com/juju/errors"
	"github.com/temoto/alive"
	"github.com/temoto/vender/log2"
)

type Client struct { //nolint:maligned
	sync.Mutex

	Config struct {
		NetworkTimeout time.Duration
		BrokerURL      string
		KeepaliveSec   uint16
		TLS            *tls.Config
		ClientID       string
		Password       string
		Subscriptions  []packet.Subscription
		OnMessage      func(*packet.Message) error
	}
	Log *log2.Log

	alive   *alive.Alive
	conn    transport.Conn
	dialer  transport.Dialer
	lastID  uint32
	pubmu   sync.Mutex // serialize public API Publish()
	tracker *client.Tracker

	flowConnect struct {
		ch     chan *packet.Connack
		packet *packet.Connect
		state  uint32
	}
	flowPublish struct {
		wake  chan struct{}
		id    packet.ID
		state uint32
	}
	flowSubscribe struct {
		ch    chan *packet.Suback
		state uint32
	}
}

const (
	clientInitialized uint32 = iota
	clientConnecting
	clientConnacked
	clientConnected
	clientDisconnecting
	clientDisconnected
)

const (
	publishNew uint32 = iota
	publishSent
	publishAck
)

func (c *Client) Init() error {
	c.alive = alive.NewAlive()

	if _, err := url.ParseRequestURI(c.Config.BrokerURL); err != nil {
		return errors.Annotatef(err, "mqtt dial broker=%s", c.Config.BrokerURL)
	}

	c.dialer.TLSConfig = c.Config.TLS
	c.lastID = uint32(time.Now().UnixNano())
	c.tracker = client.NewTracker(time.Duration(c.Config.KeepaliveSec) * time.Second)

	c.flowConnect.ch = make(chan *packet.Connack)
	c.flowConnect.packet = packet.NewConnect()
	c.flowConnect.packet.ClientID = c.Config.ClientID
	c.flowConnect.packet.KeepAlive = uint16(c.Config.KeepaliveSec + 1)
	c.flowConnect.packet.CleanSession = true
	c.flowConnect.packet.Username = c.Config.ClientID
	c.flowConnect.packet.Password = c.Config.Password
	// c.flowConnect.packet.Will = config.WillMessage
	c.flowConnect.state = clientInitialized

	c.flowPublish.wake = make(chan struct{})
	c.flowPublish.state = publishNew

	c.flowSubscribe.ch = make(chan *packet.Suback)
	c.flowSubscribe.state = 0

	ech := make(chan error)
	go c.worker(ech)
	return <-ech
}

func (c *Client) Close() error {
	c.Log.Debugf("mqtt.Close")
	c.Lock()
	defer c.Unlock()

	var err error
	if atomic.LoadUint32(&c.flowConnect.state) >= clientConnecting {
		err = c.disconnect(nil)
	}

	c.alive.Stop()
	c.alive.Wait()
	return err
}

func (c *Client) Publish(msg *packet.Message) error {
	if msg.QOS >= packet.QOSExactlyOnce {
		panic("code error QOS ExactlyOnce not implemented")
	}
	for {
		if atomic.LoadUint32(&c.flowConnect.state) == clientConnected {
			break
		}
		// return client.ErrClientNotConnected
		time.Sleep(time.Second) // TODO observe flowConnect
	}
	c.pubmu.Lock()
	defer c.pubmu.Unlock()

	publish := packet.NewPublish()
	publish.Message = *msg
	if msg.QOS >= packet.QOSAtLeastOnce {
		publish.ID = c.nextID()
	}

	atomic.StoreUint32(&c.flowPublish.state, publishNew)
	// Mutex for id assign and to avoid race like this:
	// send() finished, but before state=Sent
	// receive() gets PUBACK, reads state==New
	c.Lock()
	c.flowPublish.id = publish.ID
	err := c.send(publish)
	atomic.StoreUint32(&c.flowPublish.state, publishSent)
	c.Unlock()
	if err != nil {
		return errors.Annotate(err, "mqtt Publish send")
	}
	if msg.QOS == packet.QOSAtMostOnce {
		return nil
	}

	select {
	case <-c.flowPublish.wake:
		atomic.StoreUint32(&c.flowPublish.state, publishNew)
		return nil

	case <-time.After(c.Config.NetworkTimeout):
		// TODO resend with DUP
		err = errors.Timeoutf("mqtt Publish ack")
		return c.disconnect(err)
	}
}

func (c *Client) connect() error {
	connectTimeout := c.Config.NetworkTimeout * 2

	c.Lock()
	defer c.Unlock()
	state := atomic.LoadUint32(&c.flowConnect.state)
	switch state {
	case clientInitialized, clientDisconnected: // success path

	case clientConnected:
		return nil

	case clientConnecting:
		return client.ErrClientAlreadyConnecting

	default:
		return c.disconnect(errors.Errorf("code error mqtt connect() with state=%d", state))
	}

	atomic.StoreUint32(&c.flowConnect.state, clientConnecting)
	conn, err := c.dialer.Dial(c.Config.BrokerURL)
	if err != nil {
		return errors.Annotatef(err, "mqtt dial broker=%s", c.Config.BrokerURL)
	}
	c.conn = conn
	if err := c.send(c.flowConnect.packet); err != nil {
		return err
	}
	c.alive.Add(2)
	go c.pinger()
	go c.reader()
	select {
	case connack := <-c.flowConnect.ch:
		c.Log.Debugf("mqtt CONNACK=%v", connack)
		// return connection denied error and close connection if not accepted
		if connack.ReturnCode != packet.ConnectionAccepted {
			return c.fatal(client.ErrClientConnectionDenied)
		}
		atomic.StoreUint32(&c.flowConnect.state, clientConnected)
	case <-time.After(connectTimeout):
		err := errors.Timeoutf("mqtt connect")
		// Server doesn't know about timeout
		return c.disconnect(err)
	}
	c.Log.Debugf("mqtt connect success")
	return nil
}

func (c *Client) disconnect(err error) error {
	atomic.StoreUint32(&c.flowConnect.state, clientDisconnected)
	connErr := c.conn.Close()
	if connErr != nil {
		c.Log.Errorf("mqtt conn.Close err=%v", connErr)
		if err == nil {
			err = connErr
		}
	}
	return err
}

func (c *Client) fatal(err error) error {
	err2 := c.disconnect(err)
	c.alive.Stop()
	return err2
}

func (c *Client) nextID() packet.ID {
	u32 := atomic.AddUint32(&c.lastID, 1)
	return packet.ID(u32 % (1 << 16))
}

// manages the sending of ping packets to keep the connection alive
func (c *Client) pinger() {
	defer c.alive.Done()
	stopch := c.alive.StopChan()
	for {
		if atomic.LoadUint32(&c.flowConnect.state) == clientDisconnected {
			return
		}

		window := c.tracker.Window()
		if window < 0 {
			if c.tracker.Pending() {
				_ = c.disconnect(client.ErrClientMissingPong)
				return
			}

			err := c.send(packet.NewPingreq())
			if err != nil {
				// TODO retry
				c.Log.Errorf("mqtt pinger send err=%v", err)
			} else {
				c.tracker.Ping()
			}
		} else {
			c.Log.Debugf("mqtt KeepAlive delay=%s", window.String())
		}

		select {
		case <-stopch:
			return
		case <-time.After(window):
			continue
		}
	}
}

func (c *Client) reader() {
	defer c.alive.Done()
	for c.alive.IsRunning() {
		if atomic.LoadUint32(&c.flowConnect.state) == clientDisconnected {
			return
		}

		// get next packet from connection
		pkt, err := c.conn.Receive()
		switch err {
		case nil: // success path

		case io.EOF: // server closed connection
			c.Log.Errorf("mqtt server closed connection")
			atomic.StoreUint32(&c.flowConnect.state, clientDisconnected)
			c.conn.Close()
			return

		default:
			// ignore errors while disconnecting
			if atomic.LoadUint32(&c.flowConnect.state) >= clientDisconnecting {
				return
			}
			c.Log.Errorf("mqtt receive err=%v", err)
			_ = c.disconnect(err)
			return
		}
		c.Log.Debugf("mqtt received=%s", pkt.String())

		// call handlers for packet types and ignore other packets
		switch typedPkt := pkt.(type) {
		case *packet.Connack:
			if atomic.CompareAndSwapUint32(&c.flowConnect.state, clientConnecting, clientConnacked) {
				c.flowConnect.ch <- typedPkt
			} else {
				c.Log.Errorf("mqtt ignore stray CONNACK")
			}

		case *packet.Suback:
			if atomic.CompareAndSwapUint32(&c.flowSubscribe.state, 1, 0) {
				c.flowSubscribe.ch <- typedPkt
			}

		case *packet.Pingresp:
			c.tracker.Pong()
		case *packet.Publish:
			c.receivePublish(typedPkt)
		case *packet.Puback:
			c.receivePuback(typedPkt.ID)
		}
	}
}

func (c *Client) receivePublish(publish *packet.Publish) {
	// call callback for unacknowledged and directly acknowledged messages
	if publish.Message.QOS <= packet.QOSAtLeastOnce {
		err := c.Config.OnMessage(&publish.Message)
		if err != nil {
			c.Log.Errorf("mqtt onMessage topic=%s payload=%x err=%v", publish.Message.Topic, publish.Message.Payload, err)
			_ = c.disconnect(err)
			return
		}
	}

	if publish.Message.QOS == packet.QOSAtLeastOnce {
		puback := packet.NewPuback()
		puback.ID = publish.ID
		err := c.send(puback)
		if err != nil {
			// TODO retry send()
			_ = c.disconnect(err)
			return
		}
	}

	if publish.Message.QOS == packet.QOSExactlyOnce {
		panic("code error qos=2 not implemented")
	}
}

func (c *Client) receivePuback(id packet.ID) {
	c.Lock()
	defer c.Unlock()
	if c.flowPublish.state != publishSent {
		c.Log.Errorf("mqtt stray puback id=%d state=%d", id, c.flowPublish.state)
		return
	}
	if c.flowPublish.id != id {
		// given no concurrent publish flow of this code, PUBACK for unexpected id is severe error
		_ = c.disconnect(errors.Errorf("puback id=%d expected=%d", id, c.flowPublish.id))
		return
	}
	atomic.StoreUint32(&c.flowPublish.state, publishAck)
	c.flowPublish.wake <- struct{}{}
}

func (c *Client) send(pkt packet.Generic) error {
	c.tracker.Reset()

	err := c.conn.Send(pkt, false)
	if err != nil {
		return err
	}

	c.Log.Debugf("mqtt sent=%s", pkt.String())
	return nil
}

func (c *Client) subscribe(subs []packet.Subscription) error {
	if atomic.LoadUint32(&c.flowConnect.state) != clientConnected {
		return client.ErrClientNotConnected
	}

	sub := &packet.Subscribe{
		ID:            c.nextID(),
		Subscriptions: subs,
	}
	atomic.StoreUint32(&c.flowSubscribe.state, 1)
	if err := c.send(sub); err != nil {
		return errors.Annotate(err, "subscribe")
	}

	select {
	case suback := <-c.flowSubscribe.ch:
		for _, code := range suback.ReturnCodes {
			if code == packet.QOSFailure {
				return client.ErrFailedSubscription
			}
		}
		return nil
	case <-time.After(c.Config.NetworkTimeout):
		return c.disconnect(errors.Timeoutf("mqtt subscribe"))
	}
}

func (c *Client) worker(initChan chan<- error) {
	first := true
	for c.alive.IsRunning() {
		err := c.connect()
		if err == nil {
			err = c.subscribe(c.Config.Subscriptions)
			if err != nil {
				err = errors.Annotate(err, "mqtt worker subscribe")
			} else {
				c.Log.Debugf("mqtt subscribe success")
			}
		} else {
			c.Log.Errorf("mqtt worker connect err=%v", err)
		}
		if first {
			first = false
			initChan <- err
		}
		c.alive.WaitTasks()
	}
	_ = c.disconnect(nil)
}

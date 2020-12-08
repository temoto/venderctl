// Separate package is workaround to import cycles.
package tele_config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io/ioutil"

	"github.com/juju/errors"
)

type Config struct { //nolint:maligned
	ExecOnState string `hcl:"exec_on_state"`

	LogDebug     bool `hcl:"log_debug"`
	MqttLogDebug bool `hcl:"mqtt_log_debug"`

	Connect *Connect `hcl:"connect"`

	Listens []Listen `hcl:"listen"`

	RoleMembersAdmin   []string `hcl:"role_admin"`
	RoleMembersControl []string `hcl:"role_control"`
	RoleMembersMonitor []string `hcl:"role_monitor"`
	SecretsPath        string   `hcl:"secrets"`

	Mode Mode `hcl:"-"`
}

type Mode string

const (
	ModeDisabled Mode = ""
	ModeClient   Mode = "client"
	ModeServer   Mode = "server"
	ModeTax		 Mode = "tax"
	ModeCommand  Mode = "command"
	ModeSponge   Mode = "sponge"
)

type Connect struct { //nolint:maligned
	ClientID          string `hcl:"clientid"`
	URL               string `hcl:"url"`
	TLS               TLS    `hcl:"tls"`
	KeepaliveSec      int    `hcl:"keepalive_sec"`
	NetworkTimeoutSec int    `hcl:"network_timeout_sec"`
}

type Listen struct { //nolint:maligned
	URL               string   `hcl:"url,key"`
	TLS               TLS      `hcl:"tls"`
	KeepaliveSec      int      `hcl:"keepalive_sec"`
	NetworkTimeoutSec int      `hcl:"network_timeout_sec"`
	AllowRoles        []string `hcl:"allow_roles"`
}

type TLS struct { //nolint:maligned
	CaFile   string `hcl:"ca_file"`
	PSK      string `hcl:"psk"` // secret
	CertFile string `hcl:"cert_file"`
	KeyFile  string `hcl:"key_file"`
}

func (c *Config) SetMode(m string) {
	switch m {
	case "sponge":
		c.Mode = ModeSponge
	default:
		c.Mode = ModeDisabled
	}
}

// [a, b], b -> true
// [a, b], c -> false
// [_all], * -> true
func isAllowed(allows []string, client Role) bool {
	for _, a := range allows {
		if a == string(RoleAll) || a == string(client) {
			return true
		}
	}
	return false
}

// Prepare Config in client mode. Use endpoint Connect if available or first Listen allowing the role.
func (c *Config) EnableClient(role Role) error {
	c.Mode = ModeClient
	if len(c.Listens) == 0 && c.Connect == nil {
		return fmt.Errorf("no listen or connect endpoints configured")
	}
	if c.Connect != nil {
		return nil
	}
	for _, l := range c.Listens {
		if isAllowed(l.AllowRoles, role) {
			c.Connect = &Connect{
				URL:               l.URL,
				KeepaliveSec:      l.KeepaliveSec,
				NetworkTimeoutSec: l.NetworkTimeoutSec,
				TLS:               TLS{PSK: l.TLS.PSK},
			}
			return nil
		}
	}
	return fmt.Errorf("connect is not configured, no listen endpoints allow role=%s", role)
}

func (c *Config) EnableServer() error {
	c.Mode = ModeServer
	if len(c.Listens) == 0 {
		return fmt.Errorf("no listen endpoints configured")
	}
	return nil
}

func (c *Config) Disable() { c.Mode = ModeDisabled }

func (t *TLS) TLSConfig() (*tls.Config, error) {
	c := &tls.Config{
		NextProtos: []string{"mqtt"},
	}
	if t.CaFile != "" {
		c.RootCAs = x509.NewCertPool()
		cabytes, err := ioutil.ReadFile(t.CaFile)
		if err != nil {
			return nil, errors.Annotate(err, "CaFile read")
		}
		c.RootCAs.AppendCertsFromPEM(cabytes)
	}
	if t.CertFile != "" && t.KeyFile != "" {
		var err error
		c.Certificates = make([]tls.Certificate, 1)
		c.Certificates[0], err = tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, errors.Annotate(err, "Cert/KeyFile read")
		}
	}
	if t.PSK != "" {
		b, err := base64.RawStdEncoding.DecodeString(t.PSK)
		if err != nil {
			return nil, errors.Annotate(err, "PSK base64 decode")
		}
		copy(c.SessionTicketKey[:], b)
	}
	return c, nil
}

// Separate package is workaround to import cycles.
package tele_config

type Config struct { //nolint:maligned
	Enable            bool   `hcl:"enable"`
	LogDebug          bool   `hcl:"log_debug"`
	KeepaliveSec      int    `hcl:"keepalive_sec"`
	MqttBroker        string `hcl:"mqtt_broker"`
	MqttLogDebug      bool   `hcl:"mqtt_log_debug"`
	MqttClientId      string `hcl:"mqtt_clientid"`
	MqttPassword      string `hcl:"mqtt_password"` // secret
	NetworkTimeoutSec int    `hcl:"network_timeout_sec"`
	TlsCaFile         string `hcl:"tls_ca_file"`
	TlsPsk            string `hcl:"tls_psk"` // secret

	MqttSubscribe []string
}

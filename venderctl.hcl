db {
  // github.com/go-pg/pg ParseURL
  url = "postgresql://user:password@server/database"
}

money {
  // Multiple of lowest money unit for config convenience and formatting.
  // All money numbers in config are multipled by scale.
  // For USD/EUR set `scale=1` to work with cents.
  scale = 100
}

sponge {
  // Only these variables passed as environment:
  // db_updated=true  if state successfully written to database
  // vmid=123         int32
  // new=2            integer, current state, see tele.proto enum State
  // prev=1           previous state, 0 means problem or just new vmid
  exec_on_state = "/usr/local/bin/vender-on-state"
}

tele {
  // sponge forces enable=true
  enable = false

  log_debug      = false // debug venderctl/tele
  mqtt_log_debug = false // debug MQTT library

  network_timeout_sec = 3
  keepalive_sec       = 3 // default is network_timeout_sec

  mqtt_broker   = "tls://user@server:port"
  mqtt_clientid = "client"
  mqtt_password = "password"
  tls_ca_file   = "/.../ca.pem"
  tls_psk       = ""
}

include "venderctl-local.hcl" {
  optional = true
}

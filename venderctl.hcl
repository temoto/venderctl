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

tax {
  ru2019 {
    tag1018 = ""
    tag1055 = 2
    tag1199 = 6

    umka {
      base_url = "http://70:70@office.armax.ru:58088"
    }
  }
}

tele {
  // Only these variables passed as environment:
  // db_updated=true  if state successfully written to database
  // vmid=123         int32
  // new=2            integer, current state, see tele.proto enum State
  // prev=1           previous state, 0 means problem or just new vmid
  exec_on_state = "/usr/local/bin/vender-on-state"

  log_debug      = false // debug venderctl/tele
  mqtt_log_debug = false // debug MQTT library

  role_admin   = ["admin"]
  role_control = ["ctl"]
  role_monitor = ["prometheus"]
  secrets      = "/etc/venderctl/tele.secrets"

  listen "tls://[addr]:port" {
    allow_roles = ["control"]

    network_timeout_sec = 5

    tls {
      ca_file   = "/ca.pem"
      cert_file = "/server.pem"
      key_file  = "/server.key"
      psk       = ""            // secret
    }
  }

  listen "unix:///run/venderctl" {
    allow_roles         = ["_all"]
    network_timeout_sec = 1
  }

  connect {
    url                 = "tcp://ctl:secret@internal"
    network_timeout_sec = 5
  }
}

include "venderctl-local.hcl" {
  optional = true
}

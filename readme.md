# What

Open source vending machine data processing server. The backend for https://github.com/temoto/vender

Goals:
- [cmd/sponge] receive telemetry from Vender VMC
- send remote control commands to vending machines
- load telemetry data into existing legacy dashboard
- send reports to government fiscal agency
- (maybe) new dashboard, alerts


# Flair

[![Build status](https://travis-ci.org/temoto/venderctl.svg?branch=master)](https://travis-ci.org/temoto/venderctl)
[![Coverage](https://codecov.io/gh/temoto/venderctl/branch/master/graph/badge.svg)](https://codecov.io/gh/temoto/venderctl)
[![Go Report Card](https://goreportcard.com/badge/github.com/temoto/venderctl)](https://goreportcard.com/report/github.com/temoto/venderctl)

#Config file
sponge {
  // env
  // db_updated - database update (boolean)
  // vmid - vmc id (number)
  // new - current state (number)
  // prev - preview state (number)
  exec_on_state = "/usr/local/bin/vender-on-state"
}

money {
  scale = 100
}

db {
    url = "postgresql://user:password@server/database"
}

tele {
  enable = true
  log_debug = true
  mqtt_log_debug = true
  mqtt_broker = "tls://user@server:port"
  mqtt_clientid = "user"
  mqtt_password = "password"
  tls_ca_file = "/../ca.crt"
}

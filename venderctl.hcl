db {
  url = "postgresql://EDIT_ME"
}

money {
  // Multiple of lowest money unit for config convenience and formatting.
  // All money numbers in config are multipled by scale.
  // For USD/EUR set `scale=1` to work with cents.
  scale = 100
}

sponge {
  exec_on_state = "/usr/local/bin/vender-on-state"
}

include "venderctl-local.hcl" {
  optional = true
}

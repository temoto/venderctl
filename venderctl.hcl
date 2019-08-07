money {
  // Multiple of lowest money unit for config convenience and formatting.
  // All money numbers in config are multipled by scale.
  // For USD/EUR set `scale=1` to work with cents.
  scale = 100
}

include "venderctl-local.hcl" {
  optional = true
}

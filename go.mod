module github.com/temoto/venderctl

go 1.16

require (
	github.com/256dpi/gomqtt v0.14.1 // indirect
	github.com/coreos/go-systemd v0.0.0-20190719114852-fd7a80b32e1f
	github.com/eclipse/paho.mqtt.golang v1.3.5
	github.com/go-pg/pg/v9 v9.0.3
	github.com/golang/protobuf v1.5.2
	github.com/hashicorp/hcl v1.0.0
	github.com/juju/errors v0.0.0-20190930114154-d42613fe1ab9
	github.com/stretchr/testify v1.5.1
	github.com/temoto/alive/v2 v2.0.0
	github.com/temoto/ru-nalog-go v0.6.1-0.20200126230605-d6d5bfc79675
	github.com/temoto/vender v0.210504.1-0.20210704094029-968f6a03f7fe
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9 // indirect
	golang.org/x/net v0.0.0-20210614182718-04defd469f4e // indirect
	google.golang.org/protobuf v1.27.1 // indirect
	gopkg.in/hlandau/easymetric.v1 v1.0.0 // indirect
	gopkg.in/hlandau/measurable.v1 v1.0.1 // indirect
	gopkg.in/hlandau/passlib.v1 v1.0.10
)

replace github.com/temoto/vender => /home/alexm/vender

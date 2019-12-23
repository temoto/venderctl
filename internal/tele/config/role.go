package tele_config

type Role string

const (
	RoleInvalid Role = ""
	RoleAll     Role = "_all"
	RoleAdmin   Role = "admin"
	RoleControl Role = "control"
	RoleMonitor Role = "monitor"
	RoleVender  Role = "vender"
)

package flag

import (
	"github.com/peterbourgon/ff/v4/ffval"
	"github.com/tinkerbell/tinkerbell/pkg/backend/kube"
	"github.com/tinkerbell/tinkerbell/tootles"
)

type TootlesConfig struct {
	Config   *tootles.Config
	LogLevel int
}

var KubeIndexesTootles = map[kube.IndexType]kube.Index{
	kube.IndexTypeIPAddr:   kube.Indexes[kube.IndexTypeIPAddr],
	kube.IndexTypeLookupID: kube.Indexes[kube.IndexTypeLookupID],
}

func RegisterTootlesFlags(fs *Set, h *TootlesConfig) {
	fs.Register(TootlesDebugMode, ffval.NewValueDefault(&h.Config.DebugMode, h.Config.DebugMode))
	fs.Register(TootlesLogLevel, ffval.NewValueDefault(&h.LogLevel, h.LogLevel))
	fs.Register(TootlesInstanceEndpoint, ffval.NewValueDefault(&h.Config.InstanceEndpoint, h.Config.InstanceEndpoint))
}

var TootlesDebugMode = Config{
	Name:  "tootles-debug-mode",
	Usage: "whether to run Tootles in debug mode",
}

var TootlesLogLevel = Config{
	Name:  "tootles-log-level",
	Usage: logLevelUsage,
}

var TootlesInstanceEndpoint = Config{
	Name:  "tootles-instance-endpoint",
	Usage: "whether to enable the /tootles/instanceID/<instanceID> and /nocloud/<lookupID> endpoints that are independent from client IP address",
}

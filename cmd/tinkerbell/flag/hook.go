package flag

import (
	"github.com/peterbourgon/ff/v4/ffval"
	"github.com/tinkerbell/tinkerbell/hook"
	ntip "github.com/tinkerbell/tinkerbell/pkg/flag/netip"
)

type HookConfig struct {
	Config   *hook.Config
	LogLevel int
}

func RegisterHookFlags(fs *Set, hc *HookConfig) {
	fs.Register(HookImagePath, ffval.NewValueDefault(&hc.Config.ImagePath, hc.Config.ImagePath))
	fs.Register(HookVersion, ffval.NewValueDefault(&hc.Config.Version, hc.Config.Version))
	fs.Register(HookDownloadTimeout, ffval.NewValueDefault(&hc.Config.DownloadTimeout, hc.Config.DownloadTimeout))
	fs.Register(HookHTTPAddr, &ntip.AddrPort{AddrPort: &hc.Config.HTTPAddr})
	fs.Register(HookEnableHTTPServer, ffval.NewValueDefault(&hc.Config.EnableHTTPServer, hc.Config.EnableHTTPServer))
	fs.Register(HookLogLevel, ffval.NewValueDefault(&hc.LogLevel, hc.LogLevel))
}

var HookImagePath = Config{
	Name:  "hook-image-path",
	Usage: "[hook] directory path where hook images are stored",
}

var HookVersion = Config{
	Name:  "hook-version",
	Usage: "[hook] hook version to download (latest, v1.2.3, etc.)",
}

var HookDownloadTimeout = Config{
	Name:  "hook-download-timeout",
	Usage: "[hook] timeout for downloading hook archives",
}

var HookHTTPAddr = Config{
	Name:  "hook-http-addr",
	Usage: "[hook] address and port for the HTTP file server",
}

var HookEnableHTTPServer = Config{
	Name:  "hook-enable-http-server",
	Usage: "[hook] enable the HTTP file server",
}

var HookLogLevel = Config{
	Name:  "hook-log-level",
	Usage: "[hook] log level for hook service",
}

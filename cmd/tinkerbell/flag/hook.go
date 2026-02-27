package flag

import (
	"github.com/peterbourgon/ff/v4/ffval"
	"github.com/tinkerbell/tinkerbell/hook"
)

type HookConfig struct {
	Config    *hook.Config
	LogLevel  int
	DebugMode bool
}

func RegisterHookFlags(fs *Set, hc *HookConfig) {
	fs.Register(HookDebugMode, ffval.NewValueDefault(&hc.DebugMode, hc.DebugMode))
	fs.Register(HookLogLevel, ffval.NewValueDefault(&hc.LogLevel, hc.LogLevel))
	fs.Register(HookURLPrefix, ffval.NewValueDefault(&hc.Config.URLPrefix, hc.Config.URLPrefix))

	fs.Register(HookImagePath, ffval.NewValueDefault(&hc.Config.ImagePath, hc.Config.ImagePath))

	fs.Register(HookOCIRegistry, ffval.NewValueDefault(&hc.Config.OCIRegistry, hc.Config.OCIRegistry))
	fs.Register(HookOCIRepository, ffval.NewValueDefault(&hc.Config.OCIRepository, hc.Config.OCIRepository))
	fs.Register(HookOCIReference, ffval.NewValueDefault(&hc.Config.OCIReference, hc.Config.OCIReference))
	fs.Register(HookOCIUsername, ffval.NewValueDefault(&hc.Config.OCIUsername, hc.Config.OCIUsername))
	fs.Register(HookOCIPassword, ffval.NewValueDefault(&hc.Config.OCIPassword, hc.Config.OCIPassword))
	fs.Register(HookPullTimeout, ffval.NewValueDefault(&hc.Config.PullTimeout, hc.Config.PullTimeout))
}

var HookDebugMode = Config{
	Name:  "hook-debug-mode",
	Usage: "[hook] whether to run hook service in debug mode",
}

var HookLogLevel = Config{
	Name:  "hook-log-level",
	Usage: "[hook] log level for hook service",
}

var HookURLPrefix = Config{
	Name:  "hook-url-prefix",
	Usage: "[hook] URL path prefix for serving hook files (e.g., /hooks/)",
}

var HookImagePath = Config{
	Name:  "hook-image-path",
	Usage: "[hook] directory path where hook images are stored",
}

var HookOCIRegistry = Config{
	Name:  "hook-oci-registry",
	Usage: "[hook] OCI registry URL (e.g., ghcr.io, docker.io)",
}

var HookOCIRepository = Config{
	Name:  "hook-oci-repository",
	Usage: "[hook] OCI repository path (e.g., tinkerbell/hook)",
}

var HookOCIReference = Config{
	Name:  "hook-oci-reference",
	Usage: "[hook] OCI image reference - tag or digest (e.g., latest, v1.2.3, sha256:...)",
}

var HookOCIUsername = Config{
	Name:  "hook-oci-username",
	Usage: "[hook] optional username for OCI registry authentication",
}

var HookOCIPassword = Config{
	Name:  "hook-oci-password",
	Usage: "[hook] optional password for OCI registry authentication",
}

var HookPullTimeout = Config{
	Name:  "hook-pull-timeout",
	Usage: "[hook] timeout for pulling OCI images",
}

//go:build embedded

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ccoveille/go-safecast/v2"
	"github.com/peterbourgon/ff/v4"
	"github.com/spf13/pflag"
	"github.com/tinkerbell/tinkerbell/apiserver"
	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
	"go.etcd.io/etcd/server/v3/embed"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Store pflag.FlagSet reference for kube-apiserver so we can configure it later
var apiserverPFlagSet *pflag.FlagSet

func init() {
	// defaults for etcd
	ecfg := embed.NewConfig()
	ecfg.Dir = "/tmp/default.etcd"
	ec := &flag.EmbeddedEtcdConfig{
		Config:             ecfg,
		WaitHealthyTimeout: time.Minute,
	}
	// register flags
	kaffs := ff.NewFlagSet("embedded kube-apiserver")
	kafs := &flag.Set{FlagSet: kaffs}
	kac := &flag.EmbeddedKubeAPIServerConfig{}
	flag.RegisterKubeAPIServer(kafs, kac)
	efs := ff.NewFlagSet("embedded etcd").SetParent(kaffs)
	flag.RegisterEtcd(&flag.Set{FlagSet: efs}, ec)
	embeddedFlagSet = efs
	apiserverPFS, runFunc := apiserver.ConfigAndFlags(&kac.DisableLogging)
	apiserverPFlagSet = apiserverPFS // Store for later configuration
	apiserverPFS.VisitAll(kubeAPIServerFlags(kaffs))

	// register the run command
	embeddedApiserverExecute = runFunc
	embeddedKubeControllerManagerExecute = apiserver.Kubecontrollermanager
	embeddedEtcdExecute = func(ctx context.Context, logLevel int) error {
		ll := ternary((logLevel != 0), logLevel, ec.LogLevel)
		log := zapLogger(ll)
		if ec.DisableLogging {
			log = zap.NewNop()
		}
		ec.Config.ZapLoggerBuilder = embed.NewZapLoggerBuilder(log)
		e, err := embed.StartEtcd(ec.Config)
		if err != nil {
			return fmt.Errorf("failed to start etcd: %w", err)
		}
		defer e.Close()
		select {
		case <-e.Server.ReadyNotify():
			log.Info("etcd server is ready")
		case <-time.After(ec.WaitHealthyTimeout):
			e.Server.Stop() // trigger a shutdown
			return fmt.Errorf("server took too long to become healthy")
		case <-ctx.Done():
			e.Server.Stop() // trigger a shutdown
			log.Info("context cancelled waiting for etcd to become healthy")
			return nil
		}
		<-ctx.Done()
		e.Server.Stop()
		return nil
	}
}

func kubeAPIServerFlags(kaffs *ff.FlagSet) func(*pflag.Flag) {
	return func(f *pflag.Flag) {
		// help and v already exist in the global flags defined above so we skip them
		// here to avoid duplicate flag errors.
		// bind-address, tls-cert-file and tls-key-file are also defined in global flags,
		// so we skip them here to avoid duplicate flag errors.
		// These will be configured via pflag.Set() from globals before starting kube-apiserver.
		if f.Name == "help" || f.Name == "v" || f.Name == "bind-address" || f.Name == "tls-cert-file" || f.Name == "tls-key-file" {
			return
		}
		fc := ff.FlagConfig{
			LongName: f.Name,
			Usage:    f.Usage,
			Value:    f.Value,
		}
		// feature-gates has a lot of output and includes a lot of '\n' characters
		// that makes the ffhelp output not output all the flags. We remove all the
		// feature gates so that all the flags are output in the help.
		if f.Name == "feature-gates" {
			lines := strings.Split(f.Usage, "\n")
			newlines := make([]string, 0)
			for _, line := range lines {
				if !strings.HasPrefix(line, "kube:") {
					newlines = append(newlines, line)
				}
			}
			fc.Usage = strings.Join(newlines, "\n")
		}

		if len([]rune(f.Shorthand)) > 0 {
			fc.ShortName = []rune(f.Shorthand)[0]
		}

		if _, err := kaffs.AddFlag(fc); err != nil {
			fmt.Printf("error adding flag: %v\n", err)
		}
	}
}

// zapLogger is used by embedded etcd. It's the only logger that embedded etcd supports.
func zapLogger(level int) *zap.Logger {
	config := zap.NewProductionConfig()
	config.OutputPaths = []string{"stdout"}
	l, err := safecast.Convert[int8](level)
	if err != nil {
		l = 0
	}
	config.Level = zap.NewAtomicLevelAt(zapcore.Level(-l))
	config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)
	config.EncoderConfig.TimeKey = "time"
	config.EncoderConfig.EncodeLevel = func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(fmt.Sprintf("%d", l))
	}
	config.EncoderConfig.NameKey = "logger"
	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	return logger.Named("etcd")
}

// ConfigureKubeAPIServerFromGlobals sets kube-apiserver flags from global config.
// This must be called after global flag parsing but before starting the API server.
func ConfigureKubeAPIServerFromGlobals(bindAddr, tlsCertFile, tlsKeyFile string) error {
	if apiserverPFlagSet == nil {
		return fmt.Errorf("kube-apiserver pflag set not initialized")
	}

	if bindAddr != "" {
		if err := apiserverPFlagSet.Set("bind-address", bindAddr); err != nil {
			return fmt.Errorf("failed to set bind-address: %w", err)
		}
	}

	if tlsCertFile != "" {
		if err := apiserverPFlagSet.Set("tls-cert-file", tlsCertFile); err != nil {
			return fmt.Errorf("failed to set tls-cert-file: %w", err)
		}
	}

	if tlsKeyFile != "" {
		if err := apiserverPFlagSet.Set("tls-key-file", tlsKeyFile); err != nil {
			return fmt.Errorf("failed to set tls-key-file: %w", err)
		}
	}

	return nil
}

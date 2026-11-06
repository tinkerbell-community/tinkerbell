package main

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
	"github.com/tinkerbell/tinkerbell/crd"
	"github.com/tinkerbell/tinkerbell/pkg/conversionwebhook"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// certRenewInterval is how often a running instance checks whether the
// webhook's serving certificate needs renewing.
const certRenewInterval = 12 * time.Hour

// startConversionWebhook loads (or creates) the conversion webhook's
// certificates, starts serving the webhook in g, and returns how the API
// server reaches it. The listener is bound before it returns, so the CRDs can
// point at the webhook right away.
func startConversionWebhook(ctx context.Context, g *errgroup.Group, log logr.Logger, cfg *rest.Config, c flag.ConversionWebhookConfig, podNS string, embeddedAPIServer bool) (crd.ConversionWebhook, error) {
	ns := cmp.Or(c.ServiceNamespace, podNS, "default")
	hook := crd.ConversionWebhook{
		ServiceNamespace: ns,
		ServiceName:      c.ServiceName,
		ServicePort:      int32(c.ServicePort), //nolint:gosec // a port number
		Path:             conversionwebhook.Path,
		URL:              c.URL,
	}
	if hook.URL == "" && embeddedAPIServer {
		// The embedded API server has no Service network; it reaches us directly.
		_, port, err := net.SplitHostPort(c.BindAddr)
		if err != nil {
			return hook, fmt.Errorf("conversion webhook bind address %q: %w", c.BindAddr, err)
		}
		hook.URL = "https://" + net.JoinHostPort("127.0.0.1", port) + conversionwebhook.Path
	}

	certs := &conversionwebhook.Certs{Namespace: ns, SecretName: c.CertSecretName}
	if hook.URL != "" {
		u, err := url.Parse(hook.URL)
		if err != nil {
			return hook, fmt.Errorf("conversion webhook URL: %w", err)
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			certs.IPs = []net.IP{ip}
		} else {
			certs.DNSNames = []string{u.Hostname()}
		}
	} else {
		svc := c.ServiceName
		certs.DNSNames = []string{svc, svc + "." + ns, svc + "." + ns + ".svc", svc + "." + ns + ".svc.cluster.local"}
	}
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return hook, fmt.Errorf("conversion webhook: %w", err)
	}
	certs.Client = kc
	if hook.CABundle, err = certs.Ensure(ctx); err != nil {
		return hook, fmt.Errorf("conversion webhook certificate: %w", err)
	}

	handler, err := conversionwebhook.NewHandler()
	if err != nil {
		return hook, err
	}
	srv := &conversionwebhook.Server{Addr: c.BindAddr, Handler: handler, GetCertificate: certs.GetCertificate}
	l, err := srv.Listen(ctx)
	if err != nil {
		return hook, err
	}
	g.Go(func() error { return srv.Serve(ctx, l) })
	g.Go(func() error {
		certs.Renew(ctx, certRenewInterval, func(err error) {
			log.Error(err, "renewing the conversion webhook certificate failed; will retry", "secret", ns+"/"+c.CertSecretName)
		})
		return nil
	})

	where := hook.URL
	if where == "" {
		where = "service " + ns + "/" + c.ServiceName + ":" + strconv.Itoa(c.ServicePort)
	}
	log.Info("conversion webhook serving", "addr", l.Addr().String(), "apiServerReachesItAt", where)
	return hook, nil
}

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	bmclib "github.com/bmc-toolbox/bmclib/v2"
	bmclibbmc "github.com/bmc-toolbox/bmclib/v2/bmc"
	"github.com/go-logr/logr"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
)

const (
	// powerActionRequeueAfter is how long to wait before checking again whether a
	// power action reached the power state it asked for.
	powerActionRequeueAfter = 3 * time.Second

	// defaultOperationTimeout bounds how long a started operation may keep being
	// checked (status checks, transient BMC connection errors) before it is failed,
	// when it does not set its own timeout. It is the timeout bmc Tasks always had.
	defaultOperationTimeout = 10 * time.Minute
)

// runOperation performs op on the BMC. These are the bmclib calls bmc Tasks have
// always made for the equivalent v1alpha1 action.
func runOperation(ctx context.Context, logger logr.Logger, op bmc.Operations, bmcClient *bmclib.Client) error {
	if op.PowerAction != nil {
		ok, err := bmcClient.SetPowerState(ctx, op.PowerAction.String())
		if err != nil {
			return fmt.Errorf("failed to perform PowerAction: %w", err)
		}
		md := bmcClient.GetMetadata()
		logger.Info("power state set successfully", "providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider, "ok", ok)

		return nil
	}

	if op.BootDevice != nil {
		ok, err := bmcClient.SetBootDevice(ctx, op.BootDevice.Name.String(), op.BootDevice.Persistent, op.BootDevice.EFIBoot)
		if err != nil || !ok {
			return fmt.Errorf("failed to set BootDevice, ok: %v, err: %w", ok, err)
		}
		md := bmcClient.GetMetadata()
		logger.Info("boot device set successfully", "providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider, "ok", ok)

		return nil
	}

	if op.VirtualMediaAction != nil {
		ok, err := bmcClient.SetVirtualMedia(ctx, op.VirtualMediaAction.Kind.String(), op.VirtualMediaAction.MediaURL)
		if err != nil {
			return fmt.Errorf("failed to perform SetVirtualMedia: %w", err)
		}
		md := bmcClient.GetMetadata()
		logger.Info("virtual media set successfully", "providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider, "ok", ok)

		return nil
	}

	if op.NetworkBootConfig != nil {
		return runNetworkBootConfig(ctx, logger, op.NetworkBootConfig, bmcClient)
	}

	logger.Info("no action specified, nothing to do", "operation", op)

	return errors.New("no action specified, nothing to do")
}

// runNetworkBootConfig applies the independent, individually-optional fields of a
// NetworkBootConfig action: any combination of HTTPBootEnabled/PXEBootEnabled, HTTPBootURL, and
// HTTPBootTLSMode may be set at once. HTTPBootTLSMode is applied before HTTPBootURL so that
// relaxing TLS (e.g. Dell's default "OneWay" mode) takes effect before a plain-HTTP URL is set -
// setting the URL first would have the BMC reject it under the still-active stricter mode.
func runNetworkBootConfig(ctx context.Context, logger logr.Logger, cfg *bmc.NetworkBootConfig, bmcClient *bmclib.Client) error {
	if cfg.HTTPBootEnabled != nil || cfg.PXEBootEnabled != nil {
		ok, err := bmcClient.SetNetworkBootEnabled(ctx, cfg.HTTPBootEnabled, cfg.PXEBootEnabled)
		if err != nil || !ok {
			return fmt.Errorf("failed to set network boot enabled state, ok: %v, err: %w", ok, err)
		}
		md := bmcClient.GetMetadata()
		logger.Info("network boot enabled state set successfully",
			"httpBootEnabled", boolPtrValue(cfg.HTTPBootEnabled),
			"pxeBootEnabled", boolPtrValue(cfg.PXEBootEnabled),
			"providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider)
	}

	if cfg.HTTPBootTLSMode != nil {
		ok, err := bmcClient.SetHTTPBootTLSMode(ctx, bmclibbmc.HTTPBootTLSMode(*cfg.HTTPBootTLSMode))
		if err != nil || !ok {
			return fmt.Errorf("failed to set HTTP boot TLS mode, ok: %v, err: %w", ok, err)
		}
		md := bmcClient.GetMetadata()
		logger.Info("http boot tls mode set successfully", "httpBootTLSMode", *cfg.HTTPBootTLSMode, "providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider)
	}

	if cfg.HTTPBootURL != nil {
		ok, err := bmcClient.SetHTTPBootURI(ctx, *cfg.HTTPBootURL)
		if err != nil || !ok {
			return fmt.Errorf("failed to set HTTP boot URL, ok: %v, err: %w", ok, err)
		}
		md := bmcClient.GetMetadata()
		logger.Info("http boot url set successfully", "providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider, "ok", ok)
	}

	return nil
}

// boolPtrValue returns the pointed-to value for logging, or nil if b is nil, so log lines show
// "true"/"false"/"<nil>" instead of a pointer address.
func boolPtrValue(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// operationDone reports whether op, already performed, has taken effect. Only
// powering on and off can be checked: they are done once the BMC reports the
// power state they asked for. Every other operation is done once performed.
func operationDone(ctx context.Context, logger logr.Logger, op bmc.Operations, bmcClient *bmclib.Client) (bool, error) {
	if op.PowerAction == nil {
		return true, nil
	}
	var want bmcv1.PowerState
	switch *op.PowerAction {
	case bmc.PowerActionOn:
		want = bmcv1.On
	case bmc.PowerActionHardOff, bmc.PowerActionSoftOff:
		want = bmcv1.Off
	default:
		return true, nil
	}

	rawState, err := bmcClient.GetPowerState(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get power state: %w", err)
	}
	logger.Info("power state check", "currentPowerState", rawState)

	return toPowerState(rawState) == want, nil
}

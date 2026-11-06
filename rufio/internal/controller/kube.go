package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmc-toolbox/bmclib/v2/providers/rpc"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// bmcCredentials returns what a ClientFunc needs to open a session to the BMC of
// conn: the username and password from its credentials Secret or, for the RPC
// provider, which needs no credentials, its HMAC secrets.
func bmcCredentials(ctx context.Context, c client.Client, conn *bmc.Connection) (username, password string, opts *BMCOptions, err error) {
	opts = &BMCOptions{ProviderOptions: conn.ProviderOptions}
	if po := conn.ProviderOptions; po != nil && po.RPC != nil {
		if po.RPC.HMAC != nil && len(po.RPC.HMAC.Secrets) > 0 {
			se, err := retrieveHMACSecrets(ctx, c, po.RPC.HMAC.Secrets)
			if err != nil {
				return "", "", nil, fmt.Errorf("unable to get hmac secrets: %w", err)
			}
			opts.rpcSecrets = se
		}
		return "", "", opts, nil
	}

	username, password, err = resolveAuthSecretRef(ctx, c, conn.CredentialsRef)
	if err != nil {
		return "", "", nil, err
	}

	return username, password, opts, nil
}

// resolveAuthSecretRef Gets the Secret from the SecretReference.
// Returns the username and password encoded in the Secret.
func resolveAuthSecretRef(ctx context.Context, c client.Client, secretRef bmc.SimpleReference) (string, string, error) {
	secret := &v1.Secret{}
	key := types.NamespacedName{Namespace: secretRef.Namespace, Name: secretRef.Name}

	if err := c.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", "", fmt.Errorf("secret %s not found: %w", key, err)
		}

		return "", "", fmt.Errorf("failed to retrieve secret %s : %w", key, err)
	}

	username, ok := secret.Data["username"]
	if !ok {
		return "", "", fmt.Errorf("'username' required in Machine secret")
	}

	password, ok := secret.Data["password"]
	if !ok {
		return "", "", fmt.Errorf("'password' required in Machine secret")
	}

	return string(username), string(password), nil
}

func retrieveHMACSecrets(ctx context.Context, c client.Client, hmacSecrets bmc.HMACSecrets) (rpc.Secrets, error) {
	sec := rpc.Secrets{}
	for k, v := range hmacSecrets {
		for _, s := range v {
			secret := &v1.Secret{}
			key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}

			if err := c.Get(ctx, key, secret); err != nil {
				if apierrors.IsNotFound(err) {
					return nil, fmt.Errorf("secret %s not found: %w", key, err)
				}

				return nil, fmt.Errorf("failed to retrieve secret %s : %w", key, err)
			}

			sec[rpc.Algorithm(k)] = append(sec[rpc.Algorithm(k)], string(secret.Data["secret"]))
		}
	}

	return sec, nil
}

// toPowerState takes a raw BMC power state response and converts it to a v1alpha1.PowerState.
func toPowerState(state string) bmcv1.PowerState {
	// Normalize the response string for comparison.
	state = strings.ToLower(state)

	switch {
	case strings.Contains(state, "on"):
		return bmcv1.On
	case strings.Contains(state, "off"):
		return bmcv1.Off
	default:
		return bmcv1.Unknown
	}
}

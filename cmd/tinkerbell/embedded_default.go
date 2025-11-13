//go:build !embedded

package main

func SetKubeAPIServerConfigFromGlobals(bindAddr, tlsCertFile, tlsKeyFile string) error {
	return nil
}

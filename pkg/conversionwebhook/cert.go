package conversionwebhook

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	keyCACert  = "ca.crt"
	keyCAKey   = "ca.key"
	keyTLSCert = corev1.TLSCertKey
	keyTLSKey  = corev1.TLSPrivateKeyKey

	caValidity     = 10 * 365 * 24 * time.Hour
	leafValidity   = 365 * 24 * time.Hour
	renewBefore    = 30 * 24 * time.Hour
	caRenewBefore  = 365 * 24 * time.Hour
	clockSkewSlack = time.Hour
)

// Certs keeps the webhook's serving certificate in a Secret, so that every
// Tinkerbell instance and restart presents a certificate from the same CA, and
// the CA bundle written into the CRDs stays valid.
//
// The Secret holds a self-signed CA and a serving certificate it signed. The
// serving certificate is re-signed by the same CA before it expires, so a
// renewal never changes the CA bundle.
type Certs struct {
	Client     kubernetes.Interface
	Namespace  string
	SecretName string
	// DNSNames and IPs are the names the API server uses to reach the webhook.
	DNSNames []string
	IPs      []net.IP
	// Now returns the current time. It defaults to time.Now.
	Now func() time.Time

	mu   sync.RWMutex
	cert *tls.Certificate
	ca   []byte
}

// Ensure loads the certificates from the Secret, creating or renewing them as
// needed. It returns the PEM encoded CA certificate.
func (c *Certs) Ensure(ctx context.Context) ([]byte, error) {
	for range 3 {
		ca, err := c.ensureOnce(ctx)
		if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
			continue // another instance wrote the Secret first; use theirs
		}
		return ca, err
	}
	return nil, fmt.Errorf("conversion webhook certificate Secret %s/%s keeps changing", c.Namespace, c.SecretName)
}

func (c *Certs) ensureOnce(ctx context.Context) ([]byte, error) {
	secrets := c.Client.CoreV1().Secrets(c.Namespace)
	sec, err := secrets.Get(ctx, c.SecretName, metav1.GetOptions{})
	notFound := apierrors.IsNotFound(err)
	if err != nil && !notFound {
		return nil, fmt.Errorf("reading conversion webhook Secret %s/%s: %w", c.Namespace, c.SecretName, err)
	}

	var data map[string][]byte
	if !notFound {
		data = sec.Data
	}
	updated, changed, err := c.reconcile(data)
	if err != nil {
		return nil, err
	}

	switch {
	case notFound:
		sec = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: c.SecretName, Namespace: c.Namespace,
				Labels: map[string]string{"app.kubernetes.io/component": "conversion-webhook", "app.kubernetes.io/part-of": "tinkerbell"},
			},
			Type: corev1.SecretTypeTLS,
			Data: updated,
		}
		if _, err := secrets.Create(ctx, sec, metav1.CreateOptions{}); err != nil {
			return nil, err
		}
	case changed:
		sec = sec.DeepCopy()
		sec.Data = updated
		if _, err := secrets.Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
	}

	cert, err := tls.X509KeyPair(updated[keyTLSCert], updated[keyTLSKey])
	if err != nil {
		return nil, fmt.Errorf("loading conversion webhook certificate: %w", err)
	}
	c.mu.Lock()
	c.cert, c.ca = &cert, updated[keyCACert]
	c.mu.Unlock()
	return updated[keyCACert], nil
}

// reconcile returns Secret data with a valid CA and serving certificate,
// reusing what data already has. changed reports whether it differs from data.
func (c *Certs) reconcile(data map[string][]byte) (map[string][]byte, bool, error) {
	now := c.now()
	caCert, caKey, err := parseKeyPair(data[keyCACert], data[keyCAKey])
	if err != nil || now.Add(caRenewBefore).After(caCert.NotAfter) {
		caCert, caKey, err = newCA(now)
		if err != nil {
			return nil, false, err
		}
		data = nil // a new CA invalidates the serving certificate
	}
	out := map[string][]byte{keyCACert: encodeCert(caCert), keyCAKey: encodeKey(caKey)}
	if leaf, key, err := parseKeyPair(data[keyTLSCert], data[keyTLSKey]); err == nil && c.leafValid(leaf, caCert, now) {
		out[keyTLSCert], out[keyTLSKey] = encodeCert(leaf), encodeKey(key)
	} else {
		leaf, key, err := c.newLeaf(caCert, caKey, now)
		if err != nil {
			return nil, false, err
		}
		out[keyTLSCert], out[keyTLSKey] = encodeCert(leaf), encodeKey(key)
	}
	changed := false
	for k, v := range out {
		if !bytes.Equal(data[k], v) {
			changed = true
		}
	}
	return out, changed, nil
}

func (c *Certs) leafValid(leaf, ca *x509.Certificate, now time.Time) bool {
	if now.Add(renewBefore).After(leaf.NotAfter) || leaf.CheckSignatureFrom(ca) != nil {
		return false
	}
	for _, n := range c.DNSNames {
		if !slices.Contains(leaf.DNSNames, n) {
			return false
		}
	}
	for _, ip := range c.IPs {
		if !slices.ContainsFunc(leaf.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

// GetCertificate implements tls.Config.GetCertificate.
func (c *Certs) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cert == nil {
		return nil, errors.New("conversion webhook certificate not loaded")
	}
	return c.cert, nil
}

// CABundle returns the PEM encoded CA certificate loaded by Ensure.
func (c *Certs) CABundle() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.ca)
}

// Renew calls Ensure every interval until ctx is done, so that the serving
// certificate is renewed by long-running instances. Errors are passed to
// onErr and retried at the next interval.
func (c *Certs) Renew(ctx context.Context, interval time.Duration, onErr func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.Ensure(ctx); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}

func (c *Certs) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func newCA(now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "tinkerbell-conversion-webhook-ca"},
		NotBefore:             now.Add(-clockSkewSlack),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func (c *Certs) newLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	cn := "tinkerbell-conversion-webhook"
	if len(c.DNSNames) > 0 {
		cn = c.DNSNames[0]
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     slices.Clone(c.DNSNames),
		IPAddresses:  slices.Clone(c.IPs),
		NotBefore:    now.Add(-clockSkewSlack),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

func encodeCert(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

func encodeKey(k *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func parseKeyPair(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, errors.New("missing certificate or key")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

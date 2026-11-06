package conversionwebhook

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func leafOf(t *testing.T, c *Certs) *x509.Certificate {
	t.Helper()
	cert, err := c.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func TestCertsLifecycle(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	c := &Certs{Client: client, Namespace: "tink", SecretName: "webhook", DNSNames: []string{"svc.tink.svc"}, Now: func() time.Time { return now }}

	ca, err := c.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Secrets("tink").Get(ctx, "webhook", metav1.GetOptions{}); err != nil {
		t.Fatalf("Secret not created: %v", err)
	}
	first := leafOf(t, c)
	block, _ := pem.Decode(ca)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("serving certificate not signed by the CA bundle: %v", err)
	}
	if err := first.VerifyHostname("svc.tink.svc"); err != nil {
		t.Fatal(err)
	}

	// A restart reuses the Secret.
	again := &Certs{Client: client, Namespace: "tink", SecretName: "webhook", DNSNames: []string{"svc.tink.svc"}, Now: func() time.Time { return now }}
	ca2, err := again.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ca, ca2) || leafOf(t, again).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("a valid Secret was not reused")
	}

	// Close to expiry, the serving certificate is renewed by the same CA.
	now = first.NotAfter.Add(-renewBefore / 2)
	ca3, err := c.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	renewed := leafOf(t, c)
	if !bytes.Equal(ca, ca3) {
		t.Error("renewal changed the CA bundle")
	}
	if renewed.SerialNumber.Cmp(first.SerialNumber) == 0 || !renewed.NotAfter.After(first.NotAfter) {
		t.Error("serving certificate was not renewed")
	}

	// A new name for the webhook needs a new serving certificate.
	c.DNSNames = append(c.DNSNames, "other.tink.svc")
	if _, err := c.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := leafOf(t, c).VerifyHostname("other.tink.svc"); err != nil {
		t.Error(err)
	}
}

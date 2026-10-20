// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// testTLSCertificatePEM returns a self-signed leaf certificate and its private
// key in PEM form. The identity is irrelevant: these tests only need bytes the
// provider can fingerprint.
func testTLSCertificatePEM(t *testing.T) (string, string) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tls-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func TestTLSCertificateResourceSchema(t *testing.T) {
	r := NewTLSCertificateResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"id", "certificate_pem", "key_pem", "schema_version", "resource_id", "target_revision",
		"certificate_fingerprint", "not_after_unix", "last_issuance_id", "observed_certificate_fingerprint",
	} {
		attribute, ok := attrs[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if attribute.GetDescription() == "" {
			t.Errorf("%s must carry a description", name)
		}
	}

	certificate, ok := attrs["certificate_pem"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("certificate_pem attribute is %T", attrs["certificate_pem"])
	}
	if !certificate.Required {
		t.Error("certificate_pem must be required")
	}
	if !hasRequiresReplace(certificate.PlanModifiers) {
		t.Error("certificate_pem must carry RequiresReplace: publication replaces the whole certificate")
	}

	key, ok := attrs["key_pem"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("key_pem attribute is %T", attrs["key_pem"])
	}
	if !key.Required || !key.Sensitive {
		t.Error("key_pem must be required and sensitive")
	}
	if !hasRequiresReplace(key.PlanModifiers) {
		t.Error("key_pem must carry RequiresReplace: publication replaces the whole certificate")
	}

	for _, name := range []string{
		"id", "schema_version", "resource_id", "target_revision", "certificate_fingerprint",
		"not_after_unix", "last_issuance_id", "observed_certificate_fingerprint",
	} {
		if !attrs[name].IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
	for _, name := range []string{"certificate_pem", "key_pem"} {
		if attrs[name].IsComputed() {
			t.Errorf("%s must not be computed", name)
		}
	}
}

func TestTLSCertificateResourceMetadata(t *testing.T) {
	r := NewTLSCertificateResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_tls_certificate" {
		t.Errorf("expected rustfs_tls_certificate, got %s", resp.TypeName)
	}
}

type tlsCertificateFixture struct {
	certificatePEM string
	keyPEM         string
	fingerprint    string
}

func newTLSCertificateFixture(t *testing.T) tlsCertificateFixture {
	t.Helper()

	certificatePEM, keyPEM := testTLSCertificatePEM(t)
	fingerprint, ok := certificateFingerprintFromPEM(certificatePEM)
	if !ok {
		t.Fatal("could not fingerprint the test certificate")
	}
	return tlsCertificateFixture{certificatePEM: certificatePEM, keyPEM: keyPEM, fingerprint: fingerprint}
}

func tlsCertificateLifecycleModel(fixture tlsCertificateFixture) *TLSCertificateResourceModel {
	return &TLSCertificateResourceModel{
		ID:                             types.StringValue("server-default"),
		CertificatePEM:                 types.StringValue(fixture.certificatePEM),
		KeyPEM:                         types.StringValue(fixture.keyPEM),
		SchemaVersion:                  types.StringValue(rustfs.TLSPublishSchemaVersion),
		ResourceID:                     types.StringValue(rustfs.TLSPublishResourceID),
		TargetRevision:                 types.StringValue("1e04976a-316c-43ed-86cd-e7779b8ccd0a"),
		CertificateFingerprint:         types.StringValue(fixture.fingerprint),
		NotAfterUnix:                   types.Int64Value(1797684279),
		LastIssuanceID:                 types.StringValue("65f4ec47-53a7-415b-be51-1381f51adb9c"),
		ObservedCertificateFingerprint: types.StringValue(fixture.fingerprint),
	}
}

func tlsCertificateCreatePlan(fixture tlsCertificateFixture) *TLSCertificateResourceModel {
	return &TLSCertificateResourceModel{
		ID:                             types.StringNull(),
		CertificatePEM:                 types.StringValue(fixture.certificatePEM),
		KeyPEM:                         types.StringValue(fixture.keyPEM),
		SchemaVersion:                  types.StringNull(),
		ResourceID:                     types.StringNull(),
		TargetRevision:                 types.StringNull(),
		CertificateFingerprint:         types.StringNull(),
		NotAfterUnix:                   types.Int64Null(),
		LastIssuanceID:                 types.StringNull(),
		ObservedCertificateFingerprint: types.StringNull(),
	}
}

func tlsCertificateStateBody(fingerprint string, issuanceID string) string {
	issuance := "null"
	if issuanceID != "" {
		issuance = `"` + issuanceID + `"`
	}
	return `{"schema_version":"rustfs.tls-publish/v1alpha1","resource_id":"server-default",` +
		`"target_revision":"1e04976a-316c-43ed-86cd-e7779b8ccd0a",` +
		`"certificate_fingerprint":"` + fingerprint + `","not_after_unix":1797684279,` +
		`"last_issuance_id":` + issuance + `,"observed_certificate_fingerprint":"` + fingerprint + `"}`
}

func tlsCertificateReceiptBody(fingerprint string) string {
	return `{"schema_version":"rustfs.tls-publish/v1alpha1","resource_id":"server-default",` +
		`"issuance_id":"65f4ec47-53a7-415b-be51-1381f51adb9c",` +
		`"target_revision":"1e04976a-316c-43ed-86cd-e7779b8ccd0a",` +
		`"certificate_fingerprint":"` + fingerprint + `","not_after_unix":1797684279,` +
		`"observed_certificate_fingerprint":null}`
}

func tlsCertificateStateRoute(fingerprint string) requestRoute {
	return requestRoute{
		method:       http.MethodGet,
		pathContains: "tls/certificate-state",
		status:       http.StatusOK,
		body:         tlsCertificateStateBody(fingerprint, "65f4ec47-53a7-415b-be51-1381f51adb9c"),
	}
}

func TestTLSCertificateResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	fixture := newTLSCertificateFixture(t)
	stateBody := tlsCertificateStateBody(fixture.fingerprint, "65f4ec47-53a7-415b-be51-1381f51adb9c")

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name: "published certificate keeps state and mirrors the server",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusOK,
				body:         stateBody,
			}},
		},
		{
			// A server that still serves its startup material has no
			// publication for this resource to own.
			name: "bootstrap certificate is removed from state",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusOK,
				body:         tlsCertificateStateBody(fixture.fingerprint, ""),
			}},
			wantRemoved: true,
		},
		{
			// Another publisher replaced the certificate; this resource no
			// longer owns what the server serves.
			name: "certificate published outside Terraform is removed from state",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusOK,
				body:         tlsCertificateStateBody(strings.Repeat("f", 64), "65f4ec47-53a7-415b-be51-1381f51adb9c"),
			}},
			wantRemoved: true,
		},
		{
			name: "1.0.1 unimplemented route reports the capability gap",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "tls/certificate-state", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "A header you provided implies functionality that is not implemented.")},
				{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
			},
			wantSummary: "Error reading TLS certificate state",
			wantDetail:  "requires RustFS 1.0.2 or later",
		},
		{
			name: "plain HTTP listener is explained instead of the raw payload",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusForbidden,
				body:         s3ErrorXML("AccessDenied", "certificate management requires TLS on the RustFS listener"),
			}},
			wantSummary: "TLS certificate management needs the native TLS listener",
			wantDetail:  "ssl = false",
		},
		{
			name: "publication disabled is explained",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusServiceUnavailable,
				body:         s3ErrorXML("InvalidRequest", "certificate publication is disabled or not configured safely"),
			}},
			wantSummary: "TLS certificate publication is not enabled",
			wantDetail:  "RUSTFS_TLS_PUBLISH_DIR",
		},
		{
			name: "unrelated server error keeps state and names the failing call",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusInternalServerError,
				body:         s3ErrorXML("InternalError", "disk offline"),
			}},
			wantSummary: "Error reading TLS certificate state",
			wantDetail:  "Could not read the TLS certificate state: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTLSCertificateResource(), client)

			resp := h.read(tlsCertificateLifecycleModel(fixture))

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("target_revision"), "1e04976a-316c-43ed-86cd-e7779b8ccd0a")
				assertStateString(t, resp.State, path.Root("certificate_fingerprint"), fixture.fingerprint)
				assertStateString(t, resp.State, path.Root("observed_certificate_fingerprint"), fixture.fingerprint)
				assertStateString(t, resp.State, path.Root("last_issuance_id"), "65f4ec47-53a7-415b-be51-1381f51adb9c")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name: "1.0.1 unimplemented route reports the capability gap",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "tls/certificate-state", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "A header you provided implies functionality that is not implemented.")},
				{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
			},
			wantSummary: "Error publishing TLS certificate",
			wantDetail:  "GET /rustfs/admin/v3/tls/certificate-state",
		},
		{
			name: "plain HTTP listener is explained instead of the raw payload",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusForbidden,
				body:         s3ErrorXML("AccessDenied", "certificate management requires TLS on the RustFS listener"),
			}},
			wantSummary: "TLS certificate management needs the native TLS listener",
			wantDetail:  "ssl = false",
		},
		{
			name: "publication disabled is explained",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusServiceUnavailable,
				body:         s3ErrorXML("InvalidRequest", "certificate publication is disabled or not configured safely"),
			}},
			wantSummary: "TLS certificate publication is not enabled",
			wantDetail:  "RUSTFS_TLS_RELOAD_ENABLE",
		},
		{
			name: "lost publication race is explained",
			routes: []requestRoute{
				tlsCertificateStateRoute(fixture.fingerprint),
				{method: http.MethodPost, pathContains: "tls/certificate", status: http.StatusConflict, body: s3ErrorXML("InvalidRequest", "certificate publication revision conflict")},
			},
			wantSummary: "TLS certificate publication conflict",
			wantDetail:  "Re-run the apply",
		},
		{
			name: "server error names the failing call",
			routes: []requestRoute{
				tlsCertificateStateRoute(fixture.fingerprint),
				{method: http.MethodPost, pathContains: "tls/certificate", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "storage failed")},
			},
			wantSummary: "Error publishing TLS certificate",
			wantDetail:  "Could not publish the TLS certificate: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTLSCertificateResource(), client)
			plan := tlsCertificateCreatePlan(fixture)

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: accepted publication writes the receipt the server returned", func(t *testing.T) {
		client, recorder := routedClient(t,
			tlsCertificateStateRoute(fixture.fingerprint),
			requestRoute{method: http.MethodPost, pathContains: "tls/certificate", status: http.StatusOK, body: tlsCertificateReceiptBody(fixture.fingerprint)},
		)
		h := newCRUDHarness(t, NewTLSCertificateResource(), client)

		resp := h.create(tlsCertificateCreatePlan(fixture))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "server-default")
		assertStateString(t, resp.State, path.Root("certificate_fingerprint"), fixture.fingerprint)
		assertStateString(t, resp.State, path.Root("target_revision"), "1e04976a-316c-43ed-86cd-e7779b8ccd0a")
		assertStateString(t, resp.State, path.Root("last_issuance_id"), "65f4ec47-53a7-415b-be51-1381f51adb9c")
		assertStateString(t, resp.State, path.Root("observed_certificate_fingerprint"), "")
		assertStateString(t, resp.State, path.Root("certificate_pem"), fixture.certificatePEM)
		assertStateInt64(t, resp.State, path.Root("not_after_unix"), 1797684279)
		recorder.assertRequest(t, "POST /rustfs/admin/v3/tls/certificate")
	})

	t.Run("create: oversized PEM pair fails before any request", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewTLSCertificateResource(), client)
		plan := tlsCertificateCreatePlan(fixture)
		plan.CertificatePEM = types.StringValue(strings.Repeat("A", rustfs.TLSPublishBodyLimit))

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "TLS certificate request exceeds the RustFS body limit", "128 KiB")
		recorder.assertNoRequest(t, "tls/certificate")
		h.assertNoPartialState(resp, plan)
	})

	deleteCases := []struct {
		name            string
		fingerprint     string
		wantWarning     string
		wantDetailParts []string
	}{
		{
			name:        "published certificate warns and leaves it served",
			fingerprint: fixture.fingerprint,
			wantWarning: "TLS certificate left published",
			wantDetailParts: []string{
				fixture.fingerprint,
				"no route that unpublishes",
			},
		},
		{
			name:        "unknown fingerprint still warns",
			fingerprint: "",
			wantWarning: "TLS certificate left published",
			wantDetailParts: []string{
				"the last published certificate",
				"no route that unpublishes",
			},
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, recorder := routedClient(t)
			h := newCRUDHarness(t, NewTLSCertificateResource(), client)
			model := tlsCertificateLifecycleModel(fixture)
			model.CertificateFingerprint = types.StringValue(tc.fingerprint)

			resp := h.delete(model)

			requireNoDiagnostics(t, resp.Diagnostics)
			warnings := resp.Diagnostics.Warnings()
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning, got %d: %v", len(warnings), resp.Diagnostics)
			}
			if warnings[0].Summary() != tc.wantWarning {
				t.Errorf("warning summary = %q", warnings[0].Summary())
			}
			if got := warnings[0].Detail(); !containsAll(got, tc.wantDetailParts...) {
				t.Errorf("warning detail does not explain the destroy semantics: %q", got)
			}
			// Destroy must not call a route that cannot work.
			recorder.assertNoRequest(t, "tls/certificate")
		})
	}

	t.Run("update: writes the plan back without an API call", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewTLSCertificateResource(), client)

		plan := tlsCertificateLifecycleModel(fixture)
		state := tlsCertificateLifecycleModel(fixture)
		state.TargetRevision = types.StringValue("old-revision")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("target_revision"), "1e04976a-316c-43ed-86cd-e7779b8ccd0a")
		recorder.assertNoRequest(t, "tls/certificate")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewTLSCertificateResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// assertStateInt64 asserts the state holds want at path.
func assertStateInt64(t *testing.T, state tfsdk.State, attrPath path.Path, want int64) {
	t.Helper()

	var got types.Int64
	if diags := state.GetAttribute(context.Background(), attrPath, &got); diags.HasError() {
		t.Fatalf("state attribute %s: %v", attrPath, diags)
	}
	if got.ValueInt64() != want {
		t.Fatalf("state attribute %s = %d, want %d", attrPath, got.ValueInt64(), want)
	}
}

func TestTLSCertificateFingerprintFromPEM(t *testing.T) {
	t.Parallel()

	certificatePEM, _ := testTLSCertificatePEM(t)
	fingerprint, ok := certificateFingerprintFromPEM(certificatePEM)
	if !ok {
		t.Fatal("expected a fingerprint")
	}
	if len(fingerprint) != 64 {
		t.Errorf("fingerprint %q is not a SHA-256 hex digest", fingerprint)
	}
	if fingerprint != strings.ToLower(fingerprint) {
		t.Errorf("fingerprint %q must be lowercase like the server's", fingerprint)
	}

	for _, invalid := range []string{"", "not a certificate", "-----BEGIN CERTIFICATE-----\nZm9v\n-----END CERTIFICATE-----\n"} {
		if _, ok := certificateFingerprintFromPEM(invalid); ok {
			t.Errorf("expected no fingerprint for %q", invalid)
		}
	}
}

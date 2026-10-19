// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const tierResourceName = "rustfs_tier.test"

func TestTierResourceSchema(t *testing.T) {
	r := NewTierResource()
	sch := resourceSchemaOf(t, r)

	attrs := sch.GetAttributes()
	if len(attrs) != 3 {
		t.Errorf("expected 3 attributes, got %d: %v", len(attrs), attrs)
	}

	name, ok := attrs["name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("name is not a StringAttribute")
	}
	if !name.Required || name.Optional || name.Computed {
		t.Errorf("name must be Required only: %+v", name)
	}
	if !hasRequiresReplace(name.PlanModifiers) {
		t.Error("name must force recreation, the server keys tiers by name")
	}

	tierType, ok := attrs["tier_type"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("tier_type is not a StringAttribute")
	}
	if !tierType.Required || tierType.Optional || tierType.Computed {
		t.Errorf("tier_type must be Required only: %+v", tierType)
	}
	if !hasRequiresReplace(tierType.PlanModifiers) {
		t.Error("tier_type must force recreation, the server cannot change a tier's type")
	}

	configJSON, ok := attrs["config_json"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("config_json is not a StringAttribute")
	}
	if !configJSON.Required || configJSON.Optional || configJSON.Computed {
		t.Errorf("config_json must be Required only: %+v", configJSON)
	}
	if !configJSON.Sensitive {
		t.Error("config_json carries backend credentials and must be Sensitive")
	}
	if hasRequiresReplace(configJSON.PlanModifiers) {
		t.Error("config_json is editable through EditTier and must not force recreation")
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}
}

func TestTierResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewTierResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_tier" {
		t.Errorf("expected rustfs_tier, got %s", resp.TypeName)
	}
}

func TestTierResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewTierResource().(*TierResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewTierResource().(*TierResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a diagnostic for unexpected provider data")
		}
		if got := resp.Diagnostics[0].Summary(); got != "Unexpected Resource Configure Type" {
			t.Errorf("unexpected diagnostic summary: %s", got)
		}
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestTierResourceCreate(t *testing.T) {
	t.Run("adds the tier", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		model := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue(`{"type":"rustfs","rustfs":{"name":"S3COLD"}}`),
		}
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, model)}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/tier" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if string(request.Body) != model.ConfigJson.ValueString() {
			t.Errorf("config_json was not sent verbatim: %s", request.Body)
		}

		var got tierResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Name.ValueString() != "S3COLD" || got.TierType.ValueString() != "s3" {
			t.Errorf("state was not written from the plan: %+v", got)
		}
	})

	t.Run("rejects invalid JSON before calling the API", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("not json"),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid JSON")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>TierAddFailed</Code><Message>tier add failed</Message></Error>")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("{}"),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error adding tier")
		if !strings.Contains(diagnostic.Detail(), "TierAddFailed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

// TestTierResourceRead was deleted with #74: it pinned Read as a no-op that
// never calls the admin API, which is the behaviour the issue requires to
// change (a tier removed outside Terraform stayed in state forever).
// TestTierResourceLifecyclePaths covers the new contract: the read calls
// tier-stats, keeps a listed tier, removes a tier the server no longer lists,
// and reports a server error.

func TestTierResourceUpdate(t *testing.T) {
	t.Run("edits the tier", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue(`{"type":"rustfs","rustfs":{"name":"S3COLD","region":"us-east-01"}}`),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, tierResourceModel{}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPost || request.Path != "/rustfs/admin/v3/tier/S3COLD" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("{"),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, tierResourceModel{}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid JSON")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>TierEditFailed</Code></Error>")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("{}"),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, tierResourceModel{}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Error editing tier")
	})
}

func TestTierResourceDelete(t *testing.T) {
	t.Run("removes the tier", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		state := tierResourceModel{Name: types.StringValue("S3COLD")}
		resp := &frameworkresource.DeleteResponse{}
		r.Delete(context.Background(), frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodDelete || request.Path != "/rustfs/admin/v3/tier/S3COLD" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusConflict, "<Error><Code>XMinioAdminTierBackendInUse</Code></Error>")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		resp := &frameworkresource.DeleteResponse{}
		r.Delete(context.Background(), frameworkresource.DeleteRequest{
			State: resourceState(t, sch, tierResourceModel{Name: types.StringValue("S3COLD")}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Error removing tier")
	})
}

func TestTierResourceImportState(t *testing.T) {
	r := NewTierResource().(*TierResource)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, tierResourceModel{})}
	r.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: "S3COLD"}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("name"), &got); diags.HasError() {
		t.Fatalf("reading the imported name: %v", diags)
	}
	if got.ValueString() != "S3COLD" {
		t.Errorf("expected the import ID to land in name, got %q", got.ValueString())
	}
}

// Tier acceptance coverage. A tier is only created when the server can probe a
// reachable remote backend, so the test needs a second RustFS to point at and
// skips when the environment does not provide one. The native `rustfs` tier
// type is the one usable target here: the S3-compatible types additionally
// require credentials the remote accepts, which a two-container CI deployment
// cannot supply.

func TestAccTierResource(t *testing.T) {
	// The precheck runs here as well as in the test case below: the endpoint
	// environment is read before resource.Test, so an unconfigured environment
	// must skip the test rather than fail reading it.
	tierTestPreCheck(t)

	tierEndpoint := tierTestEndpoint(t)
	s3Endpoint := tierTestS3Endpoint(t)
	name := fmt.Sprintf("TFACCTIER%d", acctest.RandInt())
	bucket := fmt.Sprintf("tf-acc-tier-%d", acctest.RandInt())

	// The tier probe requires the target bucket to exist, and the provider's
	// aliased-provider route is not available here: RUSTFS_ENDPOINT overrides a
	// configured endpoint, so a second provider block would still talk to the
	// deployment under test. Create the bucket with the same S3 client the
	// acceptance bucket tests use instead.
	tierBackendBucket := tierTestBucket{endpoint: s3Endpoint, name: bucket}
	tierBackendBucket.create(t)
	defer tierBackendBucket.delete(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTierDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTierConfig(tierEndpoint, name, bucket),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", name),
					resource.TestCheckResourceAttr(tierResourceName, "tier_type", "rustfs"),
					resource.TestCheckResourceAttrSet(tierResourceName, "config_json"),
				),
			},
			{
				// ImportStatePassthroughID maps the tier name; tier_type and
				// config_json are not readable back from the server, so the
				// import step ignores them and expects the plan to want them
				// re-supplied from the configuration.
				ResourceName:                         tierResourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"tier_type", "config_json"},
				ExpectNonEmptyPlan:                   true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", name),
				),
			},
		},
	})
}

const (
	// tierTestEndpointEnv is the endpoint the RustFS server probes when it adds
	// the tier: a second RustFS, reachable from the server.
	tierTestEndpointEnv = "RUSTFS_TIER_ENDPOINT"
	// tierTestS3EndpointEnv overrides the host:port the test dials for that
	// second RustFS. It defaults to the host:port of RUSTFS_TIER_ENDPOINT,
	// which is correct inside the acceptance compose network; a host-based
	// local run sets it to the published port.
	tierTestS3EndpointEnv = "RUSTFS_TIER_S3_ENDPOINT"
)

// tierTestEndpoint returns the endpoint a tier can point at, failing when the
// variable is set but does not look like a URL.
func tierTestEndpoint(t *testing.T) string {
	t.Helper()
	full := os.Getenv(tierTestEndpointEnv)
	if !strings.HasPrefix(full, "http://") && !strings.HasPrefix(full, "https://") {
		t.Fatalf("%s must be a URL such as http://rustfs-peer:9000, got %q", tierTestEndpointEnv, full)
	}
	return full
}

// tierTestS3Endpoint returns the host:port of the tier backend as the test
// process reaches it.
func tierTestS3Endpoint(t *testing.T) string {
	t.Helper()
	if override := os.Getenv(tierTestS3EndpointEnv); override != "" {
		return override
	}
	return strings.TrimPrefix(strings.TrimPrefix(tierTestEndpoint(t), "http://"), "https://")
}

// tierTestPreCheck skips the test when no tier backend is configured: without a
// second endpoint the server cannot probe a tier, and pointing a tier at the
// deployment itself is refused ("probe outcome is uncertain").
func tierTestPreCheck(t *testing.T) {
	testAccPreCheck(t)
	if os.Getenv(tierTestEndpointEnv) == "" {
		t.Skipf("%s is not set, skipping: no tier backend is available for the server to probe", tierTestEndpointEnv)
	}
}

// tierTestBucket is the bucket the tier points at, on the tier backend.
type tierTestBucket struct {
	endpoint string
	name     string
}

func (b tierTestBucket) create(t *testing.T) {
	t.Helper()
	client, err := newTestS3Client(b.endpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
	if err != nil {
		t.Fatalf("building the S3 client for the tier backend %s: %v", b.endpoint, err)
	}
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(b.name)}); err != nil {
		t.Fatalf("creating the tier backend bucket %s on %s: %v", b.name, b.endpoint, err)
	}
}

func (b tierTestBucket) delete(t *testing.T) {
	t.Helper()
	client, err := newTestS3Client(b.endpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
	if err != nil {
		t.Logf("could not build the S3 client to delete the tier backend bucket %s: %v", b.name, err)
		return
	}
	if _, err := client.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(b.name)}); err != nil {
		t.Logf("could not delete the tier backend bucket %s: %v", b.name, err)
	}
}

// testAccCheckTierDestroy requires every tier the fixture created to be gone.
// The admin API answers TierNotFound for a name it does not hold. A server that
// does not implement the verify route at all (501 NotImplemented, 405) cannot
// confirm the tier's absence and is not evidence of a leak, so that passes too;
// an implemented route that still answers for the name fails.
func testAccCheckTierDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_tier" {
			continue
		}
		name := rs.Primary.Attributes["name"]
		if name == "" {
			continue
		}

		res, err := client.DoDirectRequest(context.Background(), rustfs.RequestData{
			Method: http.MethodGet,
			// DoDirectRequest resolves against the host root, so the admin
			// prefix the production client builds in createEndpointUrl is part
			// of the path here.
			RelPath: "rustfs/admin/v3/tier/" + name,
		})
		if err != nil {
			if rustfs.IsAPIErrorCode(err, "TierNotFound", "NoSuchTier") ||
				rustfs.IsAPIErrorCode(err, "NotImplemented", "MethodNotAllowed") ||
				rustfs.IsUnsupportedFeature(err) {
				continue
			}
			return fmt.Errorf("error checking tier %s destruction: %w", name, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return fmt.Errorf("tier %s still exists", name)
	}
	return nil
}

func testAccTierConfig(tierEndpoint, name, bucket string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_tier" "test" {
  name      = %q
  tier_type = "rustfs"
  config_json = jsonencode({
    type = "rustfs"
    rustfs = {
      name      = %q
      endpoint  = %q
      accessKey = %q
      secretKey = %q
      bucket    = %q
      region    = "us-east-01"
    }
  })
}
`, name, name, tierEndpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"), bucket)
}

// TestTierResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a tier that disappeared behind Terraform's back, a
// delete of a tier that is already gone, and every failing API call in Create,
// Read and Delete.
func TestTierResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed on 1.0.1 and latest: GET tier/<name> answers 500
			// TierNotFound for a tier the server does not hold.
			name:        "TierNotFound is removed from state",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("TierNotFound", "tier not found!")}},
			wantRemoved: true,
		},
		{
			name:       "configured tier stays in state",
			routes:     []requestRoute{{status: http.StatusOK}},
			wantDetail: "",
		},
		{
			// A server that does not implement the verify route cannot answer
			// the question, so the last known state is kept rather than
			// planning a destructive recreation.
			name:       "unimplemented route keeps state without a diagnostic",
			routes:     []requestRoute{{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")}},
			wantDetail: "",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading tier",
			wantDetail:  "Could not read tier: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)

			resp := h.read(&tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(`{"name":"COLD"}`),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("name"), "COLD")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: DELETE tier/<unknown> answers 200
			// with no error, so deleting an already gone tier is idempotent on
			// the server side.
			name:   "already removed tier is not an error",
			routes: []requestRoute{{status: http.StatusOK, body: ""}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error removing tier",
			wantDetail:  "Could not remove tier: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)

			resp := h.delete(&tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(`{"name":"COLD"}`),
			})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		configJSON  string
		wantSummary string
		wantDetail  string
		wantNoCall  bool
	}{
		{
			name:        "invalid JSON is rejected before any API call",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "should not be called")}},
			configJSON:  "not-json",
			wantSummary: "Invalid JSON",
			wantDetail:  "config_json must be valid JSON",
			wantNoCall:  true,
		},
		{
			name:        "rejected tier reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "tier name must be uppercase")}},
			configJSON:  `{"name":"cold","type":"s3"}`,
			wantSummary: "Error adding tier",
			wantDetail:  "Could not add tier: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)
			plan := &tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(tc.configJSON),
			}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
			if tc.wantNoCall {
				rec.assertNoRequest(t, "tier")
			}
		})
	}

	updateCases := []struct {
		name        string
		routes      []requestRoute
		configJSON  string
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "invalid JSON is rejected",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "should not be called")}},
			configJSON:  "not-json",
			wantSummary: "Invalid JSON",
			wantDetail:  "config_json must be valid JSON",
		},
		{
			name:        "rejected edit reports a diagnostic",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "unknown tier backend")}},
			configJSON:  `{"name":"COLD","type":"s3"}`,
			wantSummary: "Error editing tier",
			wantDetail:  "Could not edit tier: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)
			plan := &tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(tc.configJSON),
			}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewTierResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

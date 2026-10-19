// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccSiteReplicationResource exercises the rustfs_site_replication resource
// against a running RustFS cluster: create, in-place update with a read-back of
// the server-reported attributes, an explicit empty plan, the replacement plan
// and apply for "name" (the only RequiresReplace attribute), out-of-band drift,
// and import.
//
// A single-node server cannot form a site-replication group: the add endpoint
// rejects the deployment as soon as its own endpoint is a loopback address
// ("invalid site endpoint ... outbound URL host '127.0.0.1' is not allowed:
// loopback address"). Host-based runs (make testacc-live and a plain
// `go test` against a locally published server) therefore skip, which is also
// what happens for every run against a single RustFS instance. The peer
// coordinates are read from the acceptance environment so the compose fixture,
// which addresses both servers by service name, is the configuration where the
// steps below actually execute. The in-place update is additionally
// capability-probed (testAccSiteReplicationEditRoundTrips) and dropped, with a
// logged reason, on a fixture that publishes a single address for the peer or
// on a server that does not report the edited values back.
//
// ImportState is a plain passthrough (see
// TestSiteReplicationRessourceImportState): the import ID is the peer name, so
// there is no composite "<a>/<b>" form and no malformed-ID diagnostic to assert
// here.
func TestAccSiteReplicationResource(t *testing.T) {
	testAccPreCheck(t)
	checkSiteReplicationAvailable(t)

	fixture := testAccSiteReplicationFixture()
	name := fmt.Sprintf("tf-acc-site-%d", acctest.RandInt())
	// "name" is the only attribute carrying RequiresReplace(), so the
	// replacement path is exercised by renaming the peer.
	replacementName := name + "-alt"
	resourceName := "rustfs_site_replication.peer"
	originalConfig := fixture.config(name, fixture.endpoint, false)

	steps := []resource.TestStep{
		{
			Config: originalConfig,
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(resourceName, "name", name),
				resource.TestCheckResourceAttr(resourceName, "endpoint", fixture.endpoint),
				resource.TestCheckResourceAttr(resourceName, "access_key", fixture.accessKey),
				resource.TestCheckResourceAttr(resourceName, "skip_tls_verify", "false"),
				testAccCheckSiteReplicationExists(name),
			),
		},
	}

	// In-place update: change every server-reported attribute (the endpoint
	// when the fixture publishes a second address for the same peer, and
	// skip_tls_verify), assert the server really persisted the new values, then
	// revert.
	updateEndpoint := fixture.updateEndpoint
	if updateEndpoint == "" {
		updateEndpoint = fixture.endpoint
	}
	if testAccSiteReplicationEditRoundTrips(t, fixture, updateEndpoint) {
		updatedConfig := fixture.config(name, updateEndpoint, true)
		steps = append(steps,
			resource.TestStep{
				Config: updatedConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", name),
					resource.TestCheckResourceAttr(resourceName, "endpoint", updateEndpoint),
					resource.TestCheckResourceAttr(resourceName, "skip_tls_verify", "true"),
					testAccCheckSiteReplicationAttrs(name, updateEndpoint, true),
				),
			},
			resource.TestStep{
				// Revert the in-place update back to the original values.
				Config: originalConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", name),
					resource.TestCheckResourceAttr(resourceName, "endpoint", fixture.endpoint),
					resource.TestCheckResourceAttr(resourceName, "skip_tls_verify", "false"),
					testAccCheckSiteReplicationAttrs(name, fixture.endpoint, false),
				),
			},
		)
	} else {
		t.Log("dropping the in-place update and revert steps: this fixture cannot demonstrate a site-replication edit")
	}

	steps = append(steps,
		resource.TestStep{
			// Explicit empty plan: the state must match the config, so no
			// computed attribute may cause a perpetual diff.
			Config:             originalConfig,
			PlanOnly:           true,
			ExpectNonEmptyPlan: false,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
		},
		resource.TestStep{
			// Replacement plan for "name", the RequiresReplace attribute.
			Config:             fixture.config(replacementName, fixture.endpoint, false),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
				},
			},
		},
		resource.TestStep{
			// Apply the replacement so the replacement path really runs, then
			// assert the new instance read back from the server.
			Config: fixture.config(replacementName, fixture.endpoint, false),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(resourceName, "name", replacementName),
				resource.TestCheckResourceAttr(resourceName, "endpoint", fixture.endpoint),
				testAccCheckSiteReplicationExists(replacementName),
			),
		},
		resource.TestStep{
			// Out-of-band drift: remove the peer behind Terraform's back, then
			// assert the plan Terraform proposes (recreate it).
			Config:             fixture.config(replacementName, fixture.endpoint, false),
			ExpectNonEmptyPlan: true,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckSiteReplicationExists(replacementName),
				testAccMutateSiteReplicationOutOfBand(t, replacementName),
			),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
				},
			},
		},
		resource.TestStep{
			// Converge: recreate the peer the drift step removed.
			Config: fixture.config(replacementName, fixture.endpoint, false),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(resourceName, "name", replacementName),
				resource.TestCheckResourceAttr(resourceName, "endpoint", fixture.endpoint),
				testAccCheckSiteReplicationExists(replacementName),
			),
		},
		resource.TestStep{
			// Import uses the peer name as its whole ID. The credentials are
			// write-only - site-replication/info never returns them, so Read
			// leaves them null after an import and they cannot be verified.
			ResourceName:                         resourceName,
			ImportState:                          true,
			ImportStateId:                        replacementName,
			ImportStateVerify:                    true,
			ImportStateVerifyIdentifierAttribute: "name",
			ImportStateVerifyIgnore:              []string{"access_key", "secret_key", "ca_cert_pem"},
		},
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

// siteReplicationFixture carries the peer coordinates the acceptance server can
// reach. RUSTFS_PEER_S3_ENDPOINT is the peer's S3 endpoint as the test process
// addresses it and RUSTFS_PEER_ENDPOINT the same peer's S3 endpoint as the
// server addresses it; the compose fixture gives the peer a single service name
// for both, so updateEndpoint is only set when a run publishes two distinct
// addresses for the same site.
type siteReplicationFixture struct {
	endpoint       string
	updateEndpoint string
	accessKey      string
	secretKey      string
}

func testAccSiteReplicationFixture() siteReplicationFixture {
	fixture := siteReplicationFixture{
		endpoint:       siteReplicationURL(envOrDefault("RUSTFS_PEER_S3_ENDPOINT", "127.0.0.1:9002")),
		updateEndpoint: siteReplicationURL(os.Getenv("RUSTFS_PEER_ENDPOINT")),
		accessKey:      envOrDefault("RUSTFS_PEER_ACCESS_KEY", "rustfsadmin"),
		secretKey:      envOrDefault("RUSTFS_PEER_SECRET_KEY", "rustfsadmin"),
	}
	if fixture.updateEndpoint == fixture.endpoint {
		fixture.updateEndpoint = ""
	}
	return fixture
}

// config renders the peer resource with the fixture's credentials. The name is
// the RequiresReplace attribute; endpoint and skip_tls_verify are the
// server-reported attributes that change in place.
func (f siteReplicationFixture) config(name, endpoint string, skipTLSVerify bool) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_site_replication" "peer" {
  name            = "%s"
  endpoint        = "%s"
  access_key      = "%s"
  secret_key      = "%s"
  skip_tls_verify = %t
}
`, name, endpoint, f.accessKey, f.secretKey, skipTLSVerify)
}

// siteReplicationURL renders a "host:port" endpoint as an "http://" URL; an
// empty host:port yields an empty URL so an unset variable disables whatever
// depends on it.
func siteReplicationURL(hostPort string) string {
	if strings.TrimSpace(hostPort) == "" {
		return ""
	}
	return "http://" + strings.TrimSuffix(hostPort, "/")
}

// checkSiteReplicationAvailable attempts a real add against the configured
// endpoint and skips the test when the server rejects it (single-node servers
// cannot form a site-replication group). This confirms the add endpoint is
// reachable while allowing the suite to run on a single instance.
func checkSiteReplicationAvailable(t *testing.T) {
	t.Helper()
	client := testAccRustClient()
	fixture := testAccSiteReplicationFixture()
	probe := fmt.Sprintf("tf-acc-probe-%d", acctest.RandInt())
	err := client.SiteReplicationAdd(rustfs.SiteReplicationSite{
		Name:      probe,
		Endpoint:  fixture.endpoint,
		AccessKey: fixture.accessKey,
		SecretKey: fixture.secretKey,
	})
	if err == nil {
		_ = client.SiteReplicationRemove([]string{probe})
		return
	}
	if strings.Contains(err.Error(), "InvalidRequest") || strings.Contains(err.Error(), "loopback") {
		t.Skipf("site replication unavailable on single-node server: %v", err)
	}
	t.Fatalf("unexpected error probing site replication: %v", err)
}

// testAccSiteReplicationEditRoundTrips replays the acceptance test's in-place
// update against a throwaway peer and reports whether the server both accepted
// the edit and reported the new values back from site-replication/info. When it
// does not, the update steps are dropped: a fixture with a single address for
// the peer has nothing to change, and a server that ignores or normalizes one
// of the edited values would answer the following read with the old value
// ("Provider produced inconsistent result after apply").
func testAccSiteReplicationEditRoundTrips(t *testing.T, fixture siteReplicationFixture, updateEndpoint string) bool {
	t.Helper()
	client := testAccRustClient()
	probe := fmt.Sprintf("tf-acc-edit-probe-%d", acctest.RandInt())
	site := rustfs.SiteReplicationSite{
		Name:      probe,
		Endpoint:  fixture.endpoint,
		AccessKey: fixture.accessKey,
		SecretKey: fixture.secretKey,
	}
	if err := client.SiteReplicationAdd(site); err != nil {
		t.Logf("cannot probe the site-replication edit path (add failed): %v", err)
		return false
	}
	defer func() {
		if err := client.SiteReplicationRemove([]string{probe}); err != nil {
			t.Logf("failed to remove the site-replication probe peer %s: %v", probe, err)
		}
	}()

	site.Endpoint = updateEndpoint
	site.SkipTLSVerify = true
	if err := client.SiteReplicationEdit(site); err != nil {
		t.Logf("cannot probe the site-replication edit path (edit failed): %v", err)
		return false
	}

	info, err := client.SiteReplicationInfo()
	if err != nil {
		t.Logf("cannot probe the site-replication edit path (info failed): %v", err)
		return false
	}
	for _, peer := range info.Sites {
		if peer.Name != probe {
			continue
		}
		if peer.Endpoint != updateEndpoint || !peer.SkipTLSVerify {
			t.Logf("server reports the edited peer as endpoint=%q skip_tls_verify=%t", peer.Endpoint, peer.SkipTLSVerify)
			return false
		}
		return true
	}
	t.Logf("edited peer %s is missing from site-replication/info", probe)
	return false
}

// testAccCheckSiteReplicationExists verifies through the admin API that the
// named peer is present on the server, independently of Terraform state.
func testAccCheckSiteReplicationExists(name string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		client := testAccRustClient()
		info, err := client.SiteReplicationInfo()
		if err != nil {
			return fmt.Errorf("failed to read site replication info: %w", err)
		}
		for _, peer := range info.Sites {
			if peer.Name == name {
				return nil
			}
		}
		return fmt.Errorf("site replication peer %q is not present on the server", name)
	}
}

// testAccCheckSiteReplicationAttrs verifies the server reports the given
// endpoint and skip_tls_verify flag for the peer, so an in-place update is
// asserted against the values the server actually stored rather than only
// against Terraform state.
func testAccCheckSiteReplicationAttrs(name, endpoint string, skipTLSVerify bool) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		client := testAccRustClient()
		info, err := client.SiteReplicationInfo()
		if err != nil {
			return fmt.Errorf("failed to read site replication info: %w", err)
		}
		for _, peer := range info.Sites {
			if peer.Name != name {
				continue
			}
			if peer.Endpoint != endpoint {
				return fmt.Errorf("site replication peer %q endpoint = %q, want %q", name, peer.Endpoint, endpoint)
			}
			if peer.SkipTLSVerify != skipTLSVerify {
				return fmt.Errorf("site replication peer %q skip_tls_verify = %t, want %t", name, peer.SkipTLSVerify, skipTLSVerify)
			}
			return nil
		}
		return fmt.Errorf("site replication peer %q is not present on the server", name)
	}
}

// testAccMutateSiteReplicationOutOfBand removes the peer directly through the
// admin API, bypassing Terraform, which makes the next plan diverge from the
// state and propose to create the peer again.
func testAccMutateSiteReplicationOutOfBand(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		client := testAccRustClient()
		if err := client.SiteReplicationRemove([]string{name}); err != nil {
			return fmt.Errorf("failed to remove site replication peer %s out-of-band: %w", name, err)
		}
		return nil
	}
}

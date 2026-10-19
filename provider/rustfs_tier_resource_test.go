// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	tierResourceName = "rustfs_tier.test"
	// tierTypeRustFS is the tier type exercised by the acceptance test: the
	// second RustFS instance configured through RUSTFS_PEER_* acts as the
	// remote backend, so its own tier type is the natural fit.
	tierTypeRustFS = "rustfs"
	// tierTypeAlt carries RequiresReplace() like every other tier_type and is
	// used to build the second replacement plan. The peer accepts it with the
	// same backend fields.
	tierTypeAlt = "minio"
	// tierTypeConfigKey is the per-type sub-object key expected by the server.
	// It always matches the tier type name.
	tierTypeConfigKey = tierTypeRustFS
)

// tierTestBackend is the second S3 backend a rustfs_tier needs: RustFS
// validates the remote endpoint, its credentials and the destination bucket
// before it registers a tier, so the tier cannot point at the primary
// instance's own bucket.
type tierTestBackend struct {
	// tierEndpoint is written into config_json: the peer's S3 API as the RustFS
	// server under test reaches it.
	tierEndpoint string
	// hostEndpoint is the peer's S3 API as this test process reaches it, used to
	// create the destination bucket.
	hostEndpoint string
	bucket       string
	accessKey    string
	secretKey    string
}

// testAccTierBackend resolves the peer connection settings, creates the
// destination bucket, and probes which peer endpoint the server under test
// accepts for a tier. It skips (never fails) when the environment cannot host a
// tier: no peer is configured, or the server rejects every reachable peer
// endpoint - older RustFS releases and locked-down outbound policies reject the
// addresses a local test harness can offer.
func testAccTierBackend(t *testing.T) *tierTestBackend {
	t.Helper()
	testAccPreCheck(t)

	peerEndpoint := os.Getenv("RUSTFS_PEER_ENDPOINT")
	peerS3Endpoint := os.Getenv("RUSTFS_PEER_S3_ENDPOINT")
	accessKey := os.Getenv("RUSTFS_PEER_ACCESS_KEY")
	secretKey := os.Getenv("RUSTFS_PEER_SECRET_KEY")

	var missing []string
	for name, value := range map[string]string{
		"RUSTFS_PEER_ENDPOINT":    peerEndpoint,
		"RUSTFS_PEER_S3_ENDPOINT": peerS3Endpoint,
		"RUSTFS_PEER_ACCESS_KEY":  accessKey,
		"RUSTFS_PEER_SECRET_KEY":  secretKey,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Skipf("rustfs_tier acceptance test needs a second S3 backend: %s must be set", strings.Join(missing, ", "))
	}

	backend := &tierTestBackend{
		hostEndpoint: peerS3Endpoint,
		bucket:       fmt.Sprintf("tf-tier-%d", acctest.RandInt()),
		accessKey:    accessKey,
		secretKey:    secretKey,
	}

	peer, err := newTestS3Client(backend.hostEndpoint, accessKey, secretKey)
	if err != nil {
		t.Fatalf("failed to build peer S3 client for %s: %v", backend.hostEndpoint, err)
	}
	if _, err := peer.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(backend.bucket)}); err != nil {
		t.Fatalf("failed to create tier destination bucket %s on the peer: %v", backend.bucket, err)
	}
	t.Cleanup(func() {
		// Best effort: the peer's storage is ephemeral, and a tier that still
		// references the bucket would keep it non-empty anyway.
		if _, err := peer.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(backend.bucket)}); err != nil {
			t.Logf("could not delete tier destination bucket %s on the peer: %v", backend.bucket, err)
		}
	})

	backend.tierEndpoint = testAccTierProbeEndpoint(t, backend, tierEndpointCandidates(peerEndpoint, peerS3Endpoint))
	if backend.tierEndpoint == "" {
		t.Skip("rustfs_tier acceptance test needs a peer endpoint the RustFS server under test accepts; see the probe errors above")
	}
	return backend
}

// tierEndpointCandidates lists the peer endpoints worth probing, most portable
// first. RUSTFS_PEER_ENDPOINT is how the RustFS server reaches the peer, which
// is a service name inside a compose network but a private address on a
// container-machine harness; the host gateway names cover the latter.
func tierEndpointCandidates(peerEndpoint, peerS3Endpoint string) []string {
	var candidates []string
	add := func(endpoint string) {
		if endpoint == "" {
			return
		}
		if !strings.Contains(endpoint, "://") {
			endpoint = "http://" + endpoint
		}
		for _, existing := range candidates {
			if existing == endpoint {
				return
			}
		}
		candidates = append(candidates, endpoint)
	}

	add(os.Getenv("RUSTFS_TIER_TEST_ENDPOINT"))
	add(peerEndpoint)
	if port := endpointPort(peerS3Endpoint); port != "" {
		add("host.containers.internal" + port)
		add("host.docker.internal" + port)
	}
	return candidates
}

// endpointPort returns the ":port" suffix of an endpoint, or "" when it carries
// no port.
func endpointPort(endpoint string) string {
	if i := strings.LastIndex(endpoint, "://"); i >= 0 {
		endpoint = endpoint[i+3:]
	}
	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		return endpoint[i:]
	}
	return ""
}

// testAccTierProbeEndpoint registers a throwaway tier on each candidate
// endpoint and returns the first one the server accepts. The probed tier is
// removed again, so a failed probe leaves at most one unreferenced tier on an
// instance whose storage is ephemeral.
func testAccTierProbeEndpoint(t *testing.T, backend *tierTestBackend, candidates []string) string {
	t.Helper()
	client := testAccRustClient()

	var failures []string
	for _, endpoint := range candidates {
		name := fmt.Sprintf("TFTIERPROBE%d", acctest.RandInt())
		config := tierConfigJSON(t, tierTypeConfigKey, name, endpoint, backend.bucket, "tf-probe", backend.accessKey, backend.secretKey)
		if err := client.AddTier(json.RawMessage(config)); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", endpoint, err))
			continue
		}
		t.Logf("tier probe: %s accepted a %s tier", endpoint, tierTypeConfigKey)
		testAccRemoveTierEventually(t, client, name)
		return endpoint
	}
	for _, failure := range failures {
		t.Logf("tier probe rejected: %s", failure)
	}
	return ""
}

// tierConfigJSON builds the config_json payload for one tier. The server
// expects {"type": "<type>", "<type>": {..backend..}}, which is exactly the
// string the resource stores, so the test can pin it attribute by attribute.
func tierConfigJSON(t *testing.T, tierType, name, endpoint, bucket, prefix, accessKey, secretKey string) string {
	t.Helper()
	payload := map[string]any{
		"type": tierType,
		tierType: map[string]any{
			"name":      name,
			"endpoint":  endpoint,
			"accessKey": accessKey,
			"secretKey": secretKey,
			"bucket":    bucket,
			"prefix":    prefix,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to encode tier config: %v", err)
	}
	return string(encoded)
}

func testAccTierConfig(name, tierType, configJSON string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_tier" "test" {
  name        = %q
  tier_type   = %q
  config_json = %s
}
`, name, tierType, strconv.Quote(configJSON))
}

// testAccCheckTierRegistered asserts the server itself reports the tier. The
// resource's Read is a state passthrough, so this is the only server-side fact
// Terraform's state cannot fake.
func testAccCheckTierRegistered(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client := testAccRustClient()
		stats, err := client.TierStats()
		if err != nil {
			return fmt.Errorf("failed to list tiers: %w", err)
		}
		if _, ok := stats[name]; !ok {
			return fmt.Errorf("tier %s is not registered on the server (have %v)", name, tierNames(stats))
		}
		return nil
	}
}

// testAccCheckTierAbsent asserts the server no longer reports the tier. The
// registry drops a tier shortly after the removal is accepted, so this waits
// briefly instead of racing it.
func testAccCheckTierAbsent(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client := testAccRustClient()
		deadline := time.Now().Add(tierAbsentWait)
		for {
			stats, err := client.TierStats()
			if err != nil {
				return fmt.Errorf("failed to list tiers: %w", err)
			}
			if _, ok := stats[name]; !ok {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("tier %s is still registered on the server", name)
			}
			time.Sleep(tierProbeInterval)
		}
	}
}

// testAccMutateTierOutOfBand rewrites the tier's backend configuration without
// going through Terraform.
func testAccMutateTierOutOfBand(t *testing.T, backend *tierTestBackend, name, prefix string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client := testAccRustClient()
		config := tierConfigJSON(t, tierTypeConfigKey, name, backend.tierEndpoint, backend.bucket, prefix, backend.accessKey, backend.secretKey)
		var err error
		for range tierProbeAttempts {
			if err = client.EditTier(name, json.RawMessage(config)); err == nil {
				return nil
			}
			// The server serialises tier mutations: an edit issued while the
			// previous one is still recovering is refused, not lost.
			time.Sleep(tierProbeInterval)
		}
		return fmt.Errorf("failed to edit tier %s out-of-band: %w", name, err)
	}
}

func tierNames(stats map[string]rustfs.TierStat) []string {
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The server reconciles every tier mutation in the background and refuses the
// next one until it has finished ("Remote tier mutation recovery must finish
// before applying a new tier configuration update"), so every out-of-band call
// the test makes retries briefly. The resource retries the same conditions
// internally.
const (
	tierProbeAttempts = 8
	tierProbeInterval = 500 * time.Millisecond
	tierAbsentWait    = 10 * time.Second
	tierDestroyWait   = 30 * time.Second
)

// testAccRemoveTierEventually removes a tier, retrying while the server is
// still finishing the mutation it followed.
func testAccRemoveTierEventually(t *testing.T, client rustfs.RustfsAdmin, name string) {
	t.Helper()
	var err error
	for range tierProbeAttempts {
		if err = client.RemoveTier(name); err == nil {
			return
		}
		time.Sleep(tierProbeInterval)
	}
	t.Logf("could not remove tier %s: %v", name, err)
}

// testAccCheckTierDestroy asserts every tier Terraform managed is gone from the
// server, allowing for the asynchronous removal.
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
		deadline := time.Now().Add(tierDestroyWait)
		for {
			stats, err := client.TierStats()
			if err != nil {
				return fmt.Errorf("failed to list tiers: %w", err)
			}
			if _, ok := stats[name]; !ok {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("tier %s still exists on the server", name)
			}
			time.Sleep(time.Second)
		}
	}
	return nil
}

func TestAccTierResource(t *testing.T) {
	backend := testAccTierBackend(t)

	name := fmt.Sprintf("TFTIER%d", acctest.RandInt())
	replacementName := fmt.Sprintf("TFTIERR%d", acctest.RandInt())
	const (
		prefix        = "tf-prefix-1"
		updatedPrefix = "tf-prefix-2"
		driftedPrefix = "tf-prefix-drifted"
	)

	createConfig := tierConfigJSON(t, tierTypeConfigKey, name, backend.tierEndpoint, backend.bucket, prefix, backend.accessKey, backend.secretKey)
	updateConfig := tierConfigJSON(t, tierTypeConfigKey, name, backend.tierEndpoint, backend.bucket, updatedPrefix, backend.accessKey, backend.secretKey)
	replacementConfig := tierConfigJSON(t, tierTypeConfigKey, replacementName, backend.tierEndpoint, backend.bucket, prefix, backend.accessKey, backend.secretKey)
	altTypeConfig := tierConfigJSON(t, tierTypeAlt, name, backend.tierEndpoint, backend.bucket, prefix, backend.accessKey, backend.secretKey)
	recoveryConfig := tierConfigJSON(t, tierTypeConfigKey, replacementName, backend.tierEndpoint, backend.bucket, updatedPrefix, backend.accessKey, backend.secretKey)
	// unreachableConfig points the tier at a closed loopback port: RustFS
	// refuses it, so the provider must report the failure.
	unreachableConfig := tierConfigJSON(t, tierTypeConfigKey, name, "http://127.0.0.1:1", backend.bucket, prefix, backend.accessKey, backend.secretKey)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTierDestroy,
		Steps: []resource.TestStep{
			{
				// config_json is validated before anything reaches the server: a
				// create with a non-JSON payload must fail with the provider's
				// own diagnostic and never register a tier.
				Config:      testAccTierConfig(name, tierTypeRustFS, "{not json"),
				ExpectError: regexp.MustCompile(`config_json must be valid JSON`),
			},
			{
				// The server rejects a backend it cannot use, and the provider
				// surfaces that error instead of recording a tier that does not
				// exist. A loopback endpoint is refused by every RustFS release
				// (either by the outbound-address guard or by the connection
				// attempt itself).
				Config:      testAccTierConfig(name, tierTypeRustFS, unreachableConfig),
				ExpectError: regexp.MustCompile(`Could not add tier`),
			},
			{
				Config: testAccTierConfig(name, tierTypeRustFS, createConfig),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", name),
					resource.TestCheckResourceAttr(tierResourceName, "tier_type", tierTypeRustFS),
					resource.TestCheckResourceAttr(tierResourceName, "config_json", createConfig),
					testAccCheckTierRegistered(t, name),
				),
			},
			{
				// In-place update of the only non-replacement attribute. The
				// provider sends the new configuration to the server and records
				// it; the tier stays registered. The server's tier listing keeps
				// reporting the configuration a tier was created with (and
				// redacts secretKey), so the changed value cannot be read back
				// from it - the value assertion below is the strongest one the
				// provider's read path can support.
				Config: testAccTierConfig(name, tierTypeRustFS, updateConfig),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "config_json", updateConfig),
					testAccCheckTierRegistered(t, name),
				),
			},
			{
				// Revert the in-place update back to the original backend.
				Config: testAccTierConfig(name, tierTypeRustFS, createConfig),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "config_json", createConfig),
					testAccCheckTierRegistered(t, name),
				),
			},
			{
				// The update path validates before it calls the server too: the
				// tier must stay exactly as it was.
				Config:      testAccTierConfig(name, tierTypeRustFS, "{not json"),
				ExpectError: regexp.MustCompile(`config_json must be valid JSON`),
			},
			{
				// Explicit empty plan: the state must match the configuration,
				// with no computed attribute causing a perpetual diff.
				Config:   testAccTierConfig(name, tierTypeRustFS, createConfig),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Replacement plan for "name", one of the two RequiresReplace
				// attributes. PlanOnly: the replacement is applied later, on its
				// own step, so the replace path really runs.
				Config:             testAccTierConfig(replacementName, tierTypeRustFS, replacementConfig),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(tierResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Replacement plan for "tier_type", the other RequiresReplace
				// attribute.
				Config:             testAccTierConfig(name, tierTypeAlt, altTypeConfig),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(tierResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the replacement path really runs,
				// then assert the new instance on both sides: the state and the
				// server's own tier registry.
				Config: testAccTierConfig(replacementName, tierTypeRustFS, replacementConfig),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", replacementName),
					resource.TestCheckResourceAttr(tierResourceName, "config_json", replacementConfig),
					testAccCheckTierRegistered(t, replacementName),
					testAccCheckTierAbsent(t, name),
				),
			},
			{
				// Out-of-band drift. The server accepts the rewritten backend
				// without Terraform, but rustfs_tier's Read is a state
				// passthrough (the tier list endpoint redacts secretKey, so the
				// stored configuration cannot be reconstructed from the
				// server), which means Terraform proposes nothing at all. This
				// step pins that behaviour: the tier is still registered, the
				// plan is empty, and only a later apply of a changed
				// configuration reaches the server again.
				Config: testAccTierConfig(replacementName, tierTypeRustFS, replacementConfig),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckTierRegistered(t, replacementName),
					testAccMutateTierOutOfBand(t, backend, replacementName, driftedPrefix),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Recovery after the drift: the tier is still registered and the
				// configured backend is what Terraform keeps sending.
				Config: testAccTierConfig(replacementName, tierTypeRustFS, recoveryConfig),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "config_json", recoveryConfig),
					testAccCheckTierRegistered(t, replacementName),
				),
			},
			{
				// Import carries the tier name as its identifier. Read is a
				// passthrough, so neither tier_type nor config_json can be
				// recovered from the server: they are ignored here and verified
				// by the create/update steps above.
				ResourceName:                         tierResourceName,
				ImportState:                          true,
				ImportStateId:                        replacementName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"tier_type", "config_json"},
			},
		},
	})
}

// TestTierTransientMutationErrors pins which server answers are retried: only
// the two conditions that report an unfinished tier reconciliation. A genuine
// rejection must fail immediately.
func TestTierTransientMutationErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "unrelated", err: errors.New("NoSuchBucket")},
		{name: "tier name taken", err: &rustfs.APIError{Code: "TierNameAlreadyExist", Message: "tier name already exists"}},
		{name: "edit still recovering", err: &rustfs.APIError{Code: "TierEditFailed", Message: "tier edit failed. Remote tier mutation recovery must finish before applying a new tier configuration update"}, want: true},
		{name: "backend in use", err: &rustfs.APIError{Code: "XMinioAdminTierBackendInUse", Message: "Specified remote tier is already in use"}, want: true},
		{name: "unstructured backend in use", err: errors.New("<Error><Code>XMinioAdminTierBackendInUse</Code><Message>Specified remote tier is already in use</Message></Error>"), want: true},
		{name: "unstructured edit recovery", err: errors.New("tier edit failed. Remote tier mutation recovery must finish before applying a new tier configuration update"), want: true},
	}
	for _, tc := range cases {
		if got := isTransientTierMutationError(tc.err); got != tc.want {
			t.Errorf("%s: isTransientTierMutationError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestTierMutationRetry checks that a transient refusal is retried and that a
// permanent error is reported without a retry.
func TestTierMutationRetry(t *testing.T) {
	t.Run("retries a transient refusal", func(t *testing.T) {
		calls := 0
		err := tierMutation(func() error {
			calls++
			if calls < 3 {
				return &rustfs.APIError{Code: "XMinioAdminTierBackendInUse", Message: "Specified remote tier is already in use"}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 attempts, got %d", calls)
		}
	})

	t.Run("reports a permanent error immediately", func(t *testing.T) {
		calls := 0
		wantErr := errors.New("Specified tier type is unsupported")
		err := tierMutation(func() error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected %v, got %v", wantErr, err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 attempt, got %d", calls)
		}
	})
}

// TestTierConfigJSON checks the helper the acceptance test uses to build
// config_json, so a formatting change cannot silently weaken its assertions.
func TestTierConfigJSON(t *testing.T) {
	got := tierConfigJSON(t, tierTypeRustFS, "TIER1", "http://peer:9000", "bucket1", "prefix1", "access", "secret")
	want := `{"rustfs":{"accessKey":"access","bucket":"bucket1","endpoint":"http://peer:9000","name":"TIER1","prefix":"prefix1","secretKey":"secret"},"type":"rustfs"}`
	if got != want {
		t.Fatalf("tier config_json = %s, want %s", got, want)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("tier config_json is not valid JSON: %v", err)
	}
	if decoded["type"] != tierTypeRustFS {
		t.Errorf("type = %v, want %v", decoded["type"], tierTypeRustFS)
	}
	if _, ok := decoded[tierTypeConfigKey]; !ok {
		t.Errorf("config_json has no %s sub-object: %s", tierTypeConfigKey, got)
	}
}

// TestTierEndpointCandidates checks the peer endpoints the acceptance test
// probes, including the host-gateway fallbacks that make a
// container-machine harness usable.
func TestTierEndpointCandidates(t *testing.T) {
	t.Setenv("RUSTFS_TIER_TEST_ENDPOINT", "")
	got := tierEndpointCandidates("10.88.0.65:9000", "127.0.0.1:9002")
	want := []string{
		"http://10.88.0.65:9000",
		"http://host.containers.internal:9002",
		"http://host.docker.internal:9002",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate %d = %s, want %s", i, got[i], want[i])
		}
	}

	t.Setenv("RUSTFS_TIER_TEST_ENDPOINT", "http://peer.example:9000")
	got = tierEndpointCandidates("peer.example:9000", "")
	if len(got) != 1 || got[0] != "http://peer.example:9000" {
		t.Fatalf("explicit endpoint override = %v, want a single http://peer.example:9000", got)
	}
}

// TestTierResourceUpdateAgainstAdminAPI closes the loop the acceptance test
// cannot: RustFS keeps reporting the configuration a tier was created with and
// redacts secretKey, so an acceptance step can only observe that the tier
// survived an update. Driving Update against a stand-in admin API asserts what
// the server is actually told - the edited config_json under the tier name -
// and that the plan is written back to state.
func TestTierResourceUpdateAgainstAdminAPI(t *testing.T) {
	ctx := context.Background()
	const (
		tierName = "TIERUNIT1"
		original = `{"type":"rustfs","rustfs":{"name":"TIERUNIT1","endpoint":"http://peer:9000","accessKey":"access","secretKey":"secret","bucket":"bucket","prefix":"tf-prefix-1"}}`
		edited   = `{"type":"rustfs","rustfs":{"name":"TIERUNIT1","endpoint":"http://peer:9000","accessKey":"access","secretKey":"secret","bucket":"bucket","prefix":"tf-prefix-2"}}`
	)

	for _, tc := range []struct {
		name      string
		status    int
		response  string
		config    string
		wantBody  string
		wantErr   string
		wantCalls int
	}{
		{
			name:      "edit is accepted",
			status:    http.StatusOK,
			config:    edited,
			wantBody:  edited,
			wantCalls: 1,
		},
		{
			// A permanent rejection must reach Terraform as a diagnostic and
			// must not be retried.
			name:      "edit is rejected",
			status:    http.StatusForbidden,
			response:  `<?xml version="1.0" encoding="UTF-8"?><Error><Code>XRustfsAdminTierInvalidCredentials</Code><Message>Invalid remote tier credentials</Message></Error>`,
			config:    edited,
			wantBody:  edited,
			wantErr:   "Error editing tier",
			wantCalls: 1,
		},
		{
			// The JSON guard runs before the server is contacted at all.
			name:    "invalid json never reaches the server",
			status:  http.StatusOK,
			config:  "{not json",
			wantErr: "config_json must be valid JSON",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				calls     int
				gotPath   string
				gotBody   string
				gotMethod string
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				gotMethod, gotPath = r.Method, r.URL.Path
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(server.Close)

			client := rustfs.New(&rustfs.RustfsAdminConfig{
				AccessKey:    "admin",
				AccessSecret: "secret",
				Endpoint:     strings.TrimPrefix(server.URL, "http://"),
			})
			r := &TierResource{client: &AllClient{RustClient: client}}

			schemaResp := &fwresource.SchemaResponse{}
			r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
			if schemaResp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %s", schemaResp.Diagnostics)
			}
			schemaType := schemaResp.Schema.Type().TerraformType(ctx)

			plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaType, nil)}
			if diags := plan.Set(ctx, tierResourceModel{
				Name:       types.StringValue(tierName),
				TierType:   types.StringValue(tierTypeRustFS),
				ConfigJson: types.StringValue(tc.config),
			}); diags.HasError() {
				t.Fatalf("plan diagnostics: %s", diags)
			}

			state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaType, nil)}
			if diags := state.Set(ctx, tierResourceModel{
				Name:       types.StringValue(tierName),
				TierType:   types.StringValue(tierTypeRustFS),
				ConfigJson: types.StringValue(original),
			}); diags.HasError() {
				t.Fatalf("state diagnostics: %s", diags)
			}

			resp := &fwresource.UpdateResponse{State: state}
			r.Update(ctx, fwresource.UpdateRequest{Plan: plan}, resp)

			if calls != tc.wantCalls {
				t.Errorf("admin API calls = %d, want %d", calls, tc.wantCalls)
			}
			if tc.wantCalls > 0 {
				if gotMethod != http.MethodPost {
					t.Errorf("edit request method = %q, want %q", gotMethod, http.MethodPost)
				}
				if want := "/rustfs/admin/v3/tier/" + tierName; gotPath != want {
					t.Errorf("edit request path = %q, want %q", gotPath, want)
				}
				if gotBody != tc.wantBody {
					t.Errorf("edit request body = %s, want %s", gotBody, tc.wantBody)
				}
			}

			var got tierResourceModel
			if diags := resp.State.Get(ctx, &got); diags.HasError() {
				t.Fatalf("state diagnostics: %s", diags)
			}

			if tc.wantErr != "" {
				if !resp.Diagnostics.HasError() {
					t.Fatal("expected a diagnostic, got none")
				}
				var messages []string
				for _, d := range resp.Diagnostics.Errors() {
					messages = append(messages, fmt.Sprintf("%s: %s", d.Summary(), d.Detail()))
				}
				if joined := strings.Join(messages, "\n"); !strings.Contains(joined, tc.wantErr) {
					t.Errorf("diagnostics = %q, want them to mention %q", joined, tc.wantErr)
				}
				// A rejected update must leave the previous state untouched
				// rather than storing a plan the server never accepted.
				if got.ConfigJson.ValueString() != original {
					t.Errorf("config_json after a rejected edit = %s, want the previous value %s", got.ConfigJson.ValueString(), original)
				}
				return
			}

			if resp.Diagnostics.HasError() {
				t.Fatalf("update diagnostics: %s", resp.Diagnostics)
			}
			if got.Name.ValueString() != tierName || got.TierType.ValueString() != tierTypeRustFS {
				t.Errorf("state identity = %s/%s, want %s/%s", got.Name.ValueString(), got.TierType.ValueString(), tierName, tierTypeRustFS)
			}
			if got.ConfigJson.ValueString() != tc.config {
				t.Errorf("config_json in state = %s, want the edited value %s", got.ConfigJson.ValueString(), tc.config)
			}
		})
	}
}

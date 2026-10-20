// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure RustfsProvider satisfies various provider interfaces.
var _ provider.Provider = &RustfsProvider{}

// RustfsProvider defines the provider implementation.
type RustfsProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// RustfsProviderModel describes the provider data model.
type RustfsProviderModel struct {
	Endpoint     types.String `tfsdk:"endpoint"`
	AccessKey    types.String `tfsdk:"access_key"`
	SecretKey    types.String `tfsdk:"secret_key"`
	AccessSecret types.String `tfsdk:"access_secret"`
	Ssl          types.Bool   `tfsdk:"ssl"`
	Insecure     types.Bool   `tfsdk:"insecure"`
}

func (p *RustfsProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "rustfs"
	resp.Version = p.version
}

func (p *RustfsProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Interact with rustfs",
		MarkdownDescription: "Provider to access with RustFS",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "RUSTFS server endpoint in the format host:port. Defaults to RUSTFS_ENDPOINT environment variable.",
			},
			"access_key": schema.StringAttribute{
				Optional:    true,
				Description: "Username or access key. Defaults to RUSTFS_USER environment variable.",
			},
			"secret_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Secret key to be used as pass. Defaults to RUSTFS_SECRET environment variable.",
			},
			"access_secret": schema.StringAttribute{
				Optional:           true,
				Sensitive:          true,
				Description:        "Secret to be used as pass. Defaults to RUSTFS_SECRET environment variable.",
				DeprecationMessage: "Use secret_key instead. access_secret will be removed in a future release.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Insecure skip SSL validation",
			},
			"ssl": schema.BoolAttribute{
				Optional:    true,
				Description: "Use SSL transport",
			},
		},
	}
}

func (p *RustfsProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config RustfsProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := envOrDefault("RUSTFS_ENDPOINT", config.Endpoint.ValueString())
	if endpoint == "" {
		resp.Diagnostics.AddError(
			"Missing RUSTFS endpoint",
			"Set the endpoint in the provider block or via the RUSTFS_ENDPOINT environment variable.",
		)
		return
	}

	accessKey := envOrDefault("RUSTFS_USER", config.AccessKey.ValueString())
	secretKey := envOrDefault("RUSTFS_SECRET", config.secretKey())

	// One transport for both API clients: S3 requests go through the AWS SDK,
	// admin requests through rustfs.New, and `insecure` has to reach both.
	httpClient := &http.Client{Transport: newHTTPTransport(config.Ssl.ValueBool(), config.Insecure.ValueBool())}
	generatedConfig := generateRustClientConfig(config, httpClient)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		awsconfig.WithHTTPClient(httpClient),
	)
	if err != nil {
		resp.Diagnostics.AddError(err.Error(), err.Error())
		return
	}

	scheme := "http"
	if config.Ssl.ValueBool() {
		scheme = "https"
	}
	endpointURL := endpoint
	if !strings.Contains(endpointURL, "://") {
		endpointURL = scheme + "://" + endpointURL
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
		o.UsePathStyle = true
	})

	client := &AllClient{
		S3:         s3Client,
		RustClient: rustfs.New(generatedConfig),
	}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func newHTTPTransport(secure, insecure bool) *http.Transport {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   16,
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
		DisableCompression:    true,
	}

	if secure {
		tr.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// #nosec G402 -- certificate verification is on by default; this is
			// the explicit, opt-in `insecure` provider attribute, documented in
			// the schema and README as being for self-signed test servers.
			InsecureSkipVerify: insecure,
		}
		if f := os.Getenv("SSL_CERT_FILE"); f != "" {
			rootCAs, err := x509.SystemCertPool()
			if err != nil {
				rootCAs = x509.NewCertPool()
			}
			// #nosec G304 -- SSL_CERT_FILE is an explicit operator setting; reading
			// that exact path is the feature, there is no traversal boundary here.
			if data, err := os.ReadFile(f); err == nil {
				rootCAs.AppendCertsFromPEM(data)
			}
			tr.TLSClientConfig.RootCAs = rootCAs
		}
	}

	return tr
}

func (p *RustfsProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUserResource,
		NewPolicyResource,
		NewServiceAccountResource,
		NewLDAPServiceAccountResource,
		NewBucketResource,
		NewquotaResource,
		NewUserPolicyAttachmentResource,
		NewBucketPolicyResource,
		NewIamBackupImportResource,
		NewBucketMetadataBackupImportResource,
		NewGroupResource,
		NewGroupPolicyAttachmentResource,
		NewBucketLifecycleConfigurationResource,
		NewTierResource,
		NewBucketObjectLockResource,
		NewBucketNotificationResource,
		NewRebalanceResource,
		NewBucketReplicationResource,
		NewBucketEncryptionResource,
		NewBucketVersioningResource,
		NewAuditTargetResource,
		NewNotificationTargetResource,
		NewModuleSwitchResource,
		NewKmsKeyResource,
		NewConfigResource,
		NewBucketDurabilityResource,
		NewTableBucketResource,
		NewSiteReplicationResource,
		NewRemoteTargetResource,
		NewBucketPublicAccessBlockResource,
		NewBucketTagsResource,
		NewBucketCorsResource,
		NewLDAPPolicyAttachmentResource,
	}
}

func (p *RustfsProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewPoolsDataSource,
		NewIamBackupDataSource,
		NewBucketMetadataBackupDataSource,
		NewUsersDataSource,
		NewUserMfaDataSource,
		NewIAMPoliciesDataSource,
		NewIAMPolicyDataSource,
		NewKmsStatusDataSource,
		NewKmsConfigDataSource,
		NewQuotaDataSource,
		NewGroupsDataSource,
		NewMetricsDataSource,
		NewHealthInfoDataSource,
		NewStorageInfoDataSource,
		NewServerInfoDataSource,
		NewReplicationMetricsDataSource,
		NewIlmTierStatsDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &RustfsProvider{
			version: version,
		}
	}
}

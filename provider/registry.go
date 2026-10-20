// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"sort"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// providerTypeName is the Terraform type name prefix every registered resource
// and data source derives its own type name from.
const providerTypeName = "rustfs"

// The registry keeps provider.go independent of the type list: a feature adds
// one file that constructs its resource or data source and registers it from
// init, so parallel feature branches never edit the same list.
var (
	registryMu          sync.RWMutex
	resourceFactories   []func() resource.Resource
	dataSourceFactories []func() datasource.DataSource
)

// registerResource adds a resource constructor to the provider registry. Call
// it from init in the file that owns the resource.
func registerResource(factory func() resource.Resource) {
	registryMu.Lock()
	defer registryMu.Unlock()

	resourceFactories = append(resourceFactories, factory)
}

// registerDataSource adds a data source constructor to the provider registry.
// Call it from init in the file that owns the data source.
func registerDataSource(factory func() datasource.DataSource) {
	registryMu.Lock()
	defer registryMu.Unlock()

	dataSourceFactories = append(dataSourceFactories, factory)
}

// registeredResources returns the registered resource constructors sorted by
// Terraform type name, so Resources() is deterministic.
func registeredResources() []func() resource.Resource {
	registryMu.RLock()
	factories := append([]func() resource.Resource(nil), resourceFactories...)
	registryMu.RUnlock()

	return sortedByTypeName(factories, func(r resource.Resource) string {
		var resp resource.MetadataResponse
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		return resp.TypeName
	})
}

// registeredDataSources returns the registered data source constructors sorted
// by Terraform type name, so DataSources() is deterministic.
func registeredDataSources() []func() datasource.DataSource {
	registryMu.RLock()
	factories := append([]func() datasource.DataSource(nil), dataSourceFactories...)
	registryMu.RUnlock()

	return sortedByTypeName(factories, func(d datasource.DataSource) string {
		var resp datasource.MetadataResponse
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		return resp.TypeName
	})
}

// sortedByTypeName resolves each factory's Terraform type name once and returns
// the factories ordered by it.
func sortedByTypeName[T any](factories []func() T, typeName func(T) string) []func() T {
	type namedFactory struct {
		typeName string
		factory  func() T
	}

	named := make([]namedFactory, len(factories))
	for i, factory := range factories {
		named[i] = namedFactory{typeName: typeName(factory()), factory: factory}
	}
	sort.SliceStable(named, func(i, j int) bool {
		return named[i].typeName < named[j].typeName
	})

	sorted := make([]func() T, len(named))
	for i, entry := range named {
		sorted[i] = entry.factory
	}
	return sorted
}

// Copyright (c) 2020, Oracle and/or its affiliates.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

package resources

// ResourceContext contains information for resource creation
// It contains the relevant aspects that should be stable during creation
// of multiple resources.
type ResourceContext struct {

	// ConfigHash used to create the config map with
	ConfigHash string
	// ConfigGeneration shows the generation the configuration is based on
	ConfigGeneration int64
	// NodeGroupCount is the number of node groups in cluster configured in config
	NodeGroupCount uint32
	// ManagementNodeCount is the number of management nodes in cluster (1 or 2)
	ManagementNodeCount uint32
	// ReduncancyLevel is the reduncany level configured
	ReduncancyLevel uint32
}
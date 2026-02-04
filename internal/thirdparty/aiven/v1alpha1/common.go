package aiven_v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AuthSecretReference is a reference to a secret containing Aiven API credentials
type AuthSecretReference struct {
	// Name of the secret
	Name string `json:"name"`
	// Key within the secret
	Key string `json:"key"`
}

// ConnInfoSecretTarget specifies where to store connection information
type ConnInfoSecretTarget struct {
	// Name of the secret to create
	Name string `json:"name"`
	// Annotations to add to the secret
	Annotations map[string]string `json:"annotations,omitempty"`
	// Labels to add to the secret
	Labels map[string]string `json:"labels,omitempty"`
	// Prefix for the secret keys
	Prefix string `json:"prefix,omitempty"`
}

// IpFilter represents an IP filter entry
type IpFilter struct {
	// CIDR network address block
	Network string `json:"network"`
	// Description of the IP filter entry
	Description *string `json:"description,omitempty"`
}

// Migration configuration for data migration
type Migration struct {
	// Hostname or IP address of the source server
	Host string `json:"host"`
	// Port number of the source server
	Port int `json:"port"`
	// Password for authentication with the source server
	Password *string `json:"password,omitempty"`
	// Use SSL for the migration connection
	Ssl *bool `json:"ssl,omitempty"`
	// Username for authentication with the source server
	Username *string `json:"username,omitempty"`
	// Database name for bootstrapping the initial connection
	Dbname *string `json:"dbname,omitempty"`
	// Comma-separated list of databases to ignore during migration
	IgnoreDbs *string `json:"ignore_dbs,omitempty"`
	// The migration method
	Method *string `json:"method,omitempty"`
}

// ServiceStatus defines the observed state of an Aiven service
type ServiceStatus struct {
	// Conditions represent the latest available observations of a service state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Service state (POWEROFF, REBUILDING, REBALANCING, RUNNING)
	State string `json:"state,omitempty"`
}

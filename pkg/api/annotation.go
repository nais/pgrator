package api

const (
	AllowDeletionAnnotation           = "nais.io/allowDeletion"
	DeploymentCorrelationIDAnnotation = "nais.io/deploymentCorrelationID"

	// EngineAnnotation selects which PostgreSQL engine to use for provisioning.
	// Valid values: "zalando" (default), "cnpg".
	EngineAnnotation = "postgres.nais.io/engine"

	// EngineZalando is the Zalando Postgres Operator engine (default).
	EngineZalando = "zalando"
	// EngineCNPG is the CloudNativePG engine.
	EngineCNPG = "cnpg"
)

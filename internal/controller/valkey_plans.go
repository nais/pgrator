package controller

import (
	"fmt"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

type machineType struct {
	AivenPlan string
	Tier      v1.ValkeyTier
	Memory    v1.ValkeyMemory
}

var machineTypes = []machineType{
	{AivenPlan: "hobbyist", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory1GB},
	{AivenPlan: "startup-4", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB},
	{AivenPlan: "startup-8", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory8GB},
	{AivenPlan: "startup-14", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory14GB},
	{AivenPlan: "startup-28", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory28GB},
	{AivenPlan: "startup-56", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory56GB},
	{AivenPlan: "startup-112", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory112GB},
	{AivenPlan: "startup-200", Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory200GB},
	{AivenPlan: "business-1", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory1GB},
	{AivenPlan: "business-4", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory4GB},
	{AivenPlan: "business-8", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory8GB},
	{AivenPlan: "business-14", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory14GB},
	{AivenPlan: "business-28", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory28GB},
	{AivenPlan: "business-56", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory56GB},
	{AivenPlan: "business-112", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory112GB},
	{AivenPlan: "business-200", Tier: v1.ValkeyTierHighAvailability, Memory: v1.ValkeyMemory200GB},
}

// tierAndMemory transposes machineTypes for lookup by ValkeyTier and ValkeyMemory
var tierAndMemory map[v1.ValkeyTier]map[v1.ValkeyMemory]machineType

// aivenPlans transposes machineTypes for lookup by an Aiven plan string
var aivenPlans map[string]machineType

func init() {
	tierAndMemory = make(map[v1.ValkeyTier]map[v1.ValkeyMemory]machineType)
	for _, m := range machineTypes {
		if _, ok := tierAndMemory[m.Tier]; !ok {
			tierAndMemory[m.Tier] = make(map[v1.ValkeyMemory]machineType)
		}
		if _, ok := tierAndMemory[m.Tier][m.Memory]; ok {
			panic("duplicate tier and memory combination [" + string(m.Tier) + ", " + string(m.Memory) + "] in machineTypes")
		}
		tierAndMemory[m.Tier][m.Memory] = m
	}

	aivenPlans = make(map[string]machineType)
	for _, m := range machineTypes {
		if _, ok := aivenPlans[m.AivenPlan]; ok {
			panic("duplicate Aiven plan '" + m.AivenPlan + "' in machineTypes")
		}
		aivenPlans[m.AivenPlan] = m
	}
}

func machineTypeFromTierAndMemory(tier v1.ValkeyTier, memory v1.ValkeyMemory) (*machineType, error) {
	memories, ok := tierAndMemory[tier]
	if !ok {
		return nil, fmt.Errorf("invalid Valkey tier: %s", tier)
	}

	machine, ok := memories[memory]
	if !ok {
		return nil, fmt.Errorf("invalid Valkey memory for tier: %s cannot have memory %s", tier, memory)
	}

	return &machine, nil
}

func machineTypeFromPlan(plan string) (*machineType, error) {
	machine, ok := aivenPlans[plan]
	if !ok {
		return nil, fmt.Errorf("invalid Valkey plan: %s", plan)
	}
	return &machine, nil
}

package config

import "testing"

func containsCapability(caps []ModelCapability, target ModelCapability) bool {
	for _, cap := range caps {
		if cap == target {
			return true
		}
	}
	return false
}

func TestGetModelCapabilities_LongestPatternPreferred(t *testing.T) {
	ClearModelCapabilitiesRegistry()

	caps := GetModelCapabilities("glm-4-vision")
	if !containsCapability(caps, CapabilityVision) {
		t.Fatalf("expected glm-4-vision to include vision capability, got: %v", caps)
	}
}

func TestGetModelCapabilities_LongestPatternPreferredForGpt4o(t *testing.T) {
	ClearModelCapabilitiesRegistry()

	caps := GetModelCapabilities("gpt-4o-mini")
	if !containsCapability(caps, CapabilityVision) {
		t.Fatalf("expected gpt-4o-mini to include vision capability, got: %v", caps)
	}
}

func TestGetModelCapabilities_RegistryOverridePriority(t *testing.T) {
	ClearModelCapabilitiesRegistry()
	RegisterModelCapabilities("glm-4-vision", []ModelCapability{CapabilityStreaming})
	defer ClearModelCapabilitiesRegistry()

	caps := GetModelCapabilities("glm-4-vision")
	if containsCapability(caps, CapabilityVision) {
		t.Fatalf("expected registered override to disable vision capability, got: %v", caps)
	}
	if !containsCapability(caps, CapabilityStreaming) {
		t.Fatalf("expected registered override to keep streaming capability, got: %v", caps)
	}
}

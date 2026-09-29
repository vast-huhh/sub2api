package service

import (
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatePluginCompatibility(t *testing.T) {
	manifest := testPluginManifest(nil)
	host := PluginHostInfo{Version: "0.1.179", BuildType: "release"}

	result := EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	assert.True(t, result.Tested)
	assert.Equal(t, "compatible", result.Status)

	manifest.Requires.TestedSub2APIVersions = []string{"0.1.178"}
	result = EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	assert.False(t, result.Tested)
	assert.Equal(t, "compatible", result.Status)

	manifest.Requires.Sub2API = ">=0.2.0 <0.3.0"
	result = EvaluatePluginCompatibility(manifest, host)
	assert.True(t, result.Compatible)
	assert.Equal(t, "compatible", result.Status)
}

func TestEvaluatePluginCompatibilityRejectsProtocolMismatch(t *testing.T) {
	for _, protocol := range []string{"plugin", "transport", "ui"} {
		t.Run(protocol, func(t *testing.T) {
			manifest := testPluginManifest(nil)
			switch protocol {
			case "plugin":
				manifest.Requires.PluginProtocol = pluginv1.ProtocolVersion + 1
			case "transport":
				manifest.Requires.TransportAPI = pluginv1.TransportAPIVersion + 1
			case "ui":
				manifest.Requires.UIBridge = pluginv1.UIBridgeVersion + 1
			}
			result := EvaluatePluginCompatibility(manifest, PluginHostInfo{Version: "local-custom"})
			assert.False(t, result.Compatible)
			assert.Equal(t, "incompatible", result.Status)
		})
	}
}

func TestEvaluatePluginCompatibilityAllowsCustomHostVersions(t *testing.T) {
	for _, version := range []string{"dev", "local-custom", "0.2.7-pat401r1", "0.0.1", "99.0.0", ""} {
		t.Run(version, func(t *testing.T) {
			manifest := testPluginManifest(nil)
			manifest.Requires.Sub2API = ">=0.2.7 <0.3.0"
			manifest.Requires.TestedSub2APIVersions = []string{"0.2.7", "other-custom", ""}
			result := EvaluatePluginCompatibility(manifest, PluginHostInfo{Version: version})
			assert.True(t, result.Compatible)
			assert.False(t, result.Tested)
			assert.Equal(t, "compatible", result.Status)
		})
	}
}

func TestEvaluatePluginCompatibilityPreservesTestedVersionMetadata(t *testing.T) {
	for _, version := range []string{"v0.1.179", "local-custom"} {
		t.Run(version, func(t *testing.T) {
			manifest := testPluginManifest(nil)
			manifest.Requires.TestedSub2APIVersions = []string{"0.1.179", "local-custom"}
			result := EvaluatePluginCompatibility(manifest, PluginHostInfo{Version: version})
			assert.True(t, result.Compatible)
			assert.True(t, result.Tested)
		})
	}
}

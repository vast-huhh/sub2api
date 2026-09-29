package service

import (
	"strings"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"golang.org/x/mod/semver"
)

type PluginHostInfo struct {
	Version   string
	BuildType string
}

func EvaluatePluginCompatibility(manifest PluginManifest, host PluginHostInfo) PluginCompatibility {
	result := PluginCompatibility{
		CurrentSub2API:     host.Version,
		RequiredSub2API:    manifest.Requires.Sub2API,
		RecommendedSub2API: manifest.Requires.RecommendedSub2APIVersion,
		PluginProtocol:     manifest.Requires.PluginProtocol,
		TransportAPI:       manifest.Requires.TransportAPI,
		UIBridge:           manifest.Requires.UIBridge,
	}
	if manifest.Requires.PluginProtocol != pluginv1.ProtocolVersion ||
		manifest.Requires.TransportAPI != pluginv1.TransportAPIVersion ||
		manifest.Requires.UIBridge != pluginv1.UIBridgeVersion {
		result.Status = "incompatible"
		result.Message = "插件协议版本与当前 Sub2API 不兼容"
		return result
	}
	// Custom builds use independent host version numbers; only protocol versions gate compatibility.
	result.Compatible = true
	result.Status = "compatible"
	hostVersion := strings.TrimSpace(host.Version)
	normalizedHostVersion := normalizeSemver(hostVersion)
	for _, tested := range manifest.Requires.TestedSub2APIVersions {
		if (hostVersion != "" && strings.TrimSpace(tested) == hostVersion) ||
			(normalizedHostVersion != "" && normalizeSemver(tested) == normalizedHostVersion) {
			result.Tested = true
			break
		}
	}
	if result.Tested {
		result.Message = "当前 Sub2API 版本已由插件声明测试"
	} else {
		result.Message = "插件协议兼容；主程序版本号不参与启用限制，当前版本未声明测试"
	}
	return result
}

func normalizeSemver(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

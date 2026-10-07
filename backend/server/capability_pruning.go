package server

import "github.com/elysia-api/backend/protocol"

// toolCapabilityFamily 与 mediaCapabilityFamily 是模型能力标志（tools/vision
// capable）对应的绑定能力族；模型未声明时从绑定中裁剪这些能力。
var (
	toolCapabilityFamily = []protocol.Capability{protocol.FunctionToolsCapability, protocol.FreeTextToolsCapability, protocol.ServerToolsCapability}
	mediaCapabilityFamily = []protocol.Capability{protocol.ImagesCapability, protocol.AudioCapability, protocol.VideoCapability}
)

// stripModelDisallowedCapabilities 就地裁剪绑定中模型未声明的工具与媒体能力。
func stripModelDisallowedCapabilities(capabilities protocol.CapabilitySet, toolsCapable, visionCapable bool) {
	if !toolsCapable {
		for _, capability := range toolCapabilityFamily {
			delete(capabilities, capability)
		}
	}
	stripMediaCapabilities(capabilities, visionCapable)
}

// stripMediaCapabilities 就地裁剪视觉/听觉媒体能力（模型未声明 vision 时）。
func stripMediaCapabilities(capabilities protocol.CapabilitySet, visionCapable bool) {
	if visionCapable {
		return
	}
	for _, capability := range mediaCapabilityFamily {
		delete(capabilities, capability)
	}
}

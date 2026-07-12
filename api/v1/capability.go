package v1

import (
	"github.com/gogf/gf/v2/frame/g"

	"platform/gokit/capability"
)

type AdminCapabilitiesReq struct {
	g.Meta `path:"/api/v1/admin/capabilities" method:"get" tags:"Commerce Capabilities" summary:"List commerce capabilities"`
}
type AdminCapabilitiesRes struct {
	Manifest capability.Manifest `json:"manifest"`
}

type AdminCapabilityReq struct {
	g.Meta `path:"/api/v1/admin/capabilities/{key}" method:"get" tags:"Commerce Capabilities" summary:"Get a commerce capability"`
	Key    string `json:"key" in:"path" v:"required"`
}
type AdminCapabilityRes struct {
	Capability capability.Capability `json:"capability"`
}

type AdminProvidersReq struct {
	g.Meta `path:"/api/v1/admin/providers" method:"get" tags:"Commerce Capabilities" summary:"List commerce payment providers"`
}
type AdminProvidersRes struct {
	Items []capability.Provider `json:"items"`
}

type AdminProviderReq struct {
	g.Meta `path:"/api/v1/admin/providers/{key}" method:"get" tags:"Commerce Capabilities" summary:"Get a commerce payment provider"`
	Key    string `json:"key" in:"path" v:"required"`
}
type AdminProviderRes struct {
	Provider capability.Provider `json:"provider"`
}

type AdminProviderHealthCheckReq struct {
	g.Meta `path:"/api/v1/admin/providers/{key}/health-check" method:"post" tags:"Commerce Capabilities" summary:"Probe payment credentials without charging"`
	Key    string `json:"key" in:"path" v:"required"`
}
type AdminProviderHealthCheckRes struct {
	Provider capability.Provider `json:"provider"`
}

package catalog

import "strings"

var targetSystems = map[string]bool{"ubuntu:22.04": true, "ubuntu:24.04": true, "debian:12": true, "debian:13": true}

// Availability belongs to the trusted recipe, never to request parameters.
// Old entries retain their existing behavior; newly versioned recipes require
// explicit per-system promotion after the native acceptance workflow.
func InstallationReason(e Entry, mode, method, system string) string {
	if e.DisabledReason != "" {
		return e.DisabledReason
	}
	if mode == "docker" {
		if e.Image == "" {
			return "此版本未提供固定摘要容器镜像"
		}
		return ""
	}
	if mode != "native" {
		return "安装模式无效"
	}
	if !targetSystems[system] {
		return "仅支持 Ubuntu 22.04/24.04、Debian 12/13 的 Linux amd64 环境"
	}
	if method == "" {
		if e.Software == "mysql" || e.Software == "nodejs" {
			method = "binary"
		} else {
			method = "source"
		}
	}
	if method != "source" && method != "binary" {
		return "安装方式无效"
	}
	if len(e.Methods) > 0 {
		found := false
		for _, m := range e.Methods {
			found = found || m == method
		}
		if !found {
			return "此配方未提供所选安装方式"
		}
	}
	if method == "binary" && e.Software != "mysql" && e.Software != "nodejs" {
		found := false
		for _, artifact := range e.Binaries {
			if artifact.System == system {
				found = true
			}
		}
		if !found {
			return "当前系统尚无已验证且发布的预编译包"
		}
	}
	if method == "source" && (e.Software == "mysql" || e.Software == "nodejs") {
		return "此软件仅提供预编译安装"
	}
	if e.RecipeRevision != "" {
		for _, verified := range e.VerifiedSystems[method] {
			if verified == system {
				return ""
			}
		}
		return "此版本与系统的安装组合尚未完成原生服务验收，暂不可安装"
	}
	return ""
}

func ForSystem(system string) []Entry {
	entries := Entries()
	for i := range entries {
		e := &entries[i]
		e.Availability = map[string]string{}
		for _, method := range []string{"source", "binary"} {
			e.Availability[method] = InstallationReason(*e, "native", method, system)
		}
		if e.DisabledReason == "" && e.Image == "" && e.Availability["source"] != "" && e.Availability["binary"] != "" {
			e.DisabledReason = strings.TrimSpace(e.Availability["source"] + "；" + e.Availability["binary"])
		}
	}
	return entries
}

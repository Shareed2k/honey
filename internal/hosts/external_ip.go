package hosts

import (
	"net/netip"
	"strings"
)

// ExternalIP returns the VM's public/out-of-VPC address when present.
// GCP and AWS store it in ExtraIPs while PrimaryIP is the private address used for SSH.
func ExternalIP(r Record) string {
	primary := strings.TrimSpace(r.PrimaryIP)
	if primary != "" && isPublicUnicast(primary) {
		return primary
	}
	for _, ip := range r.ExtraIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" && isPublicUnicast(ip) {
			return ip
		}
	}
	return ""
}

// NodeDisplayIP prefers ExternalIP for UI tables; falls back to PrimaryIP.
func NodeDisplayIP(r Record) string {
	if ext := ExternalIP(r); ext != "" {
		return ext
	}
	return strings.TrimSpace(r.PrimaryIP)
}

func isPublicUnicast(s string) bool {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	return addr.IsValid() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsMulticast() && !addr.IsUnspecified()
}

// Package tun manages transparent VPN tunnels via tun2proxy-bin subprocess.
package tun

import "net"

// ComplementCIDRs returns the set of CIDRs covering all of IPv4 (and IPv6, if
// v6 nets are present) except the provided nets. Passed as --bypass args to
// tun2proxy to implement "route only these CIDRs" semantics.
func ComplementCIDRs(nets []string) []string {
	if len(nets) == 0 {
		return nil
	}
	var v4nets, v6nets []*net.IPNet
	for _, s := range nets {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			continue
		}
		if n.IP.To4() != nil {
			v4nets = append(v4nets, n)
		} else {
			v6nets = append(v6nets, n)
		}
	}
	var result []string
	result = append(result, subtractNets("0.0.0.0/0", v4nets)...)
	if len(v6nets) > 0 {
		result = append(result, subtractNets("::/0", v6nets)...)
	}
	return result
}

func subtractNets(base string, subnets []*net.IPNet) []string {
	_, baseNet, _ := net.ParseCIDR(base)
	remaining := []*net.IPNet{baseNet}
	for _, sub := range subnets {
		next := make([]*net.IPNet, 0, len(remaining))
		for _, r := range remaining {
			next = append(next, excludeNet(r, sub)...)
		}
		remaining = next
	}
	out := make([]string, 0, len(remaining))
	for _, r := range remaining {
		out = append(out, r.String())
	}
	return out
}

func excludeNet(r, excl *net.IPNet) []*net.IPNet {
	if !r.Contains(excl.IP) && !excl.Contains(r.IP) {
		return []*net.IPNet{r}
	}
	rOnes, rBits := r.Mask.Size()
	eOnes, _ := excl.Mask.Size()
	if rOnes == eOnes && r.IP.Equal(excl.IP) {
		return nil
	}
	left := &net.IPNet{IP: cloneIP(r.IP), Mask: net.CIDRMask(rOnes+1, rBits)}
	right := nextSubnet(left)
	return append(excludeNet(left, excl), excludeNet(right, excl)...)
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func nextSubnet(n *net.IPNet) *net.IPNet {
	ones, bits := n.Mask.Size()
	ip := cloneIP(n.IP)
	// Flip the last network bit (at position ones-1 from MSB) to get the next sibling.
	byteIdx := (ones - 1) / 8
	bitIdx := uint((ones - 1) % 8)
	ip[byteIdx] += 1 << (7 - bitIdx)
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(ones, bits)}
}

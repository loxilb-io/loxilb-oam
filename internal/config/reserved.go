package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// reservedEndpointsEnv holds a comma-separated list of host endpoints that must
// never be programmed as a load-balancer VIP on a managed gateway.
//
// It exists for the converged single-node deployment, where the management edge
// (Caddy) and the gateway's eBPF datapath share a host — and, on a single-NIC
// host, share a wire. A load-balancer rule whose VIP matches the edge's
// address:port does not fail cleanly:
//
//   - In the L7 fullproxy modes the gateway binds a real socket, so the outcome
//     depends on whether both listeners happen to set SO_REUSEADDR.
//   - In the L4 modes the gateway never binds at all — it processes the packet
//     in eBPF at the TC hook, ahead of netfilter and Docker's DNAT. The rule
//     silently swallows traffic to the management console. Nothing is logged
//     and nothing reports a conflict; the UI simply stops answering.
//
// The second case is why this guard exists: it is a failure of silence, so it
// has to be refused at admission rather than detected afterwards.
//
// Format: "ip:port[/proto]", comma-separated. The address may be omitted or
// given as a wildcard ("0.0.0.0", "::") to reserve the port on every address;
// the protocol may be omitted to reserve both TCP and UDP. Examples:
//
//	OAM_RESERVED_ENDPOINTS=192.168.0.8:8443
//	OAM_RESERVED_ENDPOINTS=192.168.0.8:8443/tcp,0.0.0.0:8080
const reservedEndpointsEnv = "OAM_RESERVED_ENDPOINTS"

// ReservedEndpoint is one parsed entry of OAM_RESERVED_ENDPOINTS.
type ReservedEndpoint struct {
	// IP is the reserved address. A nil IP, or an unspecified address such as
	// 0.0.0.0 or ::, reserves Port on every address of the host.
	IP net.IP
	// Port is the reserved TCP/UDP port. Always in 1..65535.
	Port int
	// Proto is "tcp", "udp", or "" meaning every protocol.
	Proto string
}

// String renders the endpoint in the same form it is configured in, so that a
// rejection message names something the operator can grep for in .env.
func (r ReservedEndpoint) String() string {
	host := "*"
	if r.IP != nil && !r.IP.IsUnspecified() {
		host = r.IP.String()
	}
	s := net.JoinHostPort(host, strconv.Itoa(r.Port))
	if r.Proto != "" {
		s += "/" + r.Proto
	}
	return s
}

var (
	reservedEndpoints    []ReservedEndpoint
	reservedEndpointsErr error
)

func init() {
	reservedEndpoints, reservedEndpointsErr = ParseReservedEndpoints(os.Getenv(reservedEndpointsEnv))
}

// ReservedEndpoints returns the configured reserved endpoints. It is empty
// unless OAM_RESERVED_ENDPOINTS is set, so the guard is inert on deployments
// that do not co-locate a gateway with the management plane.
func ReservedEndpoints() []ReservedEndpoint { return reservedEndpoints }

// ReservedEndpointsError reports a malformed OAM_RESERVED_ENDPOINTS value. The
// server refuses to start on a non-nil result (see main.requireSecrets): a
// reserved list that silently failed to parse is worse than none at all,
// because the operator believes the console is protected when it is not.
func ReservedEndpointsError() error { return reservedEndpointsErr }

// ParseReservedEndpoints parses a comma-separated OAM_RESERVED_ENDPOINTS value.
// An empty string yields no endpoints and no error.
func ParseReservedEndpoints(v string) ([]ReservedEndpoint, error) {
	var out []ReservedEndpoint
	for _, raw := range strings.Split(v, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		proto := ""
		// Split the optional protocol suffix off the right. An IPv6 literal is
		// bracketed, so a "/" can only be the protocol separator here.
		if i := strings.LastIndex(entry, "/"); i >= 0 {
			proto = strings.ToLower(strings.TrimSpace(entry[i+1:]))
			entry = strings.TrimSpace(entry[:i])
			if proto != "tcp" && proto != "udp" {
				return nil, fmt.Errorf("%s: %q has protocol %q, want tcp or udp", reservedEndpointsEnv, raw, proto)
			}
		}

		host, portStr, err := net.SplitHostPort(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a valid ip:port[/proto] entry: %w", reservedEndpointsEnv, raw, err)
		}

		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%s: %q has port %q, want 1-65535", reservedEndpointsEnv, raw, portStr)
		}

		var ip net.IP
		if host = strings.TrimSpace(host); host != "" {
			if ip = net.ParseIP(host); ip == nil {
				return nil, fmt.Errorf("%s: %q has address %q, want an IP literal", reservedEndpointsEnv, raw, host)
			}
		}

		out = append(out, ReservedEndpoint{IP: ip, Port: port, Proto: proto})
	}
	return out, nil
}

// MatchReservedEndpoint reports the first entry in list that a load-balancer
// rule for ip:port/proto would collide with.
//
// A wildcard on EITHER side matches: a reserved entry with no address protects
// its port on every address, and a rule with a wildcard VIP (0.0.0.0) captures
// traffic to every address — including the reserved one — so it must be
// refused just the same. An empty proto on either side matches any protocol.
func MatchReservedEndpoint(list []ReservedEndpoint, ip string, port int, proto string) (ReservedEndpoint, bool) {
	ruleIP := net.ParseIP(strings.TrimSpace(ip))
	ruleProto := strings.ToLower(strings.TrimSpace(proto))

	for _, r := range list {
		if r.Port != port {
			continue
		}
		if r.Proto != "" && ruleProto != "" && r.Proto != ruleProto {
			continue
		}
		if !addrOverlaps(r.IP, ruleIP) {
			continue
		}
		return r, true
	}
	return ReservedEndpoint{}, false
}

// addrOverlaps reports whether a rule bound to ruleIP could receive traffic
// addressed to reservedIP. An unspecified or unparseable address on either side
// is treated as "every address" — unparseable deliberately included, so that a
// VIP this code does not understand is refused rather than waved through.
func addrOverlaps(reservedIP, ruleIP net.IP) bool {
	if reservedIP == nil || reservedIP.IsUnspecified() {
		return true
	}
	if ruleIP == nil || ruleIP.IsUnspecified() {
		return true
	}
	return reservedIP.Equal(ruleIP)
}

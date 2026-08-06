package models

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Validation for LoxiLB instance registrations.
//
// An instance record is not free-form data: OAM derives the endpoint it
// proxies to as {protocol}://{host}:{port}/netlox/{version} and stores it
// under a UNIQUE constraint. Every field below therefore has to satisfy the
// grammar of the URL component it becomes — a host carrying a path, or a
// version carrying '..', would let a caller redirect the proxy at a target
// of their choosing. `binding:"required"` alone never checked any of that.
//
// The UI validates the same rules for fast feedback; this is the gate that
// actually holds.

const (
	instanceNameMax        = 63
	instanceHostMax        = 253
	instanceVersionMax     = 16
	instanceImageMax       = 255
	instanceDescriptionMax = 1024
)

var (
	// Name is used as a URL query value by the UI (?name=…) and must stay an
	// unambiguous token so two instances cannot differ only by encoding.
	instanceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// RFC 1123 DNS label.
	dnsLabelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	// Version becomes a path segment; the leading-alphanumeric rule is what
	// keeps '..' and './' out of it.
	instanceVersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// OCI reference without a tag: optional registry host[:port] + lowercase
	// path components.
	instanceImageRe = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]{1,5})?/)?[a-z0-9]+((([._]|__|-+)[a-z0-9]+)*)(/[a-z0-9]+((([._]|__|-+)[a-z0-9]+)*))*$`)
	// OCI tag.
	instanceTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	// A digits-only rightmost label means a mistyped IPv4, not a hostname
	// (RFC 3696 §2).
	allDigitsRe = regexp.MustCompile(`^[0-9]+$`)
)

// ValidationError names the offending field so a client can highlight it
// instead of guessing which input the message is about.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func invalid(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

// ValidateInstanceName checks the identifier the UI routes on.
func ValidateInstanceName(name string) *ValidationError {
	switch {
	case name == "":
		return invalid("name", "name is required")
	case len(name) > instanceNameMax:
		return invalid("name", fmt.Sprintf("name must be at most %d characters", instanceNameMax))
	case !instanceNameRe.MatchString(name):
		return invalid("name", "name may contain letters, digits, dot, dash and underscore, and must start with a letter or digit")
	}
	return nil
}

// IsValidInstanceHost accepts a hostname, an IPv4 literal, or a BRACKETED
// IPv6 literal — bare IPv6 is rejected because the host is interpolated into
// a URL authority, where it would be unparseable without brackets.
func IsValidInstanceHost(host string) bool {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		ip := net.ParseIP(host[1 : len(host)-1])
		return ip != nil && ip.To4() == nil
	}
	if ip := net.ParseIP(host); ip != nil {
		// net.ParseIP accepts '192.0.2.010' style input on some versions and
		// bare IPv6; only a canonical v4 literal is allowed unbracketed.
		return ip.To4() != nil && host == ip.String()
	}
	if host == "" || len(host) > instanceHostMax || strings.HasSuffix(host, ".") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !dnsLabelRe.MatchString(label) {
			return false
		}
	}
	// 192.0.2.999 parses as a syntactically legal DNS name — it is a typo'd
	// address, and accepting it would register an endpoint that can never
	// resolve.
	if len(labels) > 1 && allDigitsRe.MatchString(labels[len(labels)-1]) {
		return false
	}
	return true
}

// ValidateInstanceHost reports the specific mistake where it can, because
// "invalid host" does not tell an operator that they pasted a URL.
func ValidateInstanceHost(host string) *ValidationError {
	switch {
	case host == "":
		return invalid("host", "host is required")
	case strings.Contains(host, "://"):
		return invalid("host", "host must not include a scheme (http:// or https://)")
	case strings.Contains(host, "/"):
		return invalid("host", "host must not include a path")
	case strings.ContainsAny(host, " \t\r\n"):
		return invalid("host", "host must not contain whitespace")
	case len(host) > instanceHostMax:
		return invalid("host", fmt.Sprintf("host must be at most %d characters", instanceHostMax))
	}
	if !strings.HasPrefix(host, "[") {
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return invalid("host", "wrap an IPv6 address in brackets, e.g. [2001:db8::1]")
		}
	}
	if !IsValidInstanceHost(host) {
		return invalid("host", "host must be a hostname, an IPv4 address, or a bracketed IPv6 address")
	}
	return nil
}

// ValidateInstancePort accepts 1-65535 in canonical spelling ('08091' and
// '8091' reach the same socket but would be stored as different rows).
func ValidateInstancePort(port string) *ValidationError {
	if port == "" {
		return invalid("port", "port is required")
	}
	if len(port) > 1 && strings.HasPrefix(port, "0") {
		return invalid("port", "port must not have leading zeros")
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		return invalid("port", "port must be a number")
	}
	if value < 1 || value > 65535 {
		return invalid("port", "port must be between 1 and 65535")
	}
	return nil
}

// ValidateInstanceProtocol allows only the two schemes the proxy can speak.
func ValidateInstanceProtocol(protocol string) *ValidationError {
	if protocol != "http" && protocol != "https" {
		return invalid("protocol", "protocol must be http or https")
	}
	return nil
}

// ValidateInstanceVersion guards the path segment the endpoint ends in.
func ValidateInstanceVersion(version string) *ValidationError {
	switch {
	case version == "":
		return invalid("version", "version is required")
	case len(version) > instanceVersionMax:
		return invalid("version", fmt.Sprintf("version must be at most %d characters", instanceVersionMax))
	case !instanceVersionRe.MatchString(version):
		return invalid("version", "version must be a plain path segment, e.g. v1")
	}
	return nil
}

// ValidateInstanceImage / ValidateInstanceTag keep the two halves of an image
// reference in their own fields — a ':tag' pasted into the image would be
// stored but never used, since the tag column is what callers read.
func ValidateInstanceImage(image string) *ValidationError {
	switch {
	case image == "":
		return invalid("cimage", "cimage is required")
	case len(image) > instanceImageMax:
		return invalid("cimage", fmt.Sprintf("cimage must be at most %d characters", instanceImageMax))
	case strings.ContainsAny(image, " \t\r\n"):
		return invalid("cimage", "cimage must not contain whitespace")
	}
	segments := strings.Split(image, "/")
	if strings.Contains(segments[len(segments)-1], ":") {
		return invalid("cimage", "cimage must not include a tag — use the ctag field")
	}
	if !instanceImageRe.MatchString(image) {
		return invalid("cimage", "cimage must be a valid image reference, e.g. ghcr.io/loxilb-io/loxilb")
	}
	return nil
}

func ValidateInstanceTag(tag string) *ValidationError {
	switch {
	case tag == "":
		return invalid("ctag", "ctag is required")
	case !instanceTagRe.MatchString(tag):
		return invalid("ctag", "ctag may contain letters, digits, dot, dash and underscore, e.g. latest")
	}
	return nil
}

func ValidateInstanceDescription(description string) *ValidationError {
	if len(description) > instanceDescriptionMax {
		return invalid("description", fmt.Sprintf("description must be at most %d characters", instanceDescriptionMax))
	}
	return nil
}

// InstanceFields is the validated subset shared by the create and update
// paths, so both can never drift apart.
type InstanceFields struct {
	Name        string
	Host        string
	Port        string
	Protocol    string
	Description string
	Version     string
	Cimage      string
	Ctag        string
}

// Normalize trims surrounding whitespace, lowercases the protocol, and
// defaults the version to v1 (it has never been a required field).
//
// The protocol is deliberately NOT defaulted: create requires it, and on
// update a missing protocol silently rewrote an http instance's endpoint to
// https — pointing the proxy at a port that does not speak TLS. An absent
// protocol is an error, not an https instance.
func (f *InstanceFields) Normalize() {
	f.Name = strings.TrimSpace(f.Name)
	f.Host = strings.TrimSpace(f.Host)
	f.Port = strings.TrimSpace(f.Port)
	f.Description = strings.TrimSpace(f.Description)
	f.Cimage = strings.TrimSpace(f.Cimage)
	f.Ctag = strings.TrimSpace(f.Ctag)

	f.Protocol = strings.ToLower(strings.TrimSpace(f.Protocol))
	f.Version = strings.TrimSpace(f.Version)
	if f.Version == "" {
		f.Version = "v1"
	}
}

// Validate returns the first offending field, or nil.
func (f InstanceFields) Validate() *ValidationError {
	for _, check := range []func() *ValidationError{
		func() *ValidationError { return ValidateInstanceName(f.Name) },
		func() *ValidationError { return ValidateInstanceHost(f.Host) },
		func() *ValidationError { return ValidateInstancePort(f.Port) },
		func() *ValidationError { return ValidateInstanceProtocol(f.Protocol) },
		func() *ValidationError { return ValidateInstanceVersion(f.Version) },
		func() *ValidationError { return ValidateInstanceImage(f.Cimage) },
		func() *ValidationError { return ValidateInstanceTag(f.Ctag) },
		func() *ValidationError { return ValidateInstanceDescription(f.Description) },
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// APIEndpoint derives the proxy target. Single definition on purpose: the
// create path, the update path and the uniqueness check must agree on it
// byte-for-byte, or the UNIQUE constraint would be checked against a
// different string than the one stored.
func (f InstanceFields) APIEndpoint() string {
	return fmt.Sprintf("%s://%s:%s/netlox/%s", f.Protocol, f.Host, f.Port, f.Version)
}

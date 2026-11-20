// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package filters

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Characters allowed next to ASCII letters and digits, per kind of value.
// Each set leaves out what the HAProxy configuration parser treats specially
// in the place the value is written to.
const (
	// HTTP token characters, minus "#", "'", "$" and "%".
	headerNameChars = "!&*+-.^_`|~"
	// URL path characters, minus "'", "(", ")" and ",".
	pathChars   = "-._~!$&*+;=:@/%"
	hostChars   = "-."
	originChars = "-.:/*"
)

// ValidateFilters reports the first filter value that cannot be written as
// inert HAProxy configuration. It does not rely on the CRD schema, which
// leaves some of these values unconstrained.
//
// matchPrefixes are the path prefixes the rule matches on. They are written
// next to the replacement of a ReplacePrefixMatch modifier, so they are held
// to the same characters when a filter uses one.
func ValidateFilters(httpFilters []gatewayv1.HTTPRouteFilter, matchPrefixes ...string) error {
	for _, filter := range httpFilters {
		var err error
		switch {
		case filter.RequestHeaderModifier != nil:
			err = validateHeaderFilter(filter.RequestHeaderModifier)
		case filter.ResponseHeaderModifier != nil:
			err = validateHeaderFilter(filter.ResponseHeaderModifier)
		case filter.URLRewrite != nil:
			err = errors.Join(
				validateHostname(filter.URLRewrite.Hostname),
				validatePathModifier(filter.URLRewrite.Path, matchPrefixes),
			)
		case filter.RequestRedirect != nil:
			err = validateRedirect(filter.RequestRedirect, matchPrefixes)
		case filter.CORS != nil:
			err = validateCORS(filter.CORS)
		}
		if err != nil {
			return fmt.Errorf("invalid filter %s: %w", filter.Type, err)
		}
	}
	return nil
}

func validateHeaderFilter(f *gatewayv1.HTTPHeaderFilter) error {
	for _, headers := range [][]gatewayv1.HTTPHeader{f.Set, f.Add} {
		for _, h := range headers {
			if err := validateHeaderName(string(h.Name)); err != nil {
				return err
			}
			// A tab is legal inside a header value, other control characters are not.
			if strings.ContainsFunc(h.Value, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }) {
				return fmt.Errorf("value of header %q holds a control character", h.Name)
			}
		}
	}
	for _, name := range f.Remove {
		if err := validateHeaderName(name); err != nil {
			return err
		}
	}
	return nil
}

func validateHeaderName(name string) error {
	if name == "" || !onlyAlnumOr(name, headerNameChars) {
		return fmt.Errorf("header name %q: only letters, digits and %s are allowed", name, headerNameChars)
	}
	return nil
}

func validateHostname[T ~string](hostname *T) error {
	if hostname != nil && !onlyAlnumOr(string(*hostname), hostChars) {
		return fmt.Errorf("hostname %q: only letters, digits and %s are allowed", *hostname, hostChars)
	}
	return nil
}

func validatePath(path *string) error {
	if path != nil && !onlyAlnumOr(*path, pathChars) {
		return fmt.Errorf("path %q: only letters, digits and %s are allowed", *path, pathChars)
	}
	return nil
}

func validatePathModifier(path *gatewayv1.HTTPPathModifier, matchPrefixes []string) error {
	if path == nil {
		return nil
	}
	if err := errors.Join(validatePath(path.ReplaceFullPath), validatePath(path.ReplacePrefixMatch)); err != nil {
		return err
	}
	if path.Type != gatewayv1.PrefixMatchHTTPPathModifier {
		return nil
	}
	for _, prefix := range matchPrefixes {
		if !onlyAlnumOr(prefix, pathChars) {
			return fmt.Errorf("matched path prefix %q cannot be replaced: only letters, digits and %s are allowed",
				prefix, pathChars)
		}
	}
	return nil
}

func validateRedirect(f *gatewayv1.HTTPRequestRedirectFilter, matchPrefixes []string) error {
	if f.Scheme != nil && !onlyAlnumOr(*f.Scheme, "") {
		return fmt.Errorf("scheme %q: only letters and digits are allowed", *f.Scheme)
	}
	return errors.Join(validateHostname(f.Hostname), validatePathModifier(f.Path, matchPrefixes))
}

func validateCORS(f *gatewayv1.HTTPCORSFilter) error {
	for _, origin := range f.AllowOrigins {
		if !onlyAlnumOr(string(origin), originChars) {
			return fmt.Errorf("origin %q: only letters, digits and %s are allowed", origin, originChars)
		}
	}
	for _, method := range f.AllowMethods {
		if method != "*" && !onlyAlnumOr(string(method), "") {
			return fmt.Errorf("method %q: only letters and digits are allowed", method)
		}
	}
	for _, headers := range [][]gatewayv1.HTTPHeaderName{f.AllowHeaders, f.ExposeHeaders} {
		for _, name := range headers {
			if err := validateHeaderName(string(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// onlyAlnumOr reports whether s holds only ASCII letters, digits and extra.
func onlyAlnumOr(s, extra string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !isAlnum && strings.IndexByte(extra, c) < 0 {
			return false
		}
	}
	return true
}

// literalFmt renders s as one literal log-format argument. Inside single
// quotes HAProxy expands no variable, escape or space. Doubling "%" keeps
// log-format from evaluating it.
func literalFmt(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// escapeFmt keeps "%" literal in a log-format string of validated characters.
func escapeFmt(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

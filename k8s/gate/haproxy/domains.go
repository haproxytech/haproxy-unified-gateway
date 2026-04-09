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
package haproxy

import "strings"

// isDomainWildcard checks if a domain is a wildcard domain (starts with "*.")
func isDomainWildcard(domain string) bool {
	return strings.HasPrefix(domain, "*.")
}

func removeDomainWildcard(domain string) string {
	return strings.TrimPrefix(domain, "*")
}

// reverseDomain reverses the label order of a domain string so that the most
// significant part (TLD) is at the end. This is required for HAProxy map_end
// lookups, which match against the end of the key rather than the beginning,
// and do not perform longest-match selection.
//
// The result always starts with a "." to ensure correct suffix matching.
//
// Examples:
//
//	".example.com"  → ".com.example."
//	"example.com"   → ".com.example"
func reverseDomain(domain string) string {
	labels := strings.Split(domain, ".")
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	reversed := strings.Join(labels, ".")
	if !strings.HasPrefix(reversed, ".") {
		reversed = "." + reversed
	}
	return reversed
}

// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package base

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-cmp/cmp"
	futils "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/fileutils"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"sigs.k8s.io/yaml"
)

// This file holds the assertions a case uses when two listeners -- one a Gateway
// declares, one a ListenerSet contributes -- must yield the same configuration.
//
// They compare the objects HAProxy was given rather than a transcription of them. An
// expectation of one side would be hand-written from the same understanding as the
// other, so a case built that way asserts our own reading instead of the controller's
// behaviour; comparing the two live objects asks the controller to agree with itself
// across two code paths, which is the property these cases exist for.

// NamingDifference is one difference a parity comparison tolerates: two names that
// denote the same thing on either side -- the resource each frontend belongs to, or the
// route that feeds each of them when they are distinct objects. Every member is
// rewritten to the same placeholder before comparing.
//
// The first pair a caller passes is the pair of declaring resources, and both members
// must be present in their own document: a frontend that lost the origin it was
// programmed for is a defect of its own, and it must not pass by having nothing left to
// compare.
type NamingDifference [2]string

// namingPlaceholder replaces every member of a NamingDifference. Angle brackets cannot
// appear in an object name, so a rewritten document cannot be mistaken for naming
// another object.
const namingPlaceholder = "<naming-difference>"

// originKindPlaceholder replaces the kind of the declaring resource in the origin a
// frontend carries. A Gateway listener and a contributed entry are the two sides of
// these cases, so their kinds are expected to differ and are canonicalised away; the
// names are what the first NamingDifference keeps meaningful.
const originKindPlaceholder = "<origin-kind>"

// portPlaceholder replaces every port number in a document. The two sides of a parity case
// serve the same listener on a port each -- that is why they are two frontends -- so the
// port is a naming difference like the others, and it appears in the frontend name, in the
// bind, and in every map path besides.
const portPlaceholder = "<port>"

// ExpectFrontendsIdenticalModuloNaming fails unless the two frontends the template names
// designate hold the same configuration, once the ports and the declared naming
// differences are canonicalised.
func (b *BaseSuite) ExpectFrontendsIdenticalModuloNaming(templateA, templateB string, differences ...NamingDifference) {
	var diff string
	if !utils.WaitFor(b.IntTest().Ctx, interval, timeout, func() bool {
		textA, ok := b.frontendText(templateA)
		if !ok {
			return false
		}
		textB, ok := b.frontendText(templateB)
		if !ok {
			return false
		}
		ownerA, ownerB := differences[0][0], differences[0][1]
		if !strings.Contains(textA, ownerA) || !strings.Contains(textB, ownerB) {
			// The origin is not there yet, or names something else: keep waiting rather than
			// compare two documents that would agree by both being unattributed.
			return false
		}
		canonicalA := b.canonicalizeNaming(textA, differences)
		canonicalB := b.canonicalizeNaming(textB, differences)
		diff = cmp.Diff(canonicalA, canonicalB)
		return diff == ""
	}) {
		b.T().Fatalf("frontends %s and %s differ beyond naming:\n%s", templateA, templateB, diff)
	}
}

// ExpectMapsIdenticalModuloNaming fails unless every standard map of the two frontends
// holds the same entries, once the ports and the declared naming differences are
// canonicalised.
//
// The maps are where the routing lives: the hostname-to-listener table, the listener/route
// table and the SNI table are what a request is resolved through, and comparing them is
// comparing what the two listeners actually serve.
//
// The listener maps are required to be non-empty on both sides first, so that two
// frontends which programmed nothing cannot pass by being equally empty.
func (b *BaseSuite) ExpectMapsIdenticalModuloNaming(templateA, templateB string, differences ...NamingDifference) {
	required := []string{"listener_exact_match.map", "listener_route_exact_match.map"}
	var diff string
	if !utils.WaitFor(b.IntTest().Ctx, interval, timeout, func() bool {
		for _, mapName := range required {
			contentA, okA := b.mapText(templateA, mapName)
			contentB, okB := b.mapText(templateB, mapName)
			if !okA || !okB || strings.TrimSpace(contentA) == "" || strings.TrimSpace(contentB) == "" {
				return false
			}
		}
		for _, mapName := range StandardMaps {
			contentA, okA := b.mapText(templateA, mapName)
			contentB, okB := b.mapText(templateB, mapName)
			switch {
			case !okA && !okB:
				// Neither frontend programmed this map. It is a difference from the other
				// maps only in that there is nothing to compare.
				continue
			case okA != okB:
				diff = fmt.Sprintf("%s exists for one frontend only (%s: %t, %s: %t)",
					mapName, templateA, okA, templateB, okB)
				return false
			}
			canonicalA := b.canonicalizeNaming(contentA, differences)
			canonicalB := b.canonicalizeNaming(contentB, differences)
			if canonicalA != canonicalB {
				diff = fmt.Sprintf("%s differs beyond naming:\n%s", mapName, cmp.Diff(canonicalA, canonicalB))
				return false
			}
		}
		return true
	}) {
		b.T().Fatalf("maps of %s and %s differ beyond naming: %s", templateA, templateB, diff)
	}
}

// ExpectCrtListsIdenticalModuloNaming fails unless the two frontends serve the same
// certificates, once the ports and the declared naming differences are canonicalised.
//
// The crt-list is where termination is configured: for a case whose two sides terminate
// with a secret each, comparing the two lists is comparing what a TLS handshake on either
// port will offer. The paths they hold carry the secret's own namespace and name, so the
// two sides differ there by construction -- two Secrets cannot share a name in one
// namespace -- and the caller declares that pair like any other naming.
//
// Both lists are required to be non-empty first: two frontends that serve nothing must not
// pass by being equally empty.
func (b *BaseSuite) ExpectCrtListsIdenticalModuloNaming(templateA, templateB string, differences ...NamingDifference) {
	var diff string
	if !utils.WaitFor(b.IntTest().Ctx, interval, timeout, func() bool {
		linesA, errA := b.crtListLines(templateA)
		linesB, errB := b.crtListLines(templateB)
		if errA != nil || errB != nil || len(linesA) == 0 || len(linesB) == 0 {
			return false
		}
		canonicalA := b.canonicalizeNaming(strings.Join(linesA, "\n"), differences)
		canonicalB := b.canonicalizeNaming(strings.Join(linesB, "\n"), differences)
		diff = cmp.Diff(canonicalA, canonicalB)
		return diff == ""
	}) {
		b.T().Fatalf("crt-lists of %s and %s differ beyond naming:\n%s", templateA, templateB, diff)
	}
}

// crtListLines returns the contents of the crt-list HAProxy was given for one frontend.
//
// templateFrontend is the frontend's own name, `hug_<category>_<port>`: the file is named
// after it, and the port it carries is substituted because HAProxy is given the port it
// actually bound. A list that is not on disk is reported as an error rather than as empty,
// so that a caller can tell "serves nothing" from "not written yet".
func (b *BaseSuite) crtListLines(templateFrontend string) ([]string, error) {
	expected := futils.FilePath{
		Dir:      path.Join(b.IntTest().HaproxyCfgDir, "certlists"),
		FileName: "/" + templateFrontend + ".list",
	}
	actualFullPath := utils.SubstitutePortsInString(expected.FullPath(), b.IntTest().PortMap)
	return futils.FilePath{
		Dir:      filepath.Dir(actualFullPath),
		FileName: "/" + filepath.Base(actualFullPath),
	}.ReadLines()
}

// frontendText returns the frontend the template name designates, marshalled as the
// comparison works on. The document is the Data Plane API's own view of the frontend, so
// what is compared is what HAProxy serves, annotations included.
func (b *BaseSuite) frontendText(templateName string) (string, bool) {
	frontends, err := b.test.HaproxyClient.FrontendsGet()
	if err != nil {
		return "", false
	}
	wanted := utils.SubstitutePortsInString(templateName, b.test.PortMap)
	for _, frontend := range frontends {
		if frontend.Name != wanted {
			continue
		}
		text, err := yaml.Marshal(frontend)
		if err != nil {
			return "", false
		}
		return string(text), true
	}
	return "", false
}

// mapText returns the contents of one map of the frontend the template name designates.
// A map that is not on disk is reported as absent rather than as empty: the parity
// assertion compares the two sides on that difference too.
func (b *BaseSuite) mapText(templateFrontend, mapName string) (string, bool) {
	lines, err := b.GetMapFileFrom(path.Join(templateFrontend, mapName))
	if err != nil {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

// canonicalizeNaming rewrites the naming a parity comparison tolerates: every port, the
// declaring resources, and the kind of those resources.
func (b *BaseSuite) canonicalizeNaming(document string, differences []NamingDifference) string {
	return canonicalizeParityDocument(document, b.test.PortMap, differences)
}

// canonicalizeParityDocument is the rewrite itself, without a suite to read a port map
// from: it is what the unit test of this file's own logic calls.
//
// Both port ranges are rewritten, the template values a case addresses its frontends by
// and the derived values they were substituted with: a document that kept one where the
// other side kept the other would differ for a reason the case is not about. The two
// ranges are disjoint -- template ports stop at 31445, derived ports start at 20000 -- so
// no replacement can be rewritten by another. A number that is not a port is left alone;
// backend ports (80, 443) are below the template range, so a backend name or a server port
// keeps the value both sides share.
//
// The longest replacement is applied first. A Gateway listener's identity is a prefix of
// the one a contributed entry carries -- `<gwNs>/<gwName>_<listener>` against
// `<gwNs>/<gwName>_<lsNs>_<lsName>_<listener>`, the two forms listenerKey.String documents
// -- so rewriting the short one first would leave the long one half rewritten, which reads
// as a difference the case does not tolerate.
func canonicalizeParityDocument(document string, ports map[int]int, differences []NamingDifference) string {
	type replacement struct {
		needle      string
		rewrittenAs string
	}
	replacements := make([]replacement, 0, 2*len(ports)+2*len(differences))
	for templatePort, actualPort := range ports {
		replacements = append(replacements,
			replacement{strconv.Itoa(actualPort), portPlaceholder},
			replacement{strconv.Itoa(templatePort), portPlaceholder},
		)
	}
	for _, difference := range differences {
		replacements = append(replacements,
			replacement{difference[0], namingPlaceholder},
			replacement{difference[1], namingPlaceholder},
		)
	}
	slices.SortStableFunc(replacements, func(a, b replacement) int {
		switch {
		case len(a.needle) > len(b.needle):
			return -1
		case len(a.needle) < len(b.needle):
			return 1
		}
		return 0
	})
	for _, r := range replacements {
		document = strings.ReplaceAll(document, r.needle, r.rewrittenAs)
	}
	// The kind of the resource a frontend was programmed for is the other half of its
	// origin. Nothing else in a frontend document names either kind -- the rules speak of
	// listeners, hosts, paths and backends -- so rewriting the two words here cannot hide a
	// difference in what the frontends do.
	document = strings.ReplaceAll(document, "ListenerSet", originKindPlaceholder)
	document = strings.ReplaceAll(document, "Gateway", originKindPlaceholder)
	return document
}

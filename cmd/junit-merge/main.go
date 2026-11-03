// Copyright 2026 HAProxy Technologies LLC
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

// Command junit-merge concatenates JUnit XML files from several conformance
// shards into one, so the report tooling keeps seeing a single run.
package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"os"
)

// suite is one <testsuite> element, kept verbatim apart from its opening tag,
// which is rebuilt from the attributes so nothing else is rewritten.
type suite struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	InnerXML []byte     `xml:",innerxml"`
}

// document is a JUnit file, either wrapped in <testsuites> or a bare suite.
type document struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Suites   []suite    `xml:"testsuite"`
	InnerXML []byte     `xml:",innerxml"`
}

// readSuites returns the suites of one JUnit file.
func readSuites(path string) ([]suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc document
	if err = xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc.XMLName.Local == "testsuite" {
		return []suite{{XMLName: doc.XMLName, Attrs: doc.Attrs, InnerXML: doc.InnerXML}}, nil
	}
	return doc.Suites, nil
}

// merge returns the suites of every path, in order.
func merge(paths []string) ([]suite, error) {
	suites := make([]suite, 0, len(paths))
	for _, path := range paths {
		read, err := readSuites(path)
		if err != nil {
			return nil, err
		}
		suites = append(suites, read...)
	}
	return suites, nil
}

// render writes one <testsuites> document holding every suite.
func render(suites []suite) ([]byte, error) {
	out := &bytes.Buffer{}
	out.WriteString("<testsuites>\n")
	for _, s := range suites {
		out.WriteString("<testsuite")
		for _, attr := range s.Attrs {
			var value bytes.Buffer
			if err := xml.EscapeText(&value, []byte(attr.Value)); err != nil {
				return nil, err
			}
			fmt.Fprintf(out, " %s=%q", attr.Name.Local, value.String())
		}
		out.WriteString(">")
		_, _ = out.Write(s.InnerXML)
		out.WriteString("</testsuite>\n")
	}
	out.WriteString("</testsuites>\n")
	return out.Bytes(), nil
}

func main() {
	log.SetFlags(0)
	output := flag.String("o", "", "file to write; stdout when unset")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("usage: junit-merge -o merged.xml shard-1.xml [shard-2.xml ...]")
	}

	suites, err := merge(flag.Args())
	if err != nil {
		log.Fatalf("junit-merge: %v", err)
	}
	if len(suites) == 0 {
		log.Fatalf("junit-merge: no testsuite in %v", flag.Args())
	}

	content, err := render(suites)
	if err != nil {
		log.Fatalf("junit-merge: %v", err)
	}
	if *output == "" {
		if _, err = os.Stdout.Write(content); err != nil {
			log.Fatalf("junit-merge: %v", err)
		}
		return
	}
	if err = os.WriteFile(*output, content, 0o644); err != nil {
		log.Fatalf("junit-merge: %v", err)
	}
}

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
package docs

import (
	_ "embed"
	"log"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

//revive:disable:deep-exit

type SupportVersion struct {
	Version    string   `yaml:"version"`
	GA         string   `yaml:"ga"`
	MinEOL     string   `yaml:"min_eol"`
	EOLHuman   string   `yaml:"-"`
	GwAPI      []string `yaml:"gw_api"`
	Maintained bool     `yaml:"-"`
}

type Support struct {
	Versions []*SupportVersion `yaml:"versions"`
}

//go:embed lifecycle.yaml
var lifecycle []byte

// GetLifecycle parses lifecycle.yaml and flags versions still maintained,
// computing a human-readable time-to-EOL for each.
func GetLifecycle() (Support, error) {
	support := Support{}
	err := yaml.Unmarshal(lifecycle, &support)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}
	for i := range support.Versions {
		// min_eol is YYYY-MM; treat EOL as the end of that month.
		eolDate, err := time.Parse("2006-01-02", support.Versions[i].MinEOL+"-01")
		if err != nil {
			continue
		}
		eolDate = eolDate.AddDate(0, 1, 0)
		if eolDate.After(time.Now()) {
			support.Versions[i].Maintained = true
			support.Versions[i].EOLHuman = diff(time.Now(), eolDate)
		}
	}

	return support, nil
}

// diff returns an approximate years/months/days span between two dates.
func diff(a, b time.Time) string {
	if a.Location() != b.Location() {
		b = b.In(a.Location())
	}
	if a.After(b) {
		a, b = b, a
	}
	y1, m1, d1 := a.Date()
	y2, m2, d2 := b.Date()

	year := y2 - y1
	month := int(m2 - m1)
	day := d2 - d1
	if day < 0 {
		//revive:disable-next-line:time-date
		t := time.Date(y1, m1, 32, 0, 0, 0, 0, time.UTC)
		day += 32 - t.Day()
		month--
	}
	if month < 0 {
		month += 12
		year--
	}

	result := strconv.Itoa(day) + " day" + addS(day)

	if month > 0 || year > 0 {
		result = strconv.Itoa(month) + " month" + addS(month) + " " + result
	}
	if year > 0 {
		result = strconv.Itoa(year) + " year" + addS(year) + " " + result
	}

	return result
}

func addS(x int) string {
	if x == 1 {
		return ""
	}
	return "s"
}

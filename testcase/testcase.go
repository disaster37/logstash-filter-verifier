// Copyright (c) 2015-2018 Magnus Bäck <magnus@noun.se>

package testcase

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
	unjson "github.com/hashicorp/packer/common/json"
	"github.com/magnusbaeck/logstash-filter-verifier/logging"
	"github.com/magnusbaeck/logstash-filter-verifier/logstash"
	"github.com/mikefarah/yaml/v2"
	"github.com/pkg/errors"
)

// TestCaseSet contains the configuration of a Logstash filter test case.
// Most of the fields are supplied by the user via a JSON file or YAML file.
type TestCaseSet struct {
	// File is the absolute path to the file from which this
	// test case was read.
	File string `json:"-" yaml:"-"`

	// Codec names the Logstash codec that should be used when
	// events are read. This is normally "line" or "json_lines".
	Codec string `json:"codec" yaml:"codec"`

	// IgnoredFields contains a list of fields that will be
	// deleted from the events that Logstash returns before
	// they're compared to the events in ExpectedEevents.
	//
	// This can be used for skipping fields that Logstash
	// populates with unpredictable contents (hostnames or
	// timestamps) that can't be hard-wired into the test case
	// file.
	//
	// It's also useful for the @version field that Logstash
	// always adds with a constant value so that one doesn't have
	// to include that field in every event in ExpectedEvents.
	IgnoredFields []string `json:"ignore" yaml:"ignore"`

	// InputFields contains a mapping of fields that should be
	// added to input events, like "type" or "tags". The map
	// values may be scalar values or arrays of scalar
	// values. This is often important since filters typically are
	// configured based on the event's type or its tags.
	InputFields logstash.FieldSet `json:"fields" yaml:"fields"`

	// InputLines contains the lines of input that should be fed
	// to the Logstash process.
	InputLines []string `json:"input" yaml:"input"`

	// ExpectedEvents contains a slice of expected events to be
	// compared to the actual events produced by the Logstash
	// process.
	ExpectedEvents []logstash.Event `json:"expected" yaml:"expected"`

	// TestCases is a slice of test cases, which include at minimum
	// a pair of an input and an expected event
	// Optionally other information regarding the test case
	// may be supplied.
	TestCases []TestCase `json:"testcases" yaml:"testcases"`

	descriptions []string `json:"descriptions" yaml:"descriptions"`
}

// TestCase is a pair of an input line that should be fed
// into the Logstash process and an expected event which is compared
// to the actual event produced by the Logstash process.
type TestCase struct {
	// InputLines contains the lines of input that should be fed
	// to the Logstash process.
	InputLines []string `json:"input" yaml:"input"`

	// ExpectedEvents contains a slice of expected events to be
	// compared to the actual events produced by the Logstash
	// process.
	ExpectedEvents []logstash.Event `json:"expected" yaml:"expected"`

	// Description contains an optional description of the test case
	// which will be printed while the tests are executed.
	Description string `json:"description" yaml:"description"`
}

// ComparisonError indicates that there was a mismatch when the
// results of a test case was compared against the test case
// definition.
type ComparisonError struct {
	ActualCount   int
	ExpectedCount int
	Mismatches    []MismatchedEvent
}

// MismatchedEvent holds a single tuple of actual and expected events
// for a particular index in the list of events for a test case.
type MismatchedEvent struct {
	Actual   logstash.Event
	Expected logstash.Event
	Index    int
}

var (
	log = logging.MustGetLogger()

	defaultIgnoredFields = []string{"@version"}
)

func (t *TestCaseSet) convertDotFileds() error {

	// Convert fields in input fields
	t.InputFields = parseAllDotProperties(t.InputFields)

	// Convert fields in expected events
	for i, expected := range t.ExpectedEvents {
		t.ExpectedEvents[i] = parseAllDotProperties(expected)
	}

	// Convert  fields in input json string
	if t.Codec == "json_lines" {
		for i, line := range t.InputLines {
			var jsonObj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &jsonObj); err != nil {
				return err
			}
			jsonObj = parseAllDotProperties(jsonObj)
			data, err := json.Marshal(jsonObj)
			if err != nil {
				return err
			}
			t.InputLines[i] = string(data)
		}
	}

	return nil

}

func (t *TestCaseSet) convertIntToFloat64() {

	// Convert fields in expected events
	for i, expected := range t.ExpectedEvents {
		t.ExpectedEvents[i] = convertIntToFloat64(expected)
	}

}

// New reads a test case configuration from a reader and returns a
// TestCase. Defaults to a "line" codec and ignoring the @version
// field. If the configuration being read lists additional fields to
// ignore those will be ignored in addition to @version.
// configType must be json or yaml.
func New(reader io.Reader, configType string) (*TestCaseSet, error) {

	if configType != "json" && configType != "yaml" {
		return nil, errors.New("Config type must be json or yaml")
	}

	tcs := TestCaseSet{
		Codec:       "line",
		InputFields: logstash.FieldSet{},
	}
	buf, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	if configType == "json" {
		if err = unjson.Unmarshal(buf, &tcs); err != nil {
			return nil, err
		}
	} else {
		// Fix issue https://github.com/go-yaml/yaml/issues/139
		yaml.DefaultMapType = reflect.TypeOf(map[string]interface{}{})
		if err = yaml.Unmarshal(buf, &tcs); err != nil {
			return nil, err
		}
	}

	if err = tcs.InputFields.IsValid(); err != nil {
		return nil, err
	}
	tcs.IgnoredFields = append(tcs.IgnoredFields, defaultIgnoredFields...)
	sort.Strings(tcs.IgnoredFields)
	tcs.descriptions = make([]string, len(tcs.ExpectedEvents))
	for _, tc := range tcs.TestCases {
		tcs.InputLines = append(tcs.InputLines, tc.InputLines...)
		tcs.ExpectedEvents = append(tcs.ExpectedEvents, tc.ExpectedEvents...)
		for range tc.ExpectedEvents {
			tcs.descriptions = append(tcs.descriptions, tc.Description)
		}
	}

	// Convert Int to Float64 for compatibily with json.Marshal
	tcs.convertIntToFloat64()
	// Convert dot fields
	if err := tcs.convertDotFileds(); err != nil {
		return nil, err
	}

	log.Debugf("%+v", tcs)

	return &tcs, nil
}

// NewFromFile reads a test case configuration from an on-disk file.
func NewFromFile(path string) (*TestCaseSet, error) {
	abspath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	ext := strings.TrimPrefix(filepath.Ext(abspath), ".")
	log.Debugf("File extension: %s", ext)

	log.Debug("Reading test case file: %s (%s)", path, abspath)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	tcs, err := New(f, ext)
	if err != nil {
		return nil, fmt.Errorf("Error reading/unmarshalling %s: %s", path, err)
	}
	tcs.File = abspath
	return tcs, nil
}

// Compare compares a slice of events against the expected events of
// this test case. If quiet is true,
// the progress messages normally written to stderr will be emitted
// and the output of the diff program will be discarded.
func (tcs *TestCaseSet) Compare(events []logstash.Event, quiet bool) error {
	result := ComparisonError{
		ActualCount:   len(events),
		ExpectedCount: len(tcs.ExpectedEvents),
		Mismatches:    []MismatchedEvent{},
	}

	// Don't even attempt to do a deep comparison of the event
	// lists unless their lengths are equal.
	if result.ActualCount != result.ExpectedCount {
		return result
	}

	// Loop over events
	for i, actualEvent := range events {

		// Get the right description
		if !quiet {
			var description string
			if len(tcs.descriptions[i]) > 0 {
				description = fmt.Sprintf(" (%s)", tcs.descriptions[i])
			}
			fmt.Printf("Comparing message %d of %d from %s%s...\n", i+1, len(events), filepath.Base(tcs.File), description)
		}

		// Remove fields that must be exlude
		for _, ignored := range tcs.IgnoredFields {
			// Ignored fields can be in a sub object
			fieldTree := strings.Split(ignored, ".")
			actualEvent = removeFields(fieldTree, actualEvent)
		}

		// Compare actual events and expected
		if diff := cmp.Diff(actualEvent, tcs.ExpectedEvents[i]); diff != "" {
			result.Mismatches = append(result.Mismatches, MismatchedEvent{actualEvent, tcs.ExpectedEvents[i], i})
			fmt.Printf("%s", diff)
		}
	}
	if len(result.Mismatches) == 0 {
		return nil
	}
	return result
}

func (e ComparisonError) Error() string {
	if e.ActualCount != e.ExpectedCount {
		return fmt.Sprintf("Expected %d event(s), got %d instead.", e.ExpectedCount, e.ActualCount)
	}
	if len(e.Mismatches) > 0 {
		return fmt.Sprintf("%d message(s) did not match the expectations.", len(e.Mismatches))
	}
	return "No error"

}

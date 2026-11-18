package thordata

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSharedContractFixturesAreValidJSON(t *testing.T) {
	paths := []string{
		"testdata/contract-fixtures/serp/basic-request.json",
		"testdata/contract-fixtures/serp/envelopes.json",
		"testdata/contract-fixtures/web-scraper/lifecycle.json",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(body, &value); err != nil {
				t.Fatalf("invalid fixture JSON: %v", err)
			}
		})
	}
}

func TestSupportedDirectoriesMatchTheContract(t *testing.T) {
	if got := len(SupportedEngines()); got != 38 {
		t.Fatalf("engine count = %d", got)
	}
	if got := len(RequestParameters()); got != 194 {
		t.Fatalf("parameter count = %d", got)
	}
	if got := len(SupportedWebScrapers()); got != 7 {
		t.Fatalf("scraper count = %d", got)
	}
	engines := SupportedEngines()
	engines[0] = "changed"
	if SupportedEngines()[0] != "google" {
		t.Fatal("SupportedEngines exposed mutable internal storage")
	}
	params := RequestParameters()
	params[0] = "changed"
	if RequestParameters()[0] == "changed" {
		t.Fatal("RequestParameters exposed mutable internal storage")
	}
}

func TestContractSERPRequestFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/contract-fixtures/serp/basic-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Input map[string]any    `json:"input"`
		Form  map[string]string `json:"form"`
	}
	if err := json.Unmarshal(fixture, &value); err != nil {
		t.Fatal(err)
	}
	payload, err := (&Client{}).buildSearchPayload(SearchParams(value.Input), "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, value.Form) {
		t.Fatalf("normalized form = %#v, want %#v", payload, value.Form)
	}
}

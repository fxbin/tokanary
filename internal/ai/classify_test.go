package ai

import "testing"

func TestClassifyDisabledReturnsNil(t *testing.T) {
	cats, err := ClassifyTitles(Config{}, false, []TitleTokens{{Title: "x"}})
	if err != nil || cats != nil {
		t.Fatalf("disabled should return nil, got %v %v", cats, err)
	}
	cats, err = ClassifyTitles(Config{Endpoint: "http://127.0.0.1:0", Model: "m"}, true, nil)
	if err != nil || cats != nil {
		t.Fatalf("empty items should return nil, got %v %v", cats, err)
	}
}

func TestLoadConfigEmpty(t *testing.T) {
	// env may be set on the host; Ready() just reflects config
	c := Config{}
	if c.Ready() {
		t.Fatal("empty config must not be ready")
	}
}

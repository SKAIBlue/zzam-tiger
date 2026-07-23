package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"tag_name":"v1.3.0"}`))
	}))
	defer server.Close()

	latest, available, err := checkLatest(context.Background(), server.Client(), server.URL, "v1.2.3")
	if err != nil || latest != "v1.3.0" || !available {
		t.Fatalf("checkLatest = latest %q, available %t, error %v", latest, available, err)
	}
}

func TestCheckLatestSkipsDevelopmentBuildWithoutRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("development build made a release request")
		return nil, nil
	})}
	latest, available, err := checkLatest(context.Background(), client, "https://example.invalid", "dev")
	if err != nil || latest != "" || available {
		t.Fatalf("checkLatest(dev) = latest %q, available %t, error %v", latest, available, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestCompareVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{current: "v1.2.3", latest: "v1.2.4", want: true},
		{current: "1.2.3", latest: "v2.0.0", want: true},
		{current: "v1.2.3-beta.1", latest: "v1.2.3", want: true},
		{current: "v1.2.3", latest: "v1.2.3", want: false},
		{current: "v1.3.0", latest: "v1.2.9", want: false},
	}
	for _, test := range tests {
		current, currentOK := parseVersion(test.current)
		latest, latestOK := parseVersion(test.latest)
		if !currentOK || !latestOK {
			t.Fatalf("failed to parse current=%q latest=%q", test.current, test.latest)
		}
		if got := compareVersion(latest, current) > 0; got != test.want {
			t.Errorf("latest %q newer than %q = %t, want %t", test.latest, test.current, got, test.want)
		}
	}
}

func TestParseVersionRejectsDevelopmentAndMalformedValues(t *testing.T) {
	for _, value := range []string{"dev", "", "1.2", "release-1.2.3"} {
		if _, ok := parseVersion(value); ok {
			t.Errorf("parseVersion(%q) succeeded", value)
		}
	}
}

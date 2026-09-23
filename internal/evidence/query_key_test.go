package evidence

import "testing"

// A query parameter that merely contains a sensitive word is not a credential.
// LinkedIn's search URL names the search term in a "keywords" parameter; if that
// were redacted the record would describe a different URL from the one the
// backend fetched, which is exactly the false claim the evidence record exists
// to prevent.
func TestSanitizeURL_KeepsBenignKeywordParameter(t *testing.T) {
	const searchURL = "https://www.linkedin.com/search/results/content/?keywords=acme+corp"

	if got := SanitizeURL(searchURL); got != searchURL {
		t.Errorf("SanitizeURL(%q) = %q, want it unchanged", searchURL, got)
	}
	if HasCredentials(searchURL) {
		t.Errorf("HasCredentials(%q) = true; a search term is not a credential", searchURL)
	}
}

// Genuine credential-shaped query parameters are still redacted, including ones
// whose sensitive word is a segment rather than the whole key.
func TestSanitizeURL_StillRedactsCredentialQueryParameters(t *testing.T) {
	cases := []string{
		"https://example.com/cb?access_token=SECRET",
		"https://example.com/cb?api_key=SECRET",
		"https://example.com/cb?session_id=SECRET",
		"https://example.com/cb?X-Amz-Signature=SECRET",
		"https://example.com/cb?X-Amz-Credential=SECRET",
	}
	for _, raw := range cases {
		got := SanitizeURL(raw)
		if HasCredentials(got) {
			t.Errorf("SanitizeURL(%q) = %q still reports credentials", raw, got)
		}
	}
}

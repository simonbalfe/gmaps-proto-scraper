package main

import "testing"

func TestEmailSetExtractsAndFiltersAddresses(t *testing.T) {
	body := `
    Contact INFO@example.co.uk or support@example.com.
    <a href="mailto:sales@example.org">sales@example.org</a>
    https://www.google.com/maps/place/example/@51.5,-0.1
    fancyapps/ui@4.0
    123456789@sentry.wixpress.com
    641df33d_home-hero-video@2x-poster-00001.jpg
    example@mysite.com
    `
	result := emailSet(body)
	for _, email := range []string{"info@example.co.uk", "support@example.com", "sales@example.org"} {
		if _, ok := result[email]; !ok {
			t.Fatalf("expected %q in %#v", email, result)
		}
	}
	if len(result) != 3 {
		t.Fatalf("unexpected email set: %#v", result)
	}
}

func TestHomepageURL(t *testing.T) {
	actual, err := homepageURL("https://example.com/contact?source=maps#email")
	if err != nil {
		t.Fatal(err)
	}
	if actual != "https://example.com/" {
		t.Fatalf("unexpected homepage URL: %q", actual)
	}
}
